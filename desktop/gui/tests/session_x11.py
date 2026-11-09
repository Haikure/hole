"""Real keyboard save/navigation and scroll retention, using isolated storage."""
import json
import os
from pathlib import Path
import sys
import tempfile
import time

from PIL import Image, ImageChops
from ui_driver import Driver


def main():
    with tempfile.TemporaryDirectory(prefix="hole-session-ui-", dir=os.environ.get("TMPDIR")) as directory:
        config = Path(directory) / "config.json"
        config.write_text(json.dumps({
            "connection": {"server_url": "wss://example.invalid/ws", "room": "original", "device_name": "desktop"},
            "provide": [{"entry_id": f"entry-{i}", "id": f"service-{i:02}", "protocol": "tcp",
                         "host": "127.0.0.1", "port": 8000 + i, "enabled": True} for i in range(30)],
        }))
        driver = Driver(sys.argv[1], directory)
        try:
            driver.navigate(2)
            driver.scroll(steps=9)
            driver.move(100, 450)
            time.sleep(0.4)
            driver.shot("session-scroll-before.png")
            driver.navigate(4)
            driver.navigate(2)
            driver.move(100, 450)
            time.sleep(0.4)
            driver.shot("session-scroll-after.png")
            output = Path(os.environ.get("HOLE_ROOT", Path(__file__).resolve().parents[3])) / ".cache" / "desktop-ui" / "interactions"
            before = Image.open(output / "session-scroll-before.png").crop((240, 120, 1140, 740))
            after = Image.open(output / "session-scroll-after.png").crop((240, 120, 1140, 740))
            assert ImageChops.difference(before, after).getbbox() is None, "Page navigation lost scroll position"
            driver.navigate(5)
            # First input on the connection form; save remains accessible without scrolling.
            driver.click(450, 316)
            driver.key("a", "Control_L")
            driver.type("  wss://normalized.invalid/ws  ")
            driver.key("s", "Control_L")
            deadline = time.monotonic() + 3
            while time.monotonic() < deadline:
                if json.loads(config.read_text())["connection"]["server_url"] == "wss://normalized.invalid/ws":
                    break
                time.sleep(0.1)
            else:
                raise AssertionError("Normalized connection save did not reach disk")
            driver.navigate(2)
            driver.shot("session-after-save.png")
            print("PASS: retained service scroll and normalized keyboard save/navigation")
        except Exception:
            driver.shot("session-failure.png")
            raise
        finally:
            driver.close()


if __name__ == "__main__":
    main()
