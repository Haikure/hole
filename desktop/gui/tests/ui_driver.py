"""Small real-window X11 driver shared by desktop interaction tests."""
import ctypes as C
import os
from pathlib import Path
import subprocess
import time


PAGES = {"overview", "mappings", "devices", "runtime", "voice", "diagnostics",
         "settings", "transport", "appearance", "behavior", "exchange"}


class Driver:
    def __init__(self, binary, directory):
        self.root = Path(directory)
        self.x = C.CDLL("libX11.so.6")
        self.xt = C.CDLL("libXtst.so.6")
        for name, args, result in [
            ("XOpenDisplay", [C.c_char_p], C.c_void_p),
            ("XFlush", [C.c_void_p], C.c_int),
            ("XCloseDisplay", [C.c_void_p], C.c_int),
            ("XKeysymToKeycode", [C.c_void_p, C.c_ulong], C.c_uint),
            ("XStringToKeysym", [C.c_char_p], C.c_ulong),
            ("XDefaultRootWindow", [C.c_void_p], C.c_ulong),
            ("XSetInputFocus", [C.c_void_p, C.c_ulong, C.c_int, C.c_ulong], C.c_int),
            ("XRaiseWindow", [C.c_void_p, C.c_ulong], C.c_int),
            ("XMapRaised", [C.c_void_p, C.c_ulong], C.c_int),
            ("XFetchName", [C.c_void_p, C.c_ulong, C.POINTER(C.c_void_p)], C.c_int),
            ("XFree", [C.c_void_p], C.c_int),
        ]:
            fn = getattr(self.x, name)
            fn.argtypes, fn.restype = args, result
        self.xt.XTestFakeMotionEvent.argtypes = [C.c_void_p, C.c_int, C.c_int, C.c_int, C.c_ulong]
        self.xt.XTestFakeButtonEvent.argtypes = [C.c_void_p, C.c_uint, C.c_int, C.c_ulong]
        self.xt.XTestFakeKeyEvent.argtypes = [C.c_void_p, C.c_uint, C.c_int, C.c_ulong]
        self.x.XQueryTree.argtypes = [C.c_void_p, C.c_ulong, C.POINTER(C.c_ulong), C.POINTER(C.c_ulong), C.POINTER(C.POINTER(C.c_ulong)), C.POINTER(C.c_uint)]
        self.display = self.x.XOpenDisplay(None)
        assert self.display, "Run inside xvfb-run"
        env = dict(os.environ, HOLE_DESKTOP_CONFIG_DIR=str(self.root), SLINT_BACKEND="winit-software")
        for name in ("HOLE_DESKTOP_SCREENSHOT_DIR", "HOLE_DESKTOP_SCREENSHOT_SCENARIO",
                     "HOLE_DESKTOP_SCREENSHOT_PAGES"):
            env.pop(name, None)
        self.log = (self.root / "gui.log").open("w")
        self.process = subprocess.Popen([str(Path(binary).resolve())], env=env, stderr=self.log)
        deadline = time.monotonic() + 15
        try:
            while True:
                assert self.process.poll() is None, "GUI exited before interaction"
                try:
                    self.focus_window()
                    # The native window can be mapped before Slint finishes
                    # binding its first frame and keyboard callbacks.
                    time.sleep(2)
                    break
                except AssertionError:
                    if time.monotonic() >= deadline:
                        raise
                    time.sleep(0.1)
        except BaseException:
            self.close()
            raise

    def focus_window(self):
        pending = [self.x.XDefaultRootWindow(self.display)]
        while pending:
            window = pending.pop()
            name = C.c_void_p()
            if self.x.XFetchName(self.display, window, C.byref(name)) and name:
                title = C.string_at(name).decode(errors="replace")
                self.x.XFree(name)
                if title == "hole":
                    self.x.XMapRaised(self.display, window)
                    self.x.XSetInputFocus(self.display, window, 1, 0)
                    self.x.XFlush(self.display)
                    time.sleep(0.1)
                    return
            root, parent, count = C.c_ulong(), C.c_ulong(), C.c_uint()
            children = C.POINTER(C.c_ulong)()
            if self.x.XQueryTree(self.display, window, C.byref(root), C.byref(parent), C.byref(children), C.byref(count)):
                pending.extend(children[i] for i in range(count.value))
                if children:
                    self.x.XFree(children)
        raise AssertionError("hole window was not found")

    def move(self, px, py):
        self.xt.XTestFakeMotionEvent(self.display, -1, px, py, 0)
        self.x.XFlush(self.display)

    def button(self, down):
        self.xt.XTestFakeButtonEvent(self.display, 1, down, 0)
        self.x.XFlush(self.display)

    def click(self, px, py):
        self.move(px, py)
        self.button(1)
        time.sleep(0.04)
        self.button(0)
        time.sleep(0.3)

    def drag(self, start, end):
        self.move(*start)
        self.button(1)
        for step in range(1, 21):
            self.move(*(round(a + (b - a) * step / 20) for a, b in zip(start, end)))
            time.sleep(0.02)
        self.button(0)
        time.sleep(0.3)

    def _key(self, name, down):
        symbol = ord(name) if len(name) == 1 else self.x.XStringToKeysym(name.encode())
        code = self.x.XKeysymToKeycode(self.display, symbol)
        assert code, f"Unknown key {name}"
        self.xt.XTestFakeKeyEvent(self.display, code, down, 0)
        self.x.XFlush(self.display)

    def key(self, name, *modifiers, delay=0.15):
        for modifier in modifiers:
            self._key(modifier, 1)
        self._key(name, 1)
        self._key(name, 0)
        for modifier in reversed(modifiers):
            self._key(modifier, 0)
        time.sleep(delay)

    def type(self, text):
        for character in text:
            shifted = character.isupper() or character in '~!@#$%^&*()_+{}|:"<>?'
            self.key("space" if character == " " else character,
                     *(["Shift_L"] if shifted else []), delay=0.015)

    def navigate(self, index):
        self.key(str(index), "Control_L", delay=0.5)

    def scroll(self, down=True, steps=5, px=1060, py=600):
        self.move(px, py)
        for _ in range(steps):
            self.xt.XTestFakeButtonEvent(self.display, 5 if down else 4, 1, 0)
            self.xt.XTestFakeButtonEvent(self.display, 5 if down else 4, 0, 0)
        self.x.XFlush(self.display)
        time.sleep(0.3)

    def shot(self, name):
        from PIL import ImageGrab
        artifacts = Path(os.environ.get("HOLE_ROOT", Path(__file__).resolve().parents[3])) / ".cache" / "desktop-ui" / "interactions"
        artifacts.mkdir(parents=True, exist_ok=True)
        ImageGrab.grab().save(artifacts / name)

    def close(self):
        self.process.terminate()
        self.process.wait(timeout=5)
        self.log.close()
        self.x.XCloseDisplay(self.display)
