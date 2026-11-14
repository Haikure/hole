"""Real X11 window driver for Yororen UI. All coordinates are window relative."""
import ctypes as C
import os
from pathlib import Path
import subprocess
import time


class Window:
    def __init__(self, binary, directory, overrides=None):
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
            ("XMapRaised", [C.c_void_p, C.c_ulong], C.c_int),
            ("XFetchName", [C.c_void_p, C.c_ulong, C.POINTER(C.c_void_p)], C.c_int),
            ("XFree", [C.c_void_p], C.c_int),
            ("XResizeWindow", [C.c_void_p, C.c_ulong, C.c_uint, C.c_uint], C.c_int),
            ("XMoveWindow", [C.c_void_p, C.c_ulong, C.c_int, C.c_int], C.c_int),
            ("XTranslateCoordinates", [C.c_void_p, C.c_ulong, C.c_ulong, C.c_int, C.c_int, C.POINTER(C.c_int), C.POINTER(C.c_int), C.POINTER(C.c_ulong)], C.c_int),
            ("XGetGeometry", [C.c_void_p, C.c_ulong, C.POINTER(C.c_ulong), C.POINTER(C.c_int), C.POINTER(C.c_int), C.POINTER(C.c_uint), C.POINTER(C.c_uint), C.POINTER(C.c_uint), C.POINTER(C.c_uint)], C.c_int),
        ]:
            fn = getattr(self.x, name)
            fn.argtypes, fn.restype = args, result
        self.x.XQueryTree.argtypes = [C.c_void_p, C.c_ulong, C.POINTER(C.c_ulong), C.POINTER(C.c_ulong), C.POINTER(C.POINTER(C.c_ulong)), C.POINTER(C.c_uint)]
        self.xt.XTestFakeMotionEvent.argtypes = [C.c_void_p, C.c_int, C.c_int, C.c_int, C.c_ulong]
        self.xt.XTestFakeButtonEvent.argtypes = [C.c_void_p, C.c_uint, C.c_int, C.c_ulong]
        self.xt.XTestFakeKeyEvent.argtypes = [C.c_void_p, C.c_uint, C.c_int, C.c_ulong]
        self.display = self.x.XOpenDisplay(None)
        assert self.display, "Run inside xvfb-run"
        self.log = (self.root / "gui.log").open("w")
        env = dict(os.environ, HOLE_DESKTOP_CONFIG_DIR=str(self.root), **(overrides or {}))
        env.pop("WAYLAND_DISPLAY", None)
        self.process = subprocess.Popen([str(Path(binary).resolve())], env=env, stdout=self.log, stderr=self.log)
        self.window = None
        deadline = time.monotonic() + 30
        try:
            while not self.window:
                assert self.process.poll() is None, (self.root / "gui.log").read_text()
                self.window = self.find()
                if time.monotonic() > deadline:
                    raise AssertionError("Hole window did not appear")
                time.sleep(0.1)
            self.x.XMapRaised(self.display, self.window)
            self.x.XSetInputFocus(self.display, self.window, 1, 0)
            self.x.XFlush(self.display)
            time.sleep(2)
        except BaseException:
            self.close()
            raise

    def find(self):
        pending = [self.x.XDefaultRootWindow(self.display)]
        while pending:
            window = pending.pop()
            name = C.c_void_p()
            if self.x.XFetchName(self.display, window, C.byref(name)) and name:
                title = C.string_at(name).decode(errors="replace")
                self.x.XFree(name)
                if title.startswith("Hole"):
                    return window
            root, parent, count = C.c_ulong(), C.c_ulong(), C.c_uint()
            children = C.POINTER(C.c_ulong)()
            if self.x.XQueryTree(self.display, window, C.byref(root), C.byref(parent), C.byref(children), C.byref(count)):
                pending.extend(children[i] for i in range(count.value))
                if children:
                    self.x.XFree(children)

    def geometry(self):
        root, child = C.c_ulong(), C.c_ulong()
        x, y, rx, ry = C.c_int(), C.c_int(), C.c_int(), C.c_int()
        width, height, border, depth = C.c_uint(), C.c_uint(), C.c_uint(), C.c_uint()
        self.x.XGetGeometry(self.display, self.window, C.byref(root), C.byref(x), C.byref(y), C.byref(width), C.byref(height), C.byref(border), C.byref(depth))
        self.x.XTranslateCoordinates(self.display, self.window, self.x.XDefaultRootWindow(self.display), 0, 0, C.byref(rx), C.byref(ry), C.byref(child))
        return rx.value, ry.value, width.value, height.value

    def move(self, x, y):
        rx, ry, _, _ = self.geometry()
        self.xt.XTestFakeMotionEvent(self.display, -1, rx + x, ry + y, 0)
        self.x.XFlush(self.display)

    def click(self, x, y):
        self.move(x, y)
        for down in (1, 0):
            self.xt.XTestFakeButtonEvent(self.display, 1, down, 0)
            self.x.XFlush(self.display)
            time.sleep(0.05)
        time.sleep(0.35)

    def _key(self, name, down):
        symbol = ord(name) if len(name) == 1 else self.x.XStringToKeysym(name.encode())
        code = self.x.XKeysymToKeycode(self.display, symbol)
        assert code, name
        self.xt.XTestFakeKeyEvent(self.display, code, down, 0)
        self.x.XFlush(self.display)

    def key(self, name, *modifiers, delay=0.2):
        for modifier in modifiers:
            self._key(modifier, 1)
        self._key(name, 1)
        self._key(name, 0)
        for modifier in reversed(modifiers):
            self._key(modifier, 0)
        time.sleep(delay)

    def type(self, text):
        for char in text:
            self.key("space" if char == " " else char, *(["Shift_L"] if char.isupper() else []), delay=0.02)

    def navigate(self, index):
        self.key(str(index), "Control_L", delay=0.5)

    def resize(self, width, height):
        self.x.XMoveWindow(self.display, self.window, 0, 0)
        self.x.XResizeWindow(self.display, self.window, width, height)
        self.x.XFlush(self.display)
        time.sleep(0.7)

    def scroll(self, down=True, steps=6):
        _, _, width, height = self.geometry()
        self.move(width - 60, height - 100)
        time.sleep(0.08)
        for _ in range(steps):
            for pressed in (1, 0):
                self.xt.XTestFakeButtonEvent(self.display, 5 if down else 4, pressed, 0)
            self.x.XFlush(self.display)
            time.sleep(0.01)
        self.x.XFlush(self.display)
        time.sleep(0.4)

    def shot(self, path):
        from PIL import ImageGrab
        assert self.process.poll() is None, (self.root / "gui.log").read_text()
        x, y, width, height = self.geometry()
        deadline = time.monotonic() + 15
        while True:
            image = ImageGrab.grab().crop((x, y, x + width, y + height))
            image.save(path)
            low, high = image.convert("L").getextrema()
            if high - low > 64 and len(image.getcolors(width * height) or []) > 100:
                break
            assert time.monotonic() < deadline, f"window did not render: {self.geometry()}"
            time.sleep(0.5)
        return image

    def close(self):
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        self.log.close()
        self.x.XCloseDisplay(self.display)
