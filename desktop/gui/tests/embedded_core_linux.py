"""Run only a copied GUI, prove its child executes memfd, and check shutdown/EOF.

source scripts/build-env.sh
xvfb-run -a python3 desktop/gui/tests/embedded_core_linux.py dist/desktop/linux-amd64/hole-desktop
"""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time


def live(pid):
    try:
        state = (Path("/proc") / str(pid) / "status").read_text()
        return not any(line.startswith("State:") and "Z" in line for line in state.splitlines())
    except FileNotFoundError:
        return False


def wait_for_core(gui):
    tasks = Path(f"/proc/{gui.pid}/task")
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        assert gui.poll() is None, "GUI exited before starting its embedded core"
        children = set()
        # Linux children are attributed to the spawning thread; CoreClient
        # launches the core from its controller thread, not the UI thread.
        for task in tasks.iterdir():
            try:
                children.update((task / "children").read_text().split())
            except FileNotFoundError:
                pass
        for pid in children:
            try:
                image = os.readlink(f"/proc/{pid}/exe")
            except FileNotFoundError:
                continue
            if image.startswith("/memfd:hole-desktop-core"):
                return int(pid)
            raise AssertionError(f"GUI launched a disk executable instead of memfd: {image}")
        time.sleep(0.05)
    raise AssertionError("GUI did not launch its embedded core")


def main():
    source = Path(sys.argv[1]).resolve()
    for crash in (False, True):
        with tempfile.TemporaryDirectory(prefix="hole-single-file-", dir=os.environ.get("TMPDIR")) as directory:
            root = Path(directory)
            binary = root / "hole-desktop"
            shutil.copy2(source, binary)
            config = root / "config"
            config.mkdir()
            shots = root / "shots"
            shots.mkdir()
            env = dict(os.environ, SLINT_BACKEND="winit-software", HOLE_DESKTOP_CONFIG_DIR=str(config), HOLE_DESKTOP_SCREENSHOT_DIR=str(shots))
            env.pop("HOLE_DESKTOP_CORE", None)
            env.pop("HOLE_EMBED_CORE", None)
            env.pop("CARGO_MANIFEST_DIR", None)
            process = subprocess.Popen([str(binary)], cwd=root, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            core_pid = None
            try:
                core_pid = wait_for_core(process)
                assert not list(root.rglob("*core*.exe"))
                assert not list(root.rglob("hole-desktop-core*"))
                if crash:
                    process.kill()
                _, error = process.communicate(timeout=30)
                if not crash:
                    assert process.returncode == 0, error.decode(errors="replace")
                    assert len(list(shots.glob("*.ppm"))) == 8, "GUI screenshot cycle did not complete"
                deadline = time.monotonic() + 5
                while live(core_pid) and time.monotonic() < deadline:
                    time.sleep(0.05)
                assert not live(core_pid), "Core outlived GUI shutdown / pipe EOF"
            finally:
                if process.poll() is None:
                    process.kill()
                    process.communicate(timeout=5)
                if core_pid and live(core_pid):
                    os.kill(core_pid, 15)
    print("PASS: copied GUI only, memfd execution, eight pages, graceful shutdown, and crash EOF cleanup")


if __name__ == "__main__":
    main()
