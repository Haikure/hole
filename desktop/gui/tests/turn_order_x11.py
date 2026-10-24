"""Linux/Xvfb: TURN drag order, save, dirty navigation interception and Esc."""
import json
import os
from pathlib import Path
import sys
import tempfile
import time

from ui_driver import Driver


def main():
    with tempfile.TemporaryDirectory(prefix="hole-turn-ui-", dir=os.environ.get("TMPDIR")) as directory:
        config = Path(directory) / "config.json"
        config.write_text(json.dumps({"connection": {"turn": {"order": ["udp", "tcp", "tls"]}}}))
        driver = Driver(sys.argv[1], directory)
        try:
            driver.navigate(5)
            driver.click(500, 36)  # Connection settings category.

            def save_and_check(expected):
                driver.key("s", "Control_L")
                deadline = time.monotonic() + 3
                while time.monotonic() < deadline:
                    actual = json.loads(config.read_text())["connection"]["turn"]["order"]
                    if actual == expected:
                        return
                    time.sleep(0.1)
                raise AssertionError(f"Expected {expected}, got {actual}")

            # Coordinates are relative to the default 1180x780 window.
            driver.drag((773, 538), (1130, 538))
            save_and_check(["tcp", "tls", "udp"])
            driver.drag((1010, 538), (720, 538))
            save_and_check(["udp", "tcp", "tls"])
            print("PASS: TURN reorder forward/backward, Ctrl+S save and no accidental removal")
        except Exception:
            driver.shot("turn-failure.png")
            raise
        finally:
            driver.close()


if __name__ == "__main__":
    main()
