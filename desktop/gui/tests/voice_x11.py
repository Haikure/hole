"""Exercise voice navigation and persisted toggles using a real desktop window.

Run under xvfb-run with a freshly built desktop binary. No network or audio
devices are started: changing a stopped configuration must remain stopped.
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

    def click(px, py):
        xt.XTestFakeMotionEvent(display, -1, px, py, 0)
        x.XFlush(display)
        xt.XTestFakeButtonEvent(display, 1, 1, 0)
        x.XFlush(display)
        time.sleep(0.05)
        xt.XTestFakeButtonEvent(display, 1, 0, 0)
        x.XFlush(display)
        time.sleep(0.4)

    try:
        for mode in ("auto", "legacy"):
            with tempfile.TemporaryDirectory(prefix="hole-voice-ui-", dir=os.environ.get("TMPDIR")) as directory:
                config_file = Path(directory) / "config.json"
                config_file.write_text(json.dumps({"connection": {"connection_mode": mode}}))
                env = dict(os.environ, HOLE_DESKTOP_CONFIG_DIR=directory, SLINT_BACKEND="winit-software")
                env.pop("HOLE_DESKTOP_SCREENSHOT_DIR", None)
                process = subprocess.Popen([str(Path(sys.argv[1]).resolve())], env=env)
                try:
                    time.sleep(2)
                    assert process.poll() is None, "GUI exited before interaction"
                    click(90, 237)  # Voice navigation.
                    click(1108, 154)

                    def enabled():
                        return json.loads(config_file.read_text()).get("voice", {}).get("enabled", False)

                    if mode == "legacy":
                        assert not enabled(), "Voice enabled on incompatible IPv6 transport"
                        continue
                    deadline = time.monotonic() + 3
                    while not enabled() and time.monotonic() < deadline:
                        time.sleep(0.1)
                    assert enabled(), "Voice switch was not persisted"
                    click(90, 358)  # Leave and revisit the page.
                    click(90, 237)
                    click(1108, 154)
                    assert not enabled(), "Revisited voice switch did not reflect saved state"
                    click(1108, 154)
                    assert enabled(), "Repeated enable failed"
                    state_file = Path(directory) / "run-state.json"
                    if state_file.exists():
                        assert not json.loads(state_file.read_text()).get("run_requested", False)
                except Exception:
                    try:
                        from PIL import ImageGrab
                        ImageGrab.grab().save(Path(os.environ.get("TMPDIR", "/tmp")) / "hole-voice-ui-failure.png")
                    except ImportError:
                        pass
                    raise
                finally:
                    process.terminate()
                    process.wait(timeout=5)
        print("PASS: voice navigation, persisted toggles, stopped intent, and IPv6 rejection")
    finally:
        x.XCloseDisplay(display)


if __name__ == "__main__":
    main()
