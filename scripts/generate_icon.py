"""Render the project's MIT geometric icon or a configured PNG/JPEG image.

No system fonts, stock logos or external image services are used.
"""
from pathlib import Path
from PIL import Image, ImageDraw, ImageOps

root = Path(__file__).resolve().parents[1]


def generate(source=None):
    assets = root / "assets"
    assets.mkdir(exist_ok=True)
    if source:
        if source.stat().st_size > 512 * 1024:
            raise ValueError("Logo must be at most 512 KB")
        with Image.open(source) as original:
            if original.format not in {"PNG", "JPEG"} or not all(16 <= n <= 1024 for n in original.size):
                raise ValueError("Logo must be a PNG/JPEG image, 16-1024 pixels per side")
            original.load()
            thumb = ImageOps.contain(original.convert("RGBA"), (240, 240), Image.Resampling.LANCZOS)
        icon = Image.new("RGBA", (256, 256), (0, 0, 0, 0))
        icon.alpha_composite(thumb, ((256 - thumb.width) // 2, (256 - thumb.height) // 2))
    else:
        icon = Image.new("RGBA", (1024, 1024), (0, 0, 0, 0))
        draw = ImageDraw.Draw(icon)
        def points(values):
            return [(int(x * 4), int(y * 4)) for x, y in values]
        draw.rounded_rectangle((16, 16, 1008, 1008), radius=224, fill="#327bf0")
        draw.polygon(points([(128, 49), (203, 88), (128, 127), (53, 88)]), fill="#ffffff")
        draw.polygon(points([(53, 101), (121, 137), (121, 209), (53, 173)]), fill="#dcecff")
        draw.polygon(points([(135, 137), (203, 101), (203, 173), (135, 209)]), fill="#80e3d3")
        draw.line(points([(91, 69), (164, 108)]), fill="#327bf0", width=24)
        icon = icon.resize((256, 256), Image.Resampling.LANCZOS)
    icon.save(assets / "app.png")
    icon.save(assets / "app.ico", sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)])
    return assets / "app.png"


if __name__ == "__main__":
    generate()
    print("Application icon generated without font glyphs.")
