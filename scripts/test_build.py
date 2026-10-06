"""Offline black-box tests of build.sh; compilers are deterministic fixtures."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]

TOOL = r'''#!/usr/bin/env python3
import json, os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
keys = ("GOCACHE", "GOPATH", "GOMODCACHE", "GRADLE_USER_HOME", "CARGO_HOME", "TMPDIR", "GOPROXY", "GOOS", "GOARCH", "CGO_ENABLED", "HOLE_EMBED_CORE", "HOLE_SIGNING_KEY_ALIAS", "HOLE_SIGNING_STORE_FILE")
with open(os.environ["BUILD_TEST_LOG"], "a") as log:
    log.write(json.dumps({"tool": name, "args": args, "env": {key: os.environ.get(key) for key in keys}}) + "\n")
if name == "go":
    if args[:1] == ["env"]:
        values = {"GOVERSION": "go1.26.4", "GOHOSTOS": "linux", "GOHOSTARCH": "amd64"}
        print("\n".join(values[key] for key in args[1:]))
    elif args == ["tool", "dist", "list"]:
        print("linux/amd64\nlinux/arm64\nwindows/amd64\ndarwin/arm64")
    elif args[:1] == ["build"]:
        if os.environ.get("FAIL_GO"): sys.exit(9)
        path = pathlib.Path(args[args.index("-o") + 1])
        path.write_text("stripped-cli-fixture")
        path.chmod(0o755)
elif name == "javac":
    print("javac 17.0.0")
elif name == "cargo":
    if os.environ.get("FAIL_CARGO"): sys.exit(10)
    if args[:1] == ["build"]:
        assert "embedded-core" in args, "release GUI must embed its core"
        core = pathlib.Path(os.environ["HOLE_EMBED_CORE"])
        assert core.is_absolute() and core.read_text() == "stripped-cli-fixture"
        target = args[args.index("--target") + 1] if "--target" in args else None
        output = pathlib.Path.cwd().parents[1] / ".cache/cargo-target"
        if target: output /= target
        binary = output / "release" / ("hole-desktop.exe" if target and "windows" in target else "hole-desktop")
        binary.parent.mkdir(parents=True, exist_ok=True)
        binary.write_text("release-gui-fixture")
        binary.chmod(0o755)
elif name == "xmake":
    if os.environ.get("FAIL_XMAKE"): sys.exit(7)
    if args == ["build", "hole_plugin"]:
        path = pathlib.Path("build/linux/arm64-v8a/release/libhole_plugin.so")
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("release-plugin-fixture")
elif name == "gradlew":
    if os.environ.get("FAIL_GRADLE"): sys.exit(8)
    for module in ("app", "wear"):
        if f":{module}:assembleRelease" in args:
            path = pathlib.Path(f"{module}/build/outputs/apk/release/{module}-release.apk")
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("signed-release-fixture")
'''

VERIFY = r'''
import json, os, sys
with open(os.environ["BUILD_TEST_LOG"], "a") as log:
    log.write(json.dumps({"tool": "verify", "args": sys.argv[1:], "env": {}}) + "\n")
'''


class BuildTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="hole build test ")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / "source tree"
        self.root.mkdir()
        for relative in ("build.sh", "scripts/build-env.sh", "scripts/build_meta.py"):
            target = self.root / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / relative, target)
        for relative in ("go.mod", "go.sum", "android/corebridge/gobuild/go.mod", "android/corebridge/gobuild/go.sum", "core/compat/anet/go.mod", "core/core.go", "mobile/mobile.go"):
            target = self.root / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text("fixture\n")
        binary_dir = self.root / "tools"
        binary_dir.mkdir()
        for name in ("go", "java", "javac", "keytool", "gomobile", "gobind", "cargo", "rustc"):
            target = binary_dir / name
            target.write_text(TOOL)
            target.chmod(0o755)
        gui = self.root / "desktop/gui"
        gui.mkdir(parents=True)
        (gui / "Cargo.toml").write_text("[package]\nname = 'hole-desktop'\n")
        wrapper = self.root / "android/gradlew"
        wrapper.write_text(TOOL)
        wrapper.chmod(0o755)
        verifier = self.root / "android/scripts/verify-apk.py"
        verifier.parent.mkdir(parents=True)
        verifier.write_text(VERIFY)
        sdk = self.root / "sdk"
        for relative in ("platforms/android-37.0/android.jar", "ndk/28.2.13676358/source.properties", "build-tools/36.0.0/apksigner", "build-tools/36.0.0/zipalign", "build-tools/36.0.0/aapt2"):
            path = sdk / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.touch()
        self.toolchain = self.root / "toolchain.env"
        self.toolchain.write_text(f'export ANDROID_HOME="{sdk}"\nexport GOCACHE=/old/go-cache\nexport GOPATH=/old/go\nexport GOMODCACHE=/old/modules\nexport GRADLE_USER_HOME=/old/gradle\n')
        self.keystore = self.root / "signing keys/existing.keystore"
        self.keystore.parent.mkdir()
        self.keystore.write_text("existing-key-fixture")
        (self.root / "signing.env").write_text("HOLE_SIGNING_STORE_FILE='signing keys/existing.keystore'\nHOLE_SIGNING_STORE_PASSWORD='file-password'\nHOLE_SIGNING_KEY_ALIAS='file-alias'\nHOLE_SIGNING_KEY_PASSWORD='file-key-password'\n")
        self.log = self.root / "calls.jsonl"
        self.env = {key: value for key, value in os.environ.items() if not key.startswith(("GO", "HOLE_", "GRADLE_", "FAIL_"))}
        self.env.update(PATH=f"{binary_dir}:/usr/bin:/bin", HOLE_TOOLCHAIN_ENV=str(self.toolchain), BUILD_TEST_LOG=str(self.log))

    def invoke(self, *args, success=True, **env):
        result = subprocess.run(["bash", str(self.root / "build.sh"), *args], cwd=self.temporary.name,
                                env={**self.env, **env}, capture_output=True, text=True)
        if success:
            self.assertEqual(0, result.returncode, result.stdout + result.stderr)
        else:
            self.assertNotEqual(0, result.returncode, result.stdout + result.stderr)
        return result

    def calls(self, tool=None):
        calls = [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []
        return [call for call in calls if tool is None or call["tool"] == tool]

    def prepare_penmods(self):
        shutil.copytree(ROOT / "penmods", self.root / "penmods", ignore=shutil.ignore_patterns("build", ".xmake"))
        xmake = self.root / "tools/xmake"
        xmake.write_text(TOOL)
        xmake.chmod(0o755)

    def invoke_penmods(self, success=True, **env):
        result = subprocess.run(["bash", str(self.root / "penmods/build.sh")], cwd=self.temporary.name,
                                env={**self.env, **env}, capture_output=True, text=True)
        if success:
            self.assertEqual(0, result.returncode, result.stdout + result.stderr)
        else:
            self.assertNotEqual(0, result.returncode, result.stdout + result.stderr)
        return result

    def test_penmods_packages_current_core_and_qml(self):
        self.prepare_penmods()
        output = self.root / "dist/penmods/hole_plugin"
        self.invoke_penmods(GOOS="windows", GOARCH="amd64", CGO_ENABLED="1")
        build = next(call for call in self.calls("go") if call["args"][0] == "build")
        self.assertEqual("linux", build["env"]["GOOS"])
        self.assertEqual("arm64", build["env"]["GOARCH"])
        self.assertEqual("0", build["env"]["CGO_ENABLED"])
        self.assertEqual(str(self.root / ".cache/go-build"), build["env"]["GOCACHE"])
        self.assertIn("-mod=readonly", build["args"])
        self.assertIn("-trimpath", build["args"])
        version_flag = next(arg for arg in build["args"] if arg.startswith("-ldflags="))
        self.assertTrue(version_flag.startswith("-ldflags=-s -w -X hole/core.CoreVersion="))
        self.assertEqual("./cmd/hole-desktop-core", build["args"][-1])
        for relative in ("Main.qml", "SettingsPage.qml", "ConfigPage.qml", "metadata.json", "icon.png",
                         *[str(p.relative_to(ROOT / "penmods")) for p in (ROOT / "penmods/components").glob("*") if p.is_file()]):
            self.assertEqual((self.root / "penmods" / relative).read_bytes(), (output / relative).read_bytes())
        self.assertEqual("release-plugin-fixture", (output / "libhole_plugin.so").read_text())
        self.assertTrue(os.access(output / "hole-desktop-core", os.X_OK))
        checksum = (output / "hole-desktop-core.sha256").read_text().split()
        self.assertEqual([hashlib.sha256((output / "hole-desktop-core").read_bytes()).hexdigest(), "hole-desktop-core"], checksum)

        (self.root / "core/core.go").write_text("updated core fixture\n")
        (self.root / "penmods/Main.qml").write_text("updated QML fixture\n")
        self.invoke_penmods()
        builds = [call for call in self.calls("go") if call["args"][0] == "build"]
        self.assertEqual(2, len(builds))
        self.assertNotIn(version_flag, builds[-1]["args"])
        self.assertEqual("updated QML fixture\n", (output / "Main.qml").read_text())
        configurations = [call for call in self.calls("xmake") if call["args"][0] == "f"]
        self.assertEqual(2, len(configurations))
        for call in configurations:
            self.assertIn("-c", call["args"])
            self.assertIn("release", call["args"])

    def test_failed_penmods_build_preserves_existing_package(self):
        self.prepare_penmods()
        output = self.root / "dist/penmods/hole_plugin"
        output.mkdir(parents=True)
        core = output / "hole-desktop-core"
        core.write_text("previous-release")
        for failure in ("FAIL_GO", "FAIL_XMAKE"):
            with self.subTest(failure=failure):
                self.invoke_penmods(success=False, **{failure: "1"})
                self.assertEqual("previous-release", core.read_text())

    def test_help_and_empty_invocation_do_not_create_caches_or_run_tools(self):
        self.invoke("--help")
        self.invoke()
        self.assertFalse((self.root / ".cache").exists())
        self.assertEqual([], self.calls())

    def test_cache_env_can_be_sourced_in_bash_and_zsh_from_another_directory(self):
        for shell in ("bash", "zsh"):
            binary = shutil.which(shell)
            if not binary:
                continue
            with self.subTest(shell=shell):
                result = subprocess.run([binary, "-f", "-c", 'source "$1"; printf "%s\\n" "$HOLE_ROOT" "$GOCACHE"', shell,
                                         str(self.root / "scripts/build-env.sh")], cwd=self.temporary.name,
                                        env=self.env, capture_output=True, text=True)
                self.assertEqual(0, result.returncode, result.stderr)
                self.assertEqual([str(self.root), str(self.root / ".cache/go-build")], result.stdout.splitlines())

    def test_offline_cache_env_overrides_legacy_toolchain_proxy(self):
        with self.toolchain.open("a") as stream:
            stream.write("export GOPROXY=https://example.invalid\n")
        result = subprocess.run(["bash", "-c", 'source "$1"; hole_load_android_toolchain; printf "%s" "$GOPROXY"',
                                 "bash", str(self.root / "scripts/build-env.sh")],
                                env={**self.env, "HOLE_BUILD_OFFLINE": "1"}, capture_output=True, text=True)
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual("off", result.stdout)

    def test_bad_target_option_and_missing_value(self):
        for args in (("debug",), ("cli", "--wat"), ("cli", "--os"), ("cli", "--os", "--offline"), ("--offline",)):
            with self.subTest(args=args):
                self.invoke(*args, success=False)
        self.assertEqual([], self.calls())

    def test_cli_does_not_load_android_or_signing(self):
        self.toolchain.write_text("exit 93\n")
        (self.root / "signing.env").write_text("exit 94\n")
        self.invoke("cli")
        self.assertTrue((self.root / "dist/cli/hole-linux-amd64").is_file())
        self.assertTrue((self.root / "dist/cli/hole-linux-amd64.sha256").is_file())
        self.assertFalse(self.calls("gradlew"))
        build = next(call for call in self.calls("go") if call["args"][0] == "build")
        self.assertIn("-trimpath", build["args"])
        self.assertIn("-mod=readonly", build["args"])
        self.assertTrue(any(arg.startswith("-ldflags=-s -w -X hole/core.CoreVersion=") for arg in build["args"]))
        self.assertEqual("0", build["env"]["CGO_ENABLED"])

    def test_explicit_and_environment_cli_platforms(self):
        self.invoke("cli", "--os", "windows", "--arch", "amd64", GOOS="linux", GOARCH="arm64")
        self.assertTrue((self.root / "dist/cli/hole-windows-amd64.exe").is_file())
        self.invoke("cli", GOOS="linux", GOARCH="arm64")
        self.assertTrue((self.root / "dist/cli/hole-linux-arm64").is_file())
        self.invoke("cli", "--arch", "invalid", success=False)

    def test_desktop_core_is_opt_in_and_has_no_android_or_qt_requirement(self):
        self.toolchain.write_text("exit 93\n")
        (self.root / "signing.env").write_text("exit 94\n")
        self.invoke("desktop-core", "-t", "desktop-core", "--os", "windows", "--arch", "amd64", "--offline")
        binary = self.root / "dist/desktop-core/hole-desktop-core-windows-amd64.exe"
        self.assertTrue(binary.is_file())
        self.assertTrue(binary.with_name(binary.name + ".sha256").is_file())
        self.assertFalse((self.root / "dist/cli").exists())
        self.assertFalse(self.calls("gradlew"))
        builds = [call for call in self.calls("go") if call["args"][0] == "build"]
        self.assertEqual(1, len(builds))
        build = builds[0]
        self.assertEqual("./cmd/hole-desktop-core", build["args"][-1])
        self.assertIn("-trimpath", build["args"])
        self.assertIn("-mod=readonly", build["args"])
        self.assertIn("-buildvcs=false", build["args"])
        self.assertTrue(any(arg.startswith("-ldflags=-s -w -X hole/core.CoreVersion=") for arg in build["args"]))
        self.assertEqual("0", build["env"]["CGO_ENABLED"])
        self.assertEqual("off", build["env"]["GOPROXY"])

    def test_desktop_build_embeds_matching_bridge_and_packages_one_executable(self):
        self.toolchain.write_text("exit 93\n")
        (self.root / "signing.env").write_text("exit 94\n")
        self.invoke("desktop", "--offline")
        package = self.root / "dist/desktop/linux-amd64"
        gui = package / "hole-desktop"
        bridge = self.root / "dist/desktop-core/hole-desktop-core-linux-amd64"
        self.assertEqual("release-gui-fixture", gui.read_text())
        self.assertTrue(os.access(gui, os.X_OK))
        self.assertTrue(os.access(bridge, os.X_OK))
        self.assertEqual({"hole-desktop", "hole-desktop.sha256"}, {p.name for p in package.iterdir()})
        for binary in (gui, bridge):
            checksum = binary.with_name(binary.name + ".sha256").read_text().split()
            self.assertEqual([hashlib.sha256(binary.read_bytes()).hexdigest(), binary.name], checksum)
        builds = [call for call in self.calls("go") if call["args"][0] == "build"]
        self.assertEqual(["./cmd/hole-desktop-core"], [build["args"][-1] for build in builds])
        cargo = self.calls("cargo")[0]
        self.assertEqual(["build", "--release", "--locked", "--features", "embedded-core", "--offline"], cargo["args"])
        self.assertEqual(str(bridge), cargo["env"]["HOLE_EMBED_CORE"])
        self.assertEqual(str(self.root / ".cache/cargo-home"), cargo["env"]["CARGO_HOME"])
        self.assertIsNone(cargo["env"]["GOOS"])
        self.assertIsNone(cargo["env"]["GOARCH"])
        self.assertFalse(self.calls("gradlew"))

    def test_desktop_cross_target_and_combined_target_are_deduplicated(self):
        self.invoke("desktop", "desktop-core", "--os", "windows", "--arch", "amd64")
        package = self.root / "dist/desktop/windows-amd64"
        self.assertTrue((package / "hole-desktop.exe").is_file())
        self.assertFalse((package / "hole-desktop-core-windows-amd64.exe").exists())
        builds = [call for call in self.calls("go") if call["args"][0] == "build"]
        self.assertEqual(1, len(builds))
        self.assertEqual("windows", builds[0]["env"]["GOOS"])
        cargo = self.calls("cargo")[0]
        self.assertEqual("x86_64-pc-windows-gnu", cargo["args"][cargo["args"].index("--target") + 1])
        self.assertEqual(str(self.root / "dist/desktop-core/hole-desktop-core-windows-amd64.exe"), cargo["env"]["HOLE_EMBED_CORE"])

    def test_desktop_replaces_old_sidecar_package_and_scopes_embedded_input(self):
        package = self.root / "dist/desktop/linux-amd64"
        package.mkdir(parents=True)
        (package / "hole-desktop-core-linux-amd64").write_text("old sidecar")
        self.invoke("desktop", "android", HOLE_EMBED_CORE="/wrong/old-core")
        self.assertEqual({"hole-desktop", "hole-desktop.sha256"}, {p.name for p in package.iterdir()})
        self.assertEqual(str(self.root / "dist/desktop-core/hole-desktop-core-linux-amd64"), self.calls("cargo")[0]["env"]["HOLE_EMBED_CORE"])
        # The build's chosen input is local to Cargo and doesn't overwrite the caller's environment.
        self.assertEqual("/wrong/old-core", self.calls("gradlew")[0]["env"]["HOLE_EMBED_CORE"])

    def test_desktop_custom_output_embeds_absolute_path_with_spaces(self):
        output = self.root / "single file output"
        self.invoke("desktop", "--output", str(output))
        self.assertEqual(str(output / "desktop-core/hole-desktop-core-linux-amd64"), self.calls("cargo")[0]["env"]["HOLE_EMBED_CORE"])
        self.assertTrue((output / "desktop/linux-amd64/hole-desktop").is_file())

    def test_desktop_rejects_unsupported_target_before_building(self):
        self.invoke("desktop", "--os", "darwin", "--arch", "amd64", success=False)
        self.assertFalse([call for call in self.calls("go") if call["args"][0] == "build"])
        self.assertFalse(self.calls("cargo"))

    def test_failed_desktop_gui_build_preserves_existing_package(self):
        self.invoke("desktop")
        package = self.root / "dist/desktop/linux-amd64"
        previous = {path.name: path.read_bytes() for path in package.iterdir()}
        self.invoke("desktop", success=False, FAIL_CARGO="1")
        self.assertEqual(previous, {path.name: path.read_bytes() for path in package.iterdir()})

    def test_cli_and_all_keep_the_existing_target_set(self):
        for target in ("cli", "all"):
            with self.subTest(target=target):
                self.log.unlink(missing_ok=True)
                self.invoke(target)
                builds = [call for call in self.calls("go") if call["args"][0] == "build"]
                self.assertEqual(["."], [build["args"][-1] for build in builds])
                self.assertFalse((self.root / "dist/desktop-core").exists())
                self.assertFalse((self.root / "dist/desktop").exists())
                self.assertFalse(self.calls("cargo"))

    def test_desktop_core_can_combine_with_all_without_leaking_cross_settings(self):
        self.invoke("desktop-core", "all", "desktop-core", "--os", "windows", "--arch", "amd64")
        builds = [call for call in self.calls("go") if call["args"][0] == "build"]
        self.assertEqual([".", "./cmd/hole-desktop-core"], [build["args"][-1] for build in builds])
        self.assertEqual(1, len(self.calls("gradlew")))
        for key in ("GOOS", "GOARCH", "CGO_ENABLED"):
            self.assertIsNone(self.calls("gradlew")[0]["env"][key])

    def test_desktop_core_output_and_failed_build_preserve_existing_artifacts(self):
        output = self.root / "desktop output"
        self.invoke("desktop-core", "--output", str(output), GOOS="linux", GOARCH="arm64")
        binary = output / "desktop-core/hole-desktop-core-linux-arm64"
        self.assertTrue(binary.is_file())
        previous = binary.read_bytes()
        checksum = binary.with_name(binary.name + ".sha256").read_bytes()
        self.invoke("desktop-core", "--output", str(output), success=False,
                    GOOS="linux", GOARCH="arm64", FAIL_GO="1")
        self.assertEqual(previous, binary.read_bytes())
        self.assertEqual(checksum, binary.with_name(binary.name + ".sha256").read_bytes())
        self.invoke("desktop-core", "--arch", "invalid", success=False)

    def test_combined_android_targets_are_release_and_deduplicated(self):
        self.invoke("phone", "wear", "-t", "android", "wear")
        self.assertEqual(1, len(self.calls("gradlew")))
        args = self.calls("gradlew")[0]["args"]
        self.assertEqual(1, args.count(":app:assembleRelease"))
        self.assertEqual(1, args.count(":wear:assembleRelease"))
        self.assertNotIn(":app:assembleDebug", args)
        self.assertEqual(2, len(self.calls("verify")))

    def test_wear_only_does_not_build_phone_or_cli(self):
        self.invoke("wear")
        args = self.calls("gradlew")[0]["args"]
        self.assertIn(":wear:assembleRelease", args)
        self.assertNotIn(":app:assembleRelease", args)
        self.assertFalse([call for call in self.calls("go") if call["args"][0] == "build"])

    def test_all_resets_cross_compilation_and_legacy_cache_locations(self):
        self.invoke("all", "--os", "windows", "--arch", "amd64", GOOS="windows", GOARCH="amd64")
        gradle = self.calls("gradlew")[0]
        self.assertIsNone(gradle["env"]["GOOS"])
        self.assertIsNone(gradle["env"]["GOARCH"])
        for key in ("GOCACHE", "GOPATH", "GOMODCACHE", "GRADLE_USER_HOME", "TMPDIR"):
            self.assertTrue(gradle["env"][key].startswith(str(self.root / ".cache") + "/"), key)
        self.assertIn(str(self.root / ".cache/gradle-project"), gradle["args"])
        self.assertIn(f"-Pkotlin.project.persistent.dir={self.root}/.cache/kotlin", gradle["args"])

    def test_signing_environment_overrides_file_and_passwords_are_not_arguments(self):
        result = self.invoke("android", HOLE_SIGNING_KEY_ALIAS="environment-alias", HOLE_SIGNING_STORE_PASSWORD="environment-password")
        gradle = self.calls("gradlew")[0]
        self.assertEqual("environment-alias", gradle["env"]["HOLE_SIGNING_KEY_ALIAS"])
        self.assertEqual(str(self.keystore), gradle["env"]["HOLE_SIGNING_STORE_FILE"])
        self.assertIn("-storepass:env", self.calls("keytool")[0]["args"])
        self.assertNotIn("environment-password", result.stdout + result.stderr + self.log.read_text())

    def test_missing_signing_is_reported_before_any_build(self):
        (self.root / "signing.env").unlink()
        self.invoke("all", success=False)
        self.assertFalse(self.calls("gradlew"))
        self.assertFalse([call for call in self.calls("go") if call["args"][0] == "build"])

    def test_explicit_signing_config_and_output_with_spaces(self):
        signing = self.root / "custom signing.env"
        (self.root / "signing.env").rename(signing)
        output = Path(self.temporary.name) / "output directory"
        self.invoke("all", "--signing-config", str(signing), "--output", str(output))
        self.assertTrue((output / "cli/hole-linux-amd64").is_file())
        self.assertIn(str(output / "android"), self.calls("verify")[0]["args"])
        self.invoke("android", "--signing-config", "absent.env", success=False)

    def test_offline_applies_to_go_and_gradle(self):
        self.invoke("all", "--offline")
        self.assertIn("--offline", self.calls("gradlew")[0]["args"])
        build = next(call for call in self.calls("go") if call["args"][0] == "build")
        self.assertEqual("off", build["env"]["GOPROXY"])

    def test_failed_cli_does_not_replace_existing_artifact(self):
        artifact = self.root / "dist/cli/hole-linux-amd64"
        artifact.parent.mkdir(parents=True)
        artifact.write_text("previous-release")
        self.invoke("cli", success=False, FAIL_GO="1")
        self.assertEqual("previous-release", artifact.read_text())

    def test_failed_gradle_does_not_publish_artifacts(self):
        self.invoke("android", success=False, FAIL_GRADLE="1")
        self.assertFalse(self.calls("verify"))

    def test_core_version_works_in_source_archive(self):
        spec = importlib.util.spec_from_file_location("fixture_build_meta", self.root / "scripts/build_meta.py")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        self.assertRegex(module.core_version(), r"^unknown-worktree-[0-9a-f]{12}$")
        previous = module.core_version()
        (self.root / "core/core_test.go").write_text("excluded test\n")
        self.assertEqual(previous, module.core_version())
        for relative in ("desktop/bridge.go", "cmd/hole-desktop-core/main.go", "desktop/gui/Main.qml"):
            path = self.root / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("desktop-only fixture\n")
        self.assertEqual(previous, module.core_version(), "desktop changes invalidated the shared AAR source identity")
        (self.root / "core/core.go").write_text("changed core\n")
        self.assertNotEqual(previous, module.core_version())


if __name__ == "__main__":
    unittest.main()
