"""Desktop script regressions using local fixtures; never builds or downloads."""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
BASH = shutil.which("bash")


@unittest.skipUnless(sys.platform.startswith("linux") and BASH, "Linux desktop script fixtures")
class DesktopScriptsTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="hole desktop scripts ")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        for relative in (
            "scripts/build-env.sh", "desktop/scripts/toolchain.sh", "desktop/scripts/toolchain.py",
            "desktop/scripts/gradle.sh", "desktop/scripts/setup-appimage.sh",
        ):
            destination = self.root / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / relative, destination)
        self.env = {key: value for key, value in os.environ.items()
                    if not key.startswith(("HOLE_", "GO", "GRADLE_", "JAVA_", "JDK", "DESKTOP_TEST_"))}
        self.env.update(HOLE_ROOT=str(self.root), HOLE_CACHE_ROOT=str(self.root / ".cache"))
        self.toolchain = self.root / "desktop/scripts/toolchain.sh"

    def shell(self, body, **env):
        return subprocess.run([BASH, "-euc", 'source "$1"\n' + body, "fixture", str(self.toolchain)],
                              cwd=self.root, env={**self.env, **env}, capture_output=True, text=True)

    def script(self, name, *args, **env):
        return subprocess.run([BASH, str(self.root / "desktop/scripts" / name), *args],
                              cwd=self.root, env={**self.env, **env}, capture_output=True, text=True)

    def cached_tools(self, arch="amd64"):
        result = self.shell('hole_desktop_appimage_tools_dir "$DESKTOP_TEST_ARCH"', DESKTOP_TEST_ARCH=arch)
        self.assertEqual(0, result.returncode, result.stderr)
        directory = Path(result.stdout.strip())
        tool = directory / "squashfs-root/AppRun"
        tool.parent.mkdir(parents=True)
        tool.write_text("#!/bin/sh\nexit 0\n")
        tool.chmod(0o755)
        (directory / "runtime").write_bytes(b"local runtime fixture")
        return directory

    def test_cached_appimage_tools_work_without_path_or_environment_overrides(self):
        directory = self.cached_tools()
        result = self.shell('hole_load_desktop_appimage_tools amd64\nprintf "%s\\n" "$HOLE_APPIMAGETOOL" "$HOLE_APPIMAGE_RUNTIME"', PATH="")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual([str(directory / "squashfs-root/AppRun"), str(directory / "runtime")], result.stdout.splitlines())

    def test_explicit_tools_override_path_and_cache(self):
        directory = self.cached_tools()
        path_tool = directory / "squashfs-root/appimagetool"
        path_tool.write_text("#!/bin/sh\nexit 0\n")
        path_tool.chmod(0o755)
        explicit = self.root / "custom tool"
        explicit.write_text("#!/bin/sh\nexit 0\n")
        explicit.chmod(0o755)
        runtime = self.root / "custom runtime"
        runtime.write_bytes(b"explicit runtime")
        result = self.shell('hole_load_desktop_appimage_tools amd64\nprintf "%s\\n" "$HOLE_APPIMAGETOOL" "$HOLE_APPIMAGE_RUNTIME"',
                            HOLE_APPIMAGETOOL=str(explicit), HOLE_APPIMAGE_RUNTIME=str(runtime),
                            PATH=str(directory / "squashfs-root"))
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual([str(explicit), str(runtime)], result.stdout.splitlines())

    def test_invalid_explicit_tool_is_rejected_instead_of_using_cache(self):
        self.cached_tools()
        result = self.shell("hole_load_desktop_appimage_tools amd64", PATH="", HOLE_APPIMAGETOOL="/missing/tool")
        self.assertNotEqual(0, result.returncode)
        self.assertIn("HOLE_APPIMAGETOOL", result.stderr)

    def test_invalid_explicit_runtime_is_rejected_instead_of_using_cache(self):
        self.cached_tools()
        result = self.shell("hole_load_desktop_appimage_tools amd64", PATH="", HOLE_APPIMAGE_RUNTIME="/missing/runtime")
        self.assertNotEqual(0, result.returncode)
        self.assertIn("HOLE_APPIMAGE_RUNTIME", result.stderr)

    def test_cache_for_another_architecture_is_not_used(self):
        self.cached_tools()
        result = self.shell("hole_load_desktop_appimage_tools arm64", PATH="")
        self.assertNotEqual(0, result.returncode)
        self.assertIn("appimagetool", result.stderr)

    def test_missing_tools_explain_explicit_setup_command(self):
        result = self.shell("hole_load_desktop_appimage_tools amd64", PATH="")
        self.assertNotEqual(0, result.returncode)
        self.assertIn("bash desktop/scripts/setup-appimage.sh", result.stderr)

    def test_empty_runtime_is_rejected_before_packaging(self):
        directory = self.cached_tools()
        (directory / "runtime").write_bytes(b"")
        result = self.shell("hole_load_desktop_appimage_tools amd64", PATH="")
        self.assertNotEqual(0, result.returncode)
        self.assertIn("HOLE_APPIMAGE_RUNTIME", result.stderr)

    def test_setup_reuses_cache_in_offline_mode_without_jdk(self):
        self.cached_tools(arch=os.uname().machine)
        result = self.script("setup-appimage.sh", HOLE_BUILD_OFFLINE="1")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("已就绪", result.stdout)

    def test_offline_setup_with_missing_cache_fails_without_downloading(self):
        result = self.script("setup-appimage.sh", HOLE_BUILD_OFFLINE="1")
        self.assertNotEqual(0, result.returncode)
        self.assertIn("离线模式", result.stderr)

    def prepare_gradle(self):
        jdk = self.root / "jdk 25"
        (jdk / "bin").mkdir(parents=True)
        (jdk / "include").mkdir()
        (jdk / "jmods").mkdir()
        (jdk / "release").write_text('JAVA_VERSION="25.0.1"\n')
        (jdk / "include/jni.h").touch()
        (jdk / "jmods/java.base.jmod").touch()
        for name in ("java", "javac", "jlink", "jpackage"):
            tool = jdk / "bin" / name
            tool.write_text("#!/bin/sh\nexit 91\n")
            tool.chmod(0o755)
        wrapper = self.root / "android/gradlew"
        wrapper.parent.mkdir()
        wrapper.write_text('''#!/usr/bin/env bash
printf '%s\\0' "$@" > "$DESKTOP_TEST_ARGS"
printf '%s\\n' "$JAVA_HOME" "$GRADLE_USER_HOME" "$TMPDIR" > "$DESKTOP_TEST_ENV"
exit "${DESKTOP_TEST_EXIT:-0}"
''')
        self.env.update(HOLE_DESKTOP_JAVA_HOME=str(jdk), DESKTOP_TEST_ARGS=str(self.root / "args"),
                        DESKTOP_TEST_ENV=str(self.root / "env"))
        return jdk

    def test_gradle_entry_selects_jdk_and_project_caches_and_preserves_task_arguments(self):
        jdk = self.prepare_gradle()
        result = self.script("gradle.sh", "compileKotlin", "-Pfixture=has spaces", HOLE_BUILD_OFFLINE="1")
        self.assertEqual(0, result.returncode, result.stderr)
        arguments = (self.root / "args").read_bytes().decode().rstrip("\0").split("\0")
        self.assertIn("--offline", arguments)
        self.assertEqual(["compileKotlin", "-Pfixture=has spaces"], arguments[-2:])
        self.assertEqual(str(self.root / ".cache/gradle-project-desktop"), arguments[arguments.index("--project-cache-dir") + 1])
        self.assertEqual([str(jdk), str(self.root / ".cache/gradle"), str(self.root / ".cache/tmp")],
                         (self.root / "env").read_text().splitlines())

    def test_gradle_failure_exposes_jpackage_log_and_preserves_exit_status(self):
        self.prepare_gradle()
        log = self.root / "desktop/compose/build/compose/logs/packageReleaseExe/jpackage-err.txt"
        log.parent.mkdir(parents=True)
        log.write_text("packaging error fixture\n")
        result = self.script("gradle.sh", "packageReleaseExe", DESKTOP_TEST_EXIT="17")
        self.assertEqual(17, result.returncode)
        self.assertIn("packaging error fixture", result.stderr)


if __name__ == "__main__":
    unittest.main()
