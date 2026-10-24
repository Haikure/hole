"""Deterministic page/theme/scale inspection. Artifacts stay in project .cache/.

xvfb-run -a -s "-screen 0 3400x2200x24" python3 desktop/gui/tests/visual_matrix.py BINARY
"""
import itertools
import json
import os
from pathlib import Path
import subprocess
import sys

from PIL import Image, ImageDraw
from ui_driver import PAGES


def main():
    binary = Path(sys.argv[1]).resolve()
    output = Path(os.environ.get("HOLE_ROOT", Path(__file__).resolve().parents[3])) / ".cache" / "desktop-ui"
    output.mkdir(parents=True, exist_ok=True)
    cases = []
    # Every page in both modes at all target window sizes.
    for mode, size in itertools.product(("light", "dark"), ("860x560", "1180x780", "1600x1000")):
        cases.append((mode, "blue", size, "1", "running", None))
    # All pages in each requested scale, both modes.
    for mode, scale in itertools.product(("light", "dark"), ("1.25", "1.5", "2")):
        cases.append((mode, "blue", "1180x780", scale, "running", None))
    # Accent checks include home, service drawer controls and appearance controls.
    for mode, accent in itertools.product(("light", "dark"), ("teal", "violet", "amber", "rose")):
        cases.append((mode, accent, "1180x780", "1", "running", "overview,mappings,appearance"))
    for scenario in ("empty", "reconnecting", "error", "long-list"):
        cases.append(("light", "blue", "860x560", "1", scenario, "overview,mappings,devices,voice"))
    tiles = []
    for mode, accent, size, scale, scenario, pages in cases:
        name = f"{mode}-{accent}-{size}-{scale}-{scenario}"
        root = output / name
        config = root / "config"
        config.mkdir(parents=True, exist_ok=True)
        shots = root / "shots"
        shots.mkdir(exist_ok=True)
        (config / "config.json").write_text(json.dumps({"prefs": {"theme_mode": mode, "accent": accent}}))
        expected = set(pages.split(",")) if pages else PAGES
        env = dict(os.environ, HOLE_DESKTOP_CONFIG_DIR=str(config), SLINT_BACKEND="winit-software",
                   SLINT_SCALE_FACTOR=scale, HOLE_DESKTOP_SCREENSHOT_DIR=str(shots),
                   HOLE_DESKTOP_SCREENSHOT_SIZE=size, HOLE_DESKTOP_SCREENSHOT_SCENARIO=scenario,
                   HOLE_DESKTOP_SCREENSHOT_PAGES=pages or "")
        with (root / "gui.log").open("w") as log:
            subprocess.run([str(binary)], env=env, stderr=log, check=True, timeout=35)
        actual = {path.stem for path in shots.glob("*.ppm")}
        assert expected <= actual, f"{name}: missing {expected - actual}"
        for page in sorted(expected):
            path = shots / f"{page}.ppm"
            image = Image.open(path)
            width, height = (int(value) for value in size.split("x"))
            expected_size = (round(width * float(scale)), round(height * float(scale)))
            assert image.size == expected_size, (name, page, image.size, expected_size)
            assert len(image.getcolors(image.width * image.height)) > 30, f"{name}/{page}: empty render"
            image.thumbnail((354, 234))
            tile = Image.new("RGB", (360, 266), "#eeeeee")
            tile.paste(image, (3, 28))
            ImageDraw.Draw(tile).text((4, 4), f"{name} / {page}", fill="black")
            tiles.append(tile)
        print(f"PASS {name}: {len(expected)} named pages", flush=True)
    for index in range(0, len(tiles), 24):
        batch = tiles[index:index + 24]
        sheet = Image.new("RGB", (360 * 4, 266 * ((len(batch) + 3) // 4)), "white")
        for i, tile in enumerate(batch):
            sheet.paste(tile, ((i % 4) * 360, (i // 4) * 266))
        sheet.save(output / f"contact-{index // 24:02}.png")
    print(f"PASS: {len(cases)} cases / {len(tiles)} screenshots; {output}")


if __name__ == "__main__":
    main()
