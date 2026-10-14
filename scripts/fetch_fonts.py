"""Fetch the original, commercially usable Noto Sans SC font and its OFL notice."""
from pathlib import Path
import hashlib
import json
import urllib.request

root = Path(__file__).resolve().parents[1]
target = root / "frontend" / "public" / "fonts"
target.mkdir(parents=True, exist_ok=True)
base = "https://raw.githubusercontent.com/google/fonts/main/ofl/notosanssc/"
files = {"NotoSansSC.ttf": "NotoSansSC%5Bwght%5D.ttf", "OFL.txt": "OFL.txt"}
expected = {"NotoSansSC.ttf": "a3041811a78c361b1de50f953c805e0244951c21c5bd412f7232ef0d899af0da", "OFL.txt": "1c05c68c34f9708415aada51f17e1b0092d2cea709bf4a94cd38114f9e73d7d9"}
records = []
for name, source in files.items():
    url = base + source
    request = urllib.request.Request(url, headers={"User-Agent": "InventoryDesktop-Build/1.1"})
    with urllib.request.urlopen(request, timeout=90) as response:
        data = response.read()
    if name.endswith(".ttf") and data[:4] != b"\x00\x01\x00\x00":
        raise ValueError("Unexpected font file format")
    if name == "OFL.txt" and b"SIL OPEN FONT LICENSE Version 1.1" not in data:
        raise ValueError("Unexpected license notice")
    if hashlib.sha256(data).hexdigest() != expected[name]:
        raise ValueError("Upstream file changed; review its license and checksum before updating: " + name)
    (target / name).write_bytes(data)
    records.append({"file": name, "source": url, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest()})
    print(name, len(data), records[-1]["sha256"], flush=True)
(target / "SOURCES.json").write_text(json.dumps({"font": "Noto Sans SC", "license": "SIL Open Font License 1.1", "modified": False, "files": records}, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
