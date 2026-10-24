"""Exercise voice navigation, persisted toggles, keyboard activation and IPv6 rejection."""
import json
import os
from pathlib import Path
import sys
import tempfile

from ui_driver import Driver


def main():
    for mode in ("auto", "legacy"):
        with tempfile.TemporaryDirectory(prefix="hole-voice-ui-", dir=os.environ.get("TMPDIR")) as directory:
            config = Path(directory) / "config.json"
            config.write_text(json.dumps({"connection": {"connection_mode": mode}}))
            driver = Driver(sys.argv[1], directory)
            try:
                driver.navigate(4)
                driver.click(1110, 154)

                def enabled():
                    return json.loads(config.read_text()).get("voice", {}).get("enabled", False)

                if mode == "legacy":
                    assert not enabled(), "Voice enabled on incompatible IPv6 transport"
                    continue
                assert enabled(), "Voice switch was not persisted"
                driver.navigate(5)
                driver.navigate(4)
                driver.click(1110, 154)
                assert not enabled(), "Revisited voice switch did not reflect saved state"
                driver.key("space")
                assert enabled(), "Keyboard activation of focused voice switch failed"
                state = Path(directory) / "run-state.json"
                if state.exists():
                    assert not json.loads(state.read_text()).get("run_requested", False)
            except Exception:
                driver.shot("voice-failure.png")
                raise
            finally:
                driver.close()
    print("PASS: voice navigation, persisted toggles, keyboard activation, stopped intent and IPv6 rejection")


if __name__ == "__main__":
    main()
