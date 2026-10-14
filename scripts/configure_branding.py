"""Apply branding.json to the frontend, desktop installer and Windows metadata."""
from pathlib import Path
import hashlib
import html
import json
import re
import shutil
import unicodedata

from generate_icon import generate

root = Path(__file__).resolve().parents[1]
config = json.loads((root / "branding.json").read_text(encoding="utf-8-sig"))
limits = {"app_name": 64, "brand_short": 12, "desktop_name": 50, "installer_name": 60, "shortcut_name": 50, "publisher": 100, "copyright": 150, "logo_file": 2048}
if set(config) != set(limits):
    raise ValueError("branding.json contains missing or unknown settings")
for key, maximum in limits.items():
    value = config[key]
    if not isinstance(value, str) or len(value) > maximum or any(unicodedata.category(c) in {"Cc", "Cf", "Zl", "Zp"} for c in value):
        raise ValueError("Invalid branding setting: " + key)
    config[key] = value.strip()
    if not config[key] and key not in {"publisher", "copyright", "logo_file"}:
        raise ValueError("Empty branding setting: " + key)
for key in ("desktop_name", "installer_name", "shortcut_name"):
    value = config[key]
    if re.search(r'[<>:"/\\|?*${}]', value) or value.endswith(".") or value.split(".")[0].upper() in {"CON", "PRN", "AUX", "NUL", *("COM"+str(n) for n in range(1, 10)), *("LPT"+str(n) for n in range(1, 10))}:
        raise ValueError("Invalid Windows filename: " + key)

def write_json(target, data):
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

source = (root / config["logo_file"]).resolve() if config["logo_file"] else None
icon = generate(source)
identity = {key: config[key] for key in ("app_name", "brand_short")}
identity["brand_logo"] = ""
write_json(root / "internal/branding/defaults.json", identity)
write_json(root / "frontend/src/default-brand.json", identity)
write_json(root / "desktop/branding.json", {**identity, "desktop_name": config["desktop_name"]})
for directory in (root / "frontend/public", root / "desktop/images"):
    directory.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(icon, directory / "brand-default.png")

font_root = root / "frontend/public/fonts"
provenance = json.loads((font_root / "SOURCES.json").read_text(encoding="utf-8"))
for record in provenance["files"]:
    if hashlib.sha256((font_root / record["file"]).read_bytes()).hexdigest() != record["sha256"]:
        raise ValueError("Font or license file checksum mismatch: " + record["file"])
for target in (root / "desktop/fonts", root / "cmd/licensegen/assets"):
    target.mkdir(parents=True, exist_ok=True)
    for filename in ("NotoSansSC.ttf", "OFL.txt", "SOURCES.json"):
        shutil.copyfile(font_root / filename, target / filename)

package_path = root / "desktop/package.json"
package = json.loads(package_path.read_text(encoding="utf-8"))
package["description"] = config["app_name"] + "桌面客户端"
package["author"] = config["publisher"]
build = package["build"]
build["productName"] = config["desktop_name"]
build["copyright"] = config["copyright"]
build["files"] = ["main.cjs", "preload.cjs", "loading.html", "branding.json", "package.json", "fonts/**", "images/**"]
build["extraResources"] = [
    {"from": "../artifacts/desktop-backend/数据服务.exe", "to": "backend/数据服务.exe"},
    {"from": "../assets/app.ico", "to": "app.ico"},
    {"from": "../docs/使用说明.md", "to": "使用说明.txt"},
    {"from": "../docs/第三方许可证.txt", "to": "第三方许可证.txt"},
    {"from": "../docs/字体图标与品牌配置.md", "to": "字体图标与品牌配置.txt"},
]
build["win"]["icon"] = "../assets/app.ico"
build["win"]["artifactName"] = config["installer_name"] + "-${version}-${arch}.${ext}"
build["nsis"]["shortcutName"] = config["shortcut_name"]
build["nsis"]["uninstallDisplayName"] = config["app_name"]
write_json(package_path, package)

def rc_text(value):
    return value.replace("\\", "\\\\").replace('"', '\\"')
rc_path = root / "assets/ruilian.rc"
rc = rc_path.read_text(encoding="utf-8-sig").replace('1 ICON "ruilian.ico"', '1 ICON "app.ico"')
for key, value in {"CompanyName": config["publisher"], "FileDescription": config["app_name"] + "数据服务", "OriginalFilename": "数据服务.exe", "ProductName": config["app_name"]}.items():
    rc = re.sub(r'VALUE "' + key + r'", "[^\n]*"', lambda _: 'VALUE "' + key + '", "' + rc_text(value) + r'\0"', rc)
rc_path.write_text(rc, encoding="utf-8")

template = (root / "desktop/loading.template.html").read_text(encoding="utf-8")
(root / "desktop/loading.html").write_text(template.replace("{{APP_NAME}}", html.escape(config["app_name"])), encoding="utf-8")
index_path = root / "frontend/index.html"
index = index_path.read_text(encoding="utf-8")
index = re.sub(r"<title>.*?</title>", lambda _: "<title>" + html.escape(config["app_name"]) + "</title>", index)
index = index.replace('type="image/svg+xml" href="/favicon.svg"', 'type="image/png" href="/brand-default.png"')
index_path.write_text(index, encoding="utf-8")
print("Branding configured: " + config["app_name"])
