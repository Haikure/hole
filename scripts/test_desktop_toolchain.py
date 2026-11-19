"""JDK selection regression tests; uses files only, never runs Java or builds."""
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("desktop_toolchain", ROOT / "desktop/scripts/toolchain.py")
TOOLCHAIN = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(TOOLCHAIN)


class DesktopToolchainTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def jdk(self, version, name=None):
        path = self.root / (name or f"jdk-{version}")
        (path / "bin").mkdir(parents=True)
        (path / "include").mkdir()
        (path / "release").write_text(f'JAVA_VERSION="{version}.0.1"\n', encoding="utf-8")
        (path / "include/jni.h").touch()
        suffix = ".exe" if os.name == "nt" else ""
        for tool in ("java", "javac", "jlink", "jpackage"):
            binary = path / "bin" / (tool + suffix)
            binary.touch()
            binary.chmod(0o755)
        return path.resolve()

    def test_uses_existing_jdk25_without_jdk21(self):
        jdk25 = self.jdk(25)
        self.assertEqual(jdk25, TOOLCHAIN.select_jdk({"JAVA_HOME": str(jdk25)}, []))

    def test_android_jdk17_does_not_hide_system_jdk25(self):
        jdk17, jdk25 = self.jdk(17), self.jdk(25)
        self.assertEqual(jdk25, TOOLCHAIN.select_jdk({"JAVA_HOME": str(jdk17)}, [jdk17, jdk25]))

    def test_desktop_override_wins_and_invalid_override_does_not_fall_back(self):
        jdk21, jdk25 = self.jdk(21), self.jdk(25)
        self.assertEqual(jdk25, TOOLCHAIN.select_jdk({"JAVA_HOME": str(jdk21), "HOLE_DESKTOP_JAVA_HOME": str(jdk25)}, []))
        with self.assertRaisesRegex(ValueError, "HOLE_DESKTOP_JAVA_HOME"):
            TOOLCHAIN.select_jdk({"HOLE_DESKTOP_JAVA_HOME": str(self.root / "missing")}, [jdk25])

    def test_current_compatible_java_home_is_preserved(self):
        jdk21, jdk25 = self.jdk(21), self.jdk(25)
        self.assertEqual(jdk21, TOOLCHAIN.select_jdk({"JAVA_HOME": str(jdk21)}, [jdk25]))

    def test_selects_newest_complete_discovered_jdk(self):
        jdk21, jdk25 = self.jdk(21), self.jdk(25)
        incomplete = self.jdk(26)
        (incomplete / "include/jni.h").unlink()
        self.assertEqual(jdk25, TOOLCHAIN.select_jdk({}, [jdk21, incomplete, jdk25]))

    def test_missing_packaging_tool_is_not_a_complete_jdk(self):
        path = self.jdk(25)
        (path / "bin" / ("jpackage.exe" if os.name == "nt" else "jpackage")).unlink()
        with self.assertRaisesRegex(ValueError, "未找到完整桌面 JDK"):
            TOOLCHAIN.select_jdk({"JAVA_HOME": str(path)}, [path])


if __name__ == "__main__":
    unittest.main()
