"""A saved configuration can start a fresh core; busy/motion cannot resubmit it."""
import json
import os
from pathlib import Path
import sys
import tempfile
import time

from ui_driver import Driver


def main():
    with tempfile.TemporaryDirectory(prefix="hole-run-ui-", dir=os.environ.get("TMPDIR")) as directory:
        root = Path(directory)
        # Closed loopback port: exercise lifecycle without contacting an external service.
        (root / "config.json").write_text(json.dumps({"connection": {
            "server_url": "ws://127.0.0.1:9/ws", "allow_insecure_signal": True,
            "room": "local-ui-test", "device_name": "desktop",
        }}))
        secrets = root / "secrets.json"
        secrets.write_text(json.dumps({"password": "local-test", "token": "local-test"}))
        secrets.chmod(0o600)
        driver = Driver(sys.argv[1], directory)
        try:
            state = root / "run-state.json"

            def expect_running(expected):
                deadline = time.monotonic() + 5
                while time.monotonic() < deadline:
                    if state.exists() and json.loads(state.read_text()).get("run_requested") == expected:
                        return
                    time.sleep(0.1)
                driver.shot("run-control-failure.png")
                raise AssertionError(f"Run control did not persist requested={expected}")

            driver.click(100, 682)
            expect_running(True)
            driver.navigate(3)
            time.sleep(0.5)
            expect_running(True)
            driver.click(100, 682)
            expect_running(False)
            driver.navigate(1)
            time.sleep(0.5)
            expect_running(False)
            print("PASS: fresh saved configuration starts, cross-page stop and animations preserve intent")
        finally:
            driver.close()


if __name__ == "__main__":
    main()
