"""Real-window service CRUD, unsaved guards, import preview, report and exit checks."""
import json
import os
from pathlib import Path
import sys
import tempfile
import time

from ui_driver import Driver


def eventually(predicate, message):
    deadline = time.monotonic() + 3
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.1)
    raise AssertionError(message)


def main():
    with tempfile.TemporaryDirectory(prefix="hole-workflow-ui-", dir=os.environ.get("TMPDIR")) as directory:
        config = Path(directory) / "config.json"
        entries = [{"entry_id": name, "id": name, "protocol": "tcp", "host": "127.0.0.1",
                    "port": port, "enabled": True}
                   for name, port in (("ssh-alpha", 2222), ("web-beta", 8080))]
        config.write_text(json.dumps({"connection": {"server_url": "wss://example.invalid/ws",
                          "room": "ui-test", "device_name": "desktop"}, "provide": entries}))
        driver = Driver(sys.argv[1], directory)
        read = lambda: json.loads(config.read_text())
        try:
            driver.navigate(2)
            driver.click(1080, 156)
            driver.type("third")
            driver.click(920, 266)
            driver.type("127.0.0.1")
            driver.click(920, 366)
            driver.type("9000")
            driver.click(1110, 500)
            eventually(lambda: len(read()["provide"]) == 3, "Service creation was not saved")
            driver.shot("workflow-created.png")
            driver.click(1008, 230)
            driver.key("a", "Control_L")
            driver.type("ssh-updated")
            driver.click(1110, 500)
            eventually(lambda: read()["provide"][0]["id"] == "ssh-updated", "Service edit was not saved")
            assert read()["provide"][0]["port"] == 2222, "Reused editor retained a different service port"
            driver.click(1058, 230)
            driver.shot("workflow-menu.png")
            driver.click(1020, 315)
            eventually(lambda: read()["provide"][1]["id"] == "ssh-updated", "Service move-down failed")
            driver.click(1058, 230)
            driver.click(1020, 359)
            eventually(lambda: len(read()["provide"]) == 2, "Service deletion failed")
            driver.navigate(4)
            driver.shot("workflow-undo.png")
            driver.click(1070, 732)
            eventually(lambda: len(read()["provide"]) == 3, "Cross-page delete undo failed")
            driver.navigate(2)
            driver.click(1008, 230)
            driver.key("a", "Control_L")
            driver.type("unsaved")
            driver.key("Escape")
            driver.shot("workflow-discard.png")
            assert read()["provide"][0]["id"] != "unsaved", "Unsaved service was written"
            driver.key("Escape")
            # Canceling must return to the editor; accepting discard closes it.
            driver.key("Escape")
            driver.click(818, 434)
            driver.navigate(5)
            driver.click(450, 278)
            driver.key("a", "Control_L")
            driver.type("wss://draft.invalid/ws")
            driver.click(90, 292)  # Voice: navigation must be intercepted.
            driver.shot("workflow-dirty.png")
            driver.key("Escape")
            assert read()["connection"]["server_url"] == "wss://example.invalid/ws"
            driver.click(90, 292)
            driver.click(714, 432)
            driver.navigate(5)
            driver.click(1058, 34)  # Configuration files category.
            fixture = Path(directory) / "import.yaml"
            fixture.write_text("room: imported-room\ndevice_name: imported-device\n")
            driver.click(940, 390)
            driver.type(str(fixture))
            driver.shot("workflow-import-form.png")
            # File path / preview positions are checked against the current form.
            driver.click(800, 463)
            driver.shot("workflow-import-preview.png")
            assert read()["connection"]["room"] == "ui-test", "Preview mutated current configuration"
            driver.key("Escape")
            driver.navigate(3)
            driver.click(1000, 34)
            driver.scroll(steps=18)
            driver.shot("workflow-report.png")
            driver.click(322, 747)
            driver.shot("workflow-report-generated.png")
            driver.click(90, 748)
            driver.shot("workflow-quit.png")
            driver.key("Escape")
            assert driver.process.poll() is None, "Canceling exit closed the application"
            state = Path(directory) / "run-state.json"
            if state.exists():
                assert not json.loads(state.read_text()).get("run_requested", False)
            print("PASS: service CRUD/order/undo, dirty forms, import preview, report and exit cancellation")
        except Exception:
            driver.shot("workflow-failure.png")
            raise
        finally:
            driver.close()


if __name__ == "__main__":
    main()
