"""Run only a copied GUI, prove its child executes memfd, and check shutdown/EOF.

source scripts/build-env.sh
xvfb-run -a python3 desktop/gui/tests/embedded_core_linux.py dist/desktop/linux-amd64/hole-desktop
"""
import os
from pathlib import Path
import shutil
import sys
import tempfile
import time

from window_x11 import Window


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
            if image.startswith("/memfd:hole-core"):
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
            old_core = os.environ.pop("HOLE_CORE", None)
            try:
                window = Window(binary, config)
            finally:
                if old_core is not None:
                    os.environ["HOLE_CORE"] = old_core
            process = window.process
            core_pid = None
            try:
                core_pid = wait_for_core(process)
                assert not list(root.rglob("*core*.exe"))
                assert not list(root.rglob("hole-core*"))
                if crash:
                    process.kill()
                else:
                    # WM_DELETE is intentionally confirmed in the real window.
                    window.key("F4", "Alt_L")
                    # Xvfb without a window manager does not bind Alt+F4.
                    # Send the protocol message directly instead.
                    send_close(window)
                    _, _, width, height = window.geometry()
                    window.click(width // 2 + 110, height // 2 + 55)
                process.wait(timeout=30)
                if not crash:
                    assert process.returncode == 0, (config / "gui.log").read_text()
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
                window.close()
    print("PASS: copied GUI only, memfd execution, graceful shutdown, and crash EOF cleanup")


def send_close(window):
    import ctypes as C
    class Client(C.Structure):
        _fields_ = [("type", C.c_int), ("serial", C.c_ulong), ("send_event", C.c_int), ("display", C.c_void_p),
                    ("window", C.c_ulong), ("message_type", C.c_ulong), ("format", C.c_int), ("data", C.c_long * 5)]
    class Event(C.Union):
        _fields_ = [("client", Client), ("padding", C.c_long * 24)]
    window.x.XInternAtom.argtypes = [C.c_void_p, C.c_char_p, C.c_int]
    window.x.XInternAtom.restype = C.c_ulong
    window.x.XSendEvent.argtypes = [C.c_void_p, C.c_ulong, C.c_int, C.c_long, C.POINTER(Event)]
    event = Event()
    event.client.type = 33
    event.client.display = window.display
    event.client.window = window.window
    event.client.message_type = window.x.XInternAtom(window.display, b"WM_PROTOCOLS", 0)
    event.client.format = 32
    event.client.data[0] = window.x.XInternAtom(window.display, b"WM_DELETE_WINDOW", 0)
    window.x.XSendEvent(window.display, window.window, 0, 0, C.byref(event))
    window.x.XFlush(window.display)
    time.sleep(0.5)


if __name__ == "__main__":
    main()
