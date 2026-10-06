"""Linux/Xvfb smoke test: drag TURN chips and verify the saved order.

Run with project caches loaded:
xvfb-run -a python3 desktop/gui/tests/turn_order_x11.py .cache/cargo-target/debug/hole-desktop
"""
import ctypes as C
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time


def main():
    x = C.CDLL("libX11.so.6")
    xt = C.CDLL("libXtst.so.6")
    x.XOpenDisplay.argtypes = [C.c_char_p]
    x.XOpenDisplay.restype = C.c_void_p
    x.XFlush.argtypes = [C.c_void_p]
    x.XCloseDisplay.argtypes = [C.c_void_p]
    xt.XTestFakeMotionEvent.argtypes = [C.c_void_p, C.c_int, C.c_int, C.c_int, C.c_ulong]
    xt.XTestFakeButtonEvent.argtypes = [C.c_void_p, C.c_uint, C.c_int, C.c_ulong]
    display = x.XOpenDisplay(None)
    assert display, "Run inside xvfb-run"

    def move(px, py):
        xt.XTestFakeMotionEvent(display, -1, px, py, 0)
        x.XFlush(display)

    def button(down):
        xt.XTestFakeButtonEvent(display, 1, down, 0)
        x.XFlush(display)

    def click(px, py):
        move(px, py)
        button(1)
        time.sleep(0.05)
        button(0)
        time.sleep(0.3)

    def drag(start, end):
        move(*start)
        button(1)
        for step in range(1, 21):
            move(*(round(a + (b - a) * step / 20) for a, b in zip(start, end)))
            time.sleep(0.02)
        button(0)
        time.sleep(0.3)

    with tempfile.TemporaryDirectory(prefix="hole-turn-ui-", dir=os.environ.get("TMPDIR")) as directory:
        config_file = Path(directory) / "config.json"
        order = ["udp", "tcp", "tls"]
        config_file.write_text(json.dumps({"connection": {"turn": {"order": order}}}))
        env = dict(os.environ, HOLE_DESKTOP_CONFIG_DIR=directory, SLINT_BACKEND="winit-software")
        env.pop("HOLE_DESKTOP_SCREENSHOT_DIR", None)
        process = subprocess.Popen([str(Path(sys.argv[1]).resolve())], env=env)
        try:
            time.sleep(2)
            assert process.poll() is None, "GUI exited before interaction"
            click(90, 401)
            time.sleep(0.5)

            def save_and_check(expected):
                click(1080, 724)
                deadline = time.monotonic() + 3
                while time.monotonic() < deadline:
                    actual = json.loads(config_file.read_text())["connection"]["turn"]["order"]
                    if actual == expected:
                        return
                    time.sleep(0.1)
                raise AssertionError(f"Expected {expected}, got {actual}")

            drag((570, 464), (830, 464))
            save_and_check(["tcp", "tls", "udp"])
            drag((810, 464), (530, 464))
            save_and_check(["udp", "tcp", "tls"])
            print("PASS: reorder forward/backward, save, and no accidental click removal")
        finally:
            process.terminate()
            process.wait(timeout=5)
            x.XCloseDisplay(display)


if __name__ == "__main__":
    main()
