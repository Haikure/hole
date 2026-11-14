"""Yororen UI window smoke, navigation, forms and read-only process browsing."""
import json
from pathlib import Path
import shutil
import sys
import tempfile
import time

from window_x11 import Window

ARTIFACTS = Path(__file__).resolve().parents[3] / ".cache/desktop-ui/yororen"


def calls(root):
    return [json.loads(line)["method"] for line in (root / "rpc.jsonl").read_text().splitlines()]


def stored(root):
    return json.loads((root / "config.json").read_text())


def main():
    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="hole-yororen-ui-") as directory:
        root = Path(directory)
        config = {"connection": {"server_url": "wss://fixture.invalid/ws", "room": "room", "device_name": "desktop"},
                  "prefs": {"theme_mode": "light"}, "provide": [], "consume": []}
        (root / "config.json").write_text(json.dumps(config))
        (root / "secrets.json").write_text(json.dumps({"password": "fixture", "token": "fixture"}))
        fixture = root / "core-fixture"
        shutil.copyfile(Path(__file__).with_name("core_fixture.py"), fixture)
        fixture.chmod(0o700)
        window = Window(sys.argv[1], root, {"HOLE_CORE": str(fixture)})
        try:
            window.shot(ARTIFACTS / "home-light.png")
            for index, name in [(2, "services"), (3, "devices"), (4, "voice"), (5, "settings")]:
                window.navigate(index)
                window.shot(ARTIFACTS / f"{name}-light.png")
            assert not ({"start", "stop", "apply_config", "network_changed", "renominate_transports"} & set(calls(root))), "navigation changed network state"
            window.click(562, 94)
            window.shot(ARTIFACTS / "appearance-light.png")
            window.click(550, 219)
            window.click(505, 314)
            window.shot(ARTIFACTS / "appearance-dark.png")
            window.click(1140, 488)
            assert stored(root)["prefs"]["theme_mode"] == "dark"
            assert stored(root)["prefs"]["accent"] == "violet"
            assert "apply_config" not in calls(root), "theme save changed network configuration"
            time.sleep(5.2)
            window.navigate(1)
            window.shot(ARTIFACTS / "home-dark.png")
            window.navigate(5)
            window.click(443, 219)
            window.click(300, 314)
            window.shot(ARTIFACTS / "appearance-restored.png")
            window.click(1140, 488)
            assert stored(root)["prefs"]["theme_mode"] == "light", stored(root)["prefs"]
            window.navigate(2)
            window.click(675, 287)
            window.shot(ARTIFACTS / "service-editor.png")
            window.click(1000, 130)
            window.type("web")
            window.click(1000, 320)
            window.type("8080")
            window.click(1150, 776)
            time.sleep(0.4)
            assert stored(root)["provide"][0]["id"] == "web", "service form did not save"
            window.shot(ARTIFACTS / "services-configured.png")
            window.navigate(3)
            # Dismiss save feedback before capturing the process page.
            window.click(1172, 114)
            window.click(850, 342)
            time.sleep(2)
            window.shot(ARTIFACTS / "punch-process.png")
            assert "nat_punch_process" in calls(root), "process page did not read core records"
            window.resize(1240, 1040)
            window.scroll(steps=7)
            window.shot(ARTIFACTS / "punch-timeline.png")
            window.scroll(down=False, steps=30)
            window.click(407, 304)
            window.shot(ARTIFACTS / "punch-ports.png")
            window.scroll(steps=8)
            window.shot(ARTIFACTS / "punch-port-matrix.png")
            window.resize(800, 1000)
            window.scroll(down=False, steps=30)
            window.scroll(steps=8)
            window.shot(ARTIFACTS / "punch-plans-portrait.png")
            window.resize(1240, 1040)
            window.scroll(down=False, steps=30)
            window.click(517, 304)
            window.shot(ARTIFACTS / "punch-packets.png")
            window.scroll(steps=11)
            window.shot(ARTIFACTS / "punch-packet-records.png")
            window.resize(800, 1000)
            window.navigate(1)
            window.shot(ARTIFACTS / "home-portrait.png")
            window.resize(1240, 820)
            window.navigate(1)
            # Shared sidebar run control remains reachable from every page.
            window.click(100, 686)
            time.sleep(0.5)
            assert calls(root).count("start") == 1, "run control did not start once"
            window.shot(ARTIFACTS / "running.png")
            window.navigate(3)
            window.shot(ARTIFACTS / "devices-running.png")
            window.click(590, 287)
            assert calls(root).count("renominate_transports") == 1, "reselect must use the path selection method"
            assert "network_changed" not in calls(root), "reselect invalidated the network"
            window.click(100, 686)
            assert calls(root).count("stop") == 1, "run control did not stop once"
        finally:
            shutil.copyfile(root / "gui.log", ARTIFACTS / "gui.log")
            shutil.copyfile(root / "rpc.jsonl", ARTIFACTS / "rpc.jsonl")
            window.close()
    print("PASS: Yororen UI themes, service form, portrait layout, start/stop and path selection")


if __name__ == "__main__":
    main()
