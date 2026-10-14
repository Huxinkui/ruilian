"""Install and exercise the actual NSIS deliverable without using business data."""
from datetime import datetime, timezone
from pathlib import Path
import base64
import ctypes
import hashlib
import json
import os
import sqlite3
import struct
import subprocess
import sys
import time
import threading
import uuid
import winreg

sys.stdout.reconfigure(encoding="utf-8")
ROOT = Path(__file__).resolve().parents[1]
BRAND = json.loads((ROOT / "branding.json").read_text(encoding="utf-8-sig"))
BUILD = json.loads((ROOT / "release/版本与校验.json").read_text(encoding="utf-8-sig"))
PACKAGE = json.loads((ROOT / "desktop/package.json").read_text(encoding="utf-8"))
INSTALLER = ROOT / "release" / BUILD["file"]
RUN = ROOT / "artifacts" / ("桌面安装验收 " + datetime.now().strftime("%Y%m%d-%H%M%S"))
INSTALL = RUN / "安装目录"
DATA = RUN / "业务数据"
MAIN = INSTALL / (BRAND["desktop_name"] + ".exe")
GUID = str(uuid.uuid5(uuid.UUID("50e065bc-3134-11e6-9bab-38c9862bdaf3"), PACKAGE["build"]["appId"]))
UNINSTALL_KEY = "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\" + GUID
INSTALL_KEY = "Software\\" + GUID
HIDDEN = subprocess.CREATE_NO_WINDOW
ENV = {**os.environ, "RUILIAN_DATA_DIR": str(DATA), "RUILIAN_TEST_DATA": str(DATA),
       "RUILIAN_TEST_HIDDEN": "1", "RUILIAN_ELECTRON_PATH": str(MAIN), "PYTHONIOENCODING": "utf-8"}
results = []
report = {"passed": False, "installer": str(INSTALLER), "installer_sha256": BUILD["sha256"],
          "run_directory": str(RUN), "installed_executable": str(MAIN), "checkpoints": results}
report_path = ROOT / "artifacts/release-verification.json"
shortcuts_before = {}
installed = False


def save_report():
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")


def check(name, **details):
    results.append({"name": name, **details})
    print(json.dumps(results[-1], ensure_ascii=False), flush=True)
    save_report()


def sha(file):
    digest = hashlib.sha256()
    with file.open("rb") as stream:
        for chunk in iter(lambda: stream.read(8 * 1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def within(file, parent):
    resolved, boundary = file.resolve(), parent.resolve()
    assert resolved != boundary and resolved.is_relative_to(boundary), "Path outside the disposable test directory"
    return resolved


def registry(hive, name, view=winreg.KEY_WOW64_64KEY):
    try:
        with winreg.OpenKey(hive, name, 0, winreg.KEY_READ | view) as key:
            values = {}
            for i in range(winreg.QueryInfoKey(key)[1]):
                field, value, _ = winreg.EnumValue(key, i)
                values[field] = value
            return values
    except FileNotFoundError:
        return None


def command(args, timeout=90, **kwargs):
    result = subprocess.run(args, cwd=ROOT, env=ENV, creationflags=HIDDEN, capture_output=True,
                            encoding="utf-8", errors="replace", timeout=timeout, **kwargs)
    assert result.returncode == 0, (str(args)[:120], result.returncode, result.stdout[-2000:], result.stderr[-2000:])
    return result


def client_test(name):
    with subprocess.Popen(["node", "tests/" + name], cwd=ROOT / "frontend", env=ENV, creationflags=HIDDEN,
                          stdout=subprocess.PIPE, stderr=subprocess.STDOUT, encoding="utf-8", errors="replace") as process:
        timer = threading.Timer(600, process.kill)
        timer.start()
        try:
            with (RUN / (name + ".log")).open("w", encoding="utf-8") as log:
                for line in process.stdout:
                    log.write(line)
                    log.flush()
                    print(line, end="", flush=True)
            assert process.wait() == 0, "Installed-client test failed: " + name + "; see its log in " + str(RUN)
        finally:
            timer.cancel()


def shell_folder(number):
    buffer = ctypes.create_unicode_buffer(260)
    if ctypes.windll.shell32.SHGetFolderPathW(None, number, None, 0, buffer) != 0:
        raise OSError("Cannot resolve Windows shortcut directory")
    return Path(buffer.value)


def pe(file):
    with file.open("rb") as stream:
        assert stream.read(2) == b"MZ"
        stream.seek(0x3C)
        offset = struct.unpack("<I", stream.read(4))[0]
        stream.seek(offset)
        assert stream.read(4) == b"PE\0\0"
        machine = struct.unpack("<H", stream.read(2))[0]
        stream.seek(offset + 24)
        magic = struct.unpack("<H", stream.read(2))[0]
        stream.seek(offset + 24 + 68)
        subsystem = struct.unpack("<H", stream.read(2))[0]
    return machine, magic, subsystem


def client_processes():
    script = "[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false); $items = @(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($env:INVENTORY_ACCEPTANCE_INSTALL + '\\', [System.StringComparison]::OrdinalIgnoreCase) } | Select-Object ProcessId, ExecutablePath); ConvertTo-Json -InputObject $items -Compress"
    env = {**ENV, "INVENTORY_ACCEPTANCE_INSTALL": str(INSTALL)}
    output = subprocess.check_output(["powershell.exe", "-NoProfile", "-Command", script], env=env, creationflags=HIDDEN)
    return json.loads(output.decode("utf-8-sig", errors="replace"))


def stop_test_processes():
    within(INSTALL, RUN)
    for item in client_processes():
        # Only binaries below this run's unique installation path are eligible.
        # No process name matching or global application termination is used.
        if not Path(item["ExecutablePath"]).resolve().is_relative_to(INSTALL.resolve()):
            raise AssertionError("Unexpected process path during test cleanup")
        subprocess.run(["taskkill.exe", "/PID", str(item["ProcessId"]), "/T", "/F"],
                       capture_output=True, creationflags=HIDDEN, timeout=20)


def uninstall():
    within(INSTALL, RUN)
    entry = registry(winreg.HKEY_CURRENT_USER, UNINSTALL_KEY)
    if entry:
        location = registry(winreg.HKEY_CURRENT_USER, INSTALL_KEY)
        assert location and Path(location["InstallLocation"]).resolve() == INSTALL.resolve(), "Uninstaller registry points outside this test installation"
    uninstaller = INSTALL / ("Uninstall " + BRAND["desktop_name"] + ".exe")
    within(uninstaller, INSTALL)
    if uninstaller.exists():
        command([str(uninstaller), "/S", "/currentuser"], timeout=90)
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline and (MAIN.exists() or registry(winreg.HKEY_CURRENT_USER, UNINSTALL_KEY)):
        time.sleep(0.2)
    assert not MAIN.exists() and registry(winreg.HKEY_CURRENT_USER, UNINSTALL_KEY) is None, "Test uninstall did not finish"


try:
    within(RUN, ROOT / "artifacts")
    RUN.mkdir(parents=True, exist_ok=False)
    save_report()
    assert INSTALLER.exists() and sha(INSTALLER) == BUILD["sha256"]
    assert not PACKAGE["build"]["nsis"]["deleteAppDataOnUninstall"]
    for hive in (winreg.HKEY_CURRENT_USER, winreg.HKEY_LOCAL_MACHINE):
        for view in (winreg.KEY_WOW64_32KEY, winreg.KEY_WOW64_64KEY):
            for name in (INSTALL_KEY, UNINSTALL_KEY):
                assert registry(hive, name, view) is None, "An existing installation was detected; automatic testing will not modify it"
    shortcut_paths = [shell_folder(number) / (BRAND["shortcut_name"] + ".lnk") for number in (0x10, 0x02, 0x19, 0x17)]
    for file in shortcut_paths:
        shortcuts_before[file] = file.read_bytes() if file.exists() else None
    check("installer_checksum_and_existing_installation_protection", bytes=INSTALLER.stat().st_size)

    # NSIS /D is a final, unquoted parameter. This is a direct CreateProcess
    # invocation (shell=False), not a string passed through cmd/PowerShell.
    within(INSTALL, RUN)
    invocation = subprocess.list2cmdline([str(INSTALLER), "/S", "/currentuser"]) + " /D=" + str(INSTALL)
    installed = True
    command(invocation, executable=str(INSTALLER), timeout=180)
    assert MAIN.exists(), "NSIS did not install into the requested Unicode path"
    assert registry(winreg.HKEY_CURRENT_USER, UNINSTALL_KEY)["DisplayName"] == BRAND["app_name"]
    entry = registry(winreg.HKEY_CURRENT_USER, UNINSTALL_KEY)
    assert Path(registry(winreg.HKEY_CURRENT_USER, INSTALL_KEY)["InstallLocation"]).resolve() == INSTALL.resolve()
    assert entry["DisplayVersion"] == BUILD["version"]
    check("actual_nsis_install_and_windows_registration", installation=str(INSTALL), version=entry["DisplayVersion"])

    assert all(file.exists() for file in shortcut_paths[:2]), "Desktop or Start Menu shortcut missing"
    script = "[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false); $w = New-Object -ComObject WScript.Shell; $items = @(foreach ($linkPath in (ConvertFrom-Json -InputObject $env:INVENTORY_ACCEPTANCE_LINKS)) { $s = $w.CreateShortcut([string]$linkPath); [PSCustomObject]@{ Target = $s.TargetPath; Icon = $s.IconLocation } }); ConvertTo-Json -InputObject $items -Compress"
    links = subprocess.check_output(["powershell.exe", "-NoProfile", "-Command", script],
             env={**ENV, "INVENTORY_ACCEPTANCE_LINKS": json.dumps([str(f) for f in shortcut_paths[:2]])}, creationflags=HIDDEN)
    for link in json.loads(links.decode("utf-8-sig", errors="replace")):
        assert Path(link["Target"]).resolve() == MAIN.resolve(), {"shortcut_target": link["Target"], "expected": str(MAIN)}
    check("configured_desktop_and_start_menu_shortcuts")

    backend = INSTALL / "resources/backend/数据服务.exe"
    assert pe(MAIN) == (0x8664, 0x20B, 2) and pe(backend) == (0x8664, 0x20B, 2)
    assert sha(MAIN) == sha(ROOT / "release/client/win-unpacked" / MAIN.name)
    assert sha(backend) == BUILD["backend_sha256"]
    assert (INSTALL / "LICENSE.electron.txt").exists() and (INSTALL / "LICENSES.chromium.html").exists()
    notice = (INSTALL / "resources/第三方许可证.txt").read_text(encoding="utf-8-sig")
    assert "SIL OPEN FONT LICENSE Version 1.1" in notice and "2020-PRESENT Element Plus" in notice
    private = (ROOT / "publisher-private/issuer-private.pem").read_bytes().strip()
    seed = base64.b64decode(b"".join(line for line in private.splitlines() if not line.startswith(b"---")))[-32:]
    files = [file for file in INSTALL.rglob("*") if file.is_file()]
    for file in files:
        assert file.suffix.lower() not in {".pem", ".db", ".sqlite", ".license"}, "Customer package contains a private/data file"
        assert file.name != "授权管理工具.exe"
        tail = b""
        with file.open("rb") as stream:
            for chunk in iter(lambda: stream.read(8 * 1024 * 1024), b""):
                data = tail + chunk
                assert private not in data and seed not in data, "Issuer secret found in customer resources"
                tail = data[-len(private):]
    asar_script = "const a=require('@electron/asar'),c=require('node:crypto');const p=process.argv[1];console.log(JSON.stringify({files:a.listPackage(p),font:c.createHash('sha256').update(a.extractFile(p,'fonts/NotoSansSC.ttf')).digest('hex'),branding:JSON.parse(a.extractFile(p,'branding.json').toString())}));"
    archive = json.loads(subprocess.check_output(["node", "-e", asar_script, str(INSTALL / "resources/app.asar")], cwd=ROOT / "desktop", creationflags=HIDDEN).decode("utf-8"))
    assert archive["font"] == "a3041811a78c361b1de50f953c805e0244951c21c5bd412f7232ef0d899af0da"
    assert archive["branding"]["app_name"] == BRAND["app_name"]
    allowed = {"main.cjs", "preload.cjs", "loading.html", "branding.json", "package.json", "fonts", "images"}
    assert all(name.lstrip("/\\").replace("\\", "/").split("/")[0] in allowed for name in archive["files"])
    (RUN / "installed-resources.json").write_text(json.dumps(archive, ensure_ascii=False, indent=2), encoding="utf-8")
    check("installed_payload_x64_gui_original_font_licenses_and_no_issuer_secrets", files=len(files), font_sha256=archive["font"])

    if client_processes():
        stop_test_processes()
    for test, result_file in (("e2e.mjs", ROOT / "artifacts/screenshots/ui-verification.json"), ("client.mjs", ROOT / "artifacts/client-verification.json")):
        print("Running installed-client acceptance: " + test, flush=True)
        client_test(test)
        result = json.loads(result_file.read_text(encoding="utf-8"))
        assert result["passed"]
        (RUN / result_file.name).write_bytes(result_file.read_bytes())
        check("installed_client_" + test, checkpoints=len(result["checkpoints"]))

    assert not client_processes(), "Client left background processes running after exit"
    database = DATA / "ruilian.db"
    before = sha(database)
    with sqlite3.connect(f"file:{database.as_posix()}?mode=ro", uri=True) as db:
        assert db.execute("SELECT SUM(quantity) FROM inventory").fetchone()[0] == 360000
    uninstall()
    installed = False
    assert database.exists() and sha(database) == before, "Uninstall changed customer data"
    check("actual_uninstall_removes_program_and_retains_sqlite", retained_database=str(database))
    report["passed"] = True
    print("INSTALLED_DESKTOP_RELEASE_ACCEPTANCE_PASSED", flush=True)
except Exception as error:
    report["error"] = repr(error)
    raise
finally:
    if installed:
        try:
            stop_test_processes()
            uninstall()
        except Exception as error:
            report["cleanup_error"] = str(error)
            report["passed"] = False
    for file, original in shortcuts_before.items():
        if original is not None:
            file.write_bytes(original)
        elif file.exists():
            # Remove only the exact shortcut created by this run, after the
            # uninstaller has completed. Never remove a directory recursively.
            if registry(winreg.HKEY_CURRENT_USER, UNINSTALL_KEY) is None:
                file.unlink()
    report["completed_at"] = datetime.now(timezone.utc).isoformat()
    save_report()
