"""Collect the original license notices of Go and npm dependencies for distribution."""
from pathlib import Path
import json
import subprocess

root = Path(__file__).resolve().parents[1]
sections = ["进销存与往来管理系统 V1.1.0 — 第三方软件许可证\n\nSQLite is in the public domain.\nhttps://www.sqlite.org/copyright.html\n\n字体：Noto Sans SC，SIL Open Font License 1.1，保留官方原始字体。\n界面图标：Element Plus Icons，MIT。默认几何应用图标：MIT。\n\n以下为依赖模块随源代码提供的原始许可声明。"]
seen = set()
names = {"license", "license.txt", "license.md", "licence", "licence.txt", "copying", "notice", "notice.txt", "license-mit", "license-apache", "ofl.txt", "icon-license.txt"}

def include(directory, title):
    files = [p for p in directory.iterdir() if p.is_file() and p.name.lower() in names]
    texts = [p.read_text(encoding="utf-8", errors="replace") for p in files]
    content = "\n\n".join(texts)
    if content:
        sections.append("\n" + "=" * 80 + "\n" + title + "\n" + "=" * 80 + "\n" + content)

include(root / "frontend/public/fonts", "Noto Sans SC — SIL Open Font License 1.1\nhttps://github.com/google/fonts/tree/main/ofl/notosanssc\nFont SHA-256: a3041811a78c361b1de50f953c805e0244951c21c5bd412f7232ef0d899af0da")
include(root / "assets", "Default geometric application icon — MIT")
raw = subprocess.check_output(["go", "list", "-m", "-json", "all"], cwd=root).decode("utf-8")
decoder = json.JSONDecoder()
offset = 0
while offset < len(raw):
    while offset < len(raw) and raw[offset].isspace():
        offset += 1
    if offset == len(raw):
        break
    module, offset = decoder.raw_decode(raw, offset)
    if not module.get("Main") and module.get("Dir"):
        include(Path(module["Dir"]), module["Path"] + " " + module.get("Version", ""))

packages = [package for name in ("frontend", "desktop") for package in (root / name / "node_modules").rglob("package.json")]
for package in packages:
    if package.parent.name == "node_modules":
        continue
    try:
        metadata = json.loads(package.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        continue
    name = metadata.get("name", "")
    version = metadata.get("version", "")
    if not name or (name, version) in seen:
        continue
    seen.add((name, version))
    include(package.parent, name + " " + version)

goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], cwd=root).decode().strip())
include(goroot, "Go programming language and standard library")
target = root / "docs" / "第三方许可证.txt"
target.write_text("\n\n".join(sections), encoding="utf-8-sig")
print(f"Collected {len(sections)-1} license sections.")
