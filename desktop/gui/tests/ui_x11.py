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


def check_motion(binary, reduced):
    """Check actual rendered frames, including GPUI's reduce-motion behavior."""
    from PIL import ImageChops, ImageGrab

    with tempfile.TemporaryDirectory(prefix="hole-motion-") as directory:
        root = Path(directory)
        (root / "config.json").write_text(json.dumps({"prefs": {"theme_mode": "light", "reduce_motion": reduced}}))
        fixture = root / "core-fixture"
        shutil.copyfile(Path(__file__).with_name("core_fixture.py"), fixture)
        fixture.chmod(0o700)
        window = Window(binary, root, {"HOLE_CORE": str(fixture)})
        try:
            window.move(1000, 50)
            time.sleep(0.4)
            prefix = "reduced" if reduced else "animated"
            before = window.shot(ARTIFACTS / f"{prefix}-before.png")
            region = (1150, 10, 1208, 64)
            x, y, _, _ = window.geometry()
            window.move(1176, 37)
            # X11 software rendering can deliver the first pointer frame late.
            # Compare the first changed frame, not an arbitrary wall-clock delay.
            deadline = time.monotonic() + 3
            while True:
                middle = ImageGrab.grab().crop((x, y, x + 1240, y + 820))
                if ImageChops.difference(before.crop(region), middle.crop(region)).getbbox():
                    break
                assert time.monotonic() < deadline, "hover gave no visual feedback"
                time.sleep(0.01)
            middle.save(ARTIFACTS / f"{prefix}-middle.png")
            time.sleep(0.4)
            after = window.shot(ARTIFACTS / f"{prefix}-after.png")
            assert ImageChops.difference(before.crop(region), after.crop(region)).getbbox(), "hover gave no visual feedback"
            # Keep intermediate frames for visual review. Frame pacing depends on
            # the Vulkan driver; spring and reduced-motion timing have unit coverage.
            assert "start" not in calls(root), "motion check started the network"
        finally:
            window.close()


def main():
    from PIL import ImageChops, ImageStat

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
            window.click(850, 98)
            window.shot(ARTIFACTS / "appearance-light.png")
            window.click(550, 227)
            window.click(518, 322)
            window.shot(ARTIFACTS / "appearance-dark.png")
            window.click(1140, 496)
            assert stored(root)["prefs"]["theme_mode"] == "dark"
            assert stored(root)["prefs"]["accent"] == "violet"
            assert "apply_config" not in calls(root), "theme save changed network configuration"
            time.sleep(5.2)
            window.navigate(1)
            window.shot(ARTIFACTS / "home-dark.png")
            window.navigate(5)
            window.click(443, 227)
            window.click(300, 322)
            window.shot(ARTIFACTS / "appearance-restored.png")
            window.click(1140, 496)
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
            window.click(850, 498)
            time.sleep(2)
            window.shot(ARTIFACTS / "punch-process.png")
            assert "nat_punch_process" in calls(root), "process page did not read core records"
            window.resize(1240, 1040)
            window.click(330, 905)
            time.sleep(0.7)
            paused_reads = calls(root).count("nat_punch_process")
            time.sleep(0.7)
            assert calls(root).count("nat_punch_process") == paused_reads, "paused browsing kept polling"
            window.click(482, 905)
            window.shot(ARTIFACTS / "punch-paused.png")
            window.click(330, 905)
            time.sleep(0.7)
            assert calls(root).count("nat_punch_process") > paused_reads, "live browsing did not resume polling"
            window.scroll(steps=30)
            window.shot(ARTIFACTS / "punch-timeline.png")
            window.click(1170, 474)
            time.sleep(0.7)
            window.shot(ARTIFACTS / "punch-stage-expanded.png")
            window.click(1170, 474)
            window.scroll(down=False, steps=30)
            window.click(730, 1018)
            window.shot(ARTIFACTS / "punch-ports.png")
            window.scroll(steps=18)
            port_list = window.shot(ARTIFACTS / "punch-port-matrix.png")
            window.click(650, 780)
            time.sleep(0.7)
            plan = window.shot(ARTIFACTS / "punch-plan-detail.png")
            dimmed = (20, 100, 190, 650)
            assert sum(ImageStat.Stat(ImageChops.difference(port_list.crop(dimmed), plan.crop(dimmed))).mean) > 20, "plan detail did not open"
            window.key("Escape", delay=0.7)
            window.resize(800, 1000)
            window.scroll(down=False, steps=30)
            window.scroll(steps=8)
            window.shot(ARTIFACTS / "punch-ports-portrait.png")
            window.resize(1240, 1040)
            window.scroll(down=False, steps=30)
            window.click(1050, 1018)
            window.shot(ARTIFACTS / "punch-packets.png")
            window.scroll(steps=11)
            packet_list = window.shot(ARTIFACTS / "punch-packet-records.png")
            window.click(480, 826)
            time.sleep(0.7)
            detail = window.shot(ARTIFACTS / "punch-packet-detail.png")
            dimmed = (20, 100, 190, 650)
            assert sum(ImageStat.Stat(ImageChops.difference(packet_list.crop(dimmed), detail.crop(dimmed))).mean) > 20, "packet detail did not open"
            window.key("Escape", delay=0.7)
            closed = window.shot(ARTIFACTS / "punch-detail-closed.png")
            assert sum(ImageStat.Stat(ImageChops.difference(packet_list.crop(dimmed), closed.crop(dimmed))).mean) < 3, "packet detail did not close"
            window.click(455, 225)
            window.click(480, 860)
            time.sleep(0.7)
            window.shot(ARTIFACTS / "punch-stun-detail.png")
            window.click(970, 563)
            time.sleep(0.7)
            transaction = window.shot(ARTIFACTS / "punch-transaction-filter.png")
            assert sum(ImageStat.Stat(transaction.crop((760, 620, 1190, 890))).stddev) < 6, "transaction filter retained records with a different ID"
            window.resize(800, 1000)
            window.navigate(1)
            window.shot(ARTIFACTS / "home-portrait.png")
            window.resize(1240, 820)
            window.navigate(1)
            # Shared sidebar run control remains reachable from every page.
            window.click(100, 700)
            time.sleep(0.5)
            assert calls(root).count("start") == 1, "run control did not start once"
            window.shot(ARTIFACTS / "running.png")
            window.navigate(3)
            window.shot(ARTIFACTS / "devices-running.png")
            window.click(590, 440)
            assert calls(root).count("renominate_transports") == 1, "reselect must use the path selection method"
            assert "network_changed" not in calls(root), "reselect invalidated the network"
            window.click(100, 700)
            assert calls(root).count("stop") == 1, "run control did not stop once"
        finally:
            shutil.copyfile(root / "gui.log", ARTIFACTS / "gui.log")
            shutil.copyfile(root / "rpc.jsonl", ARTIFACTS / "rpc.jsonl")
            window.close()
    check_motion(sys.argv[1], False)
    check_motion(sys.argv[1], True)
    print("PASS: Yororen UI themes, service form, process replay, packet detail, motion, portrait layout, start/stop and path selection")


if __name__ == "__main__":
    main()
