"""Exercise release planning and artifact scripts without GitHub or a toolchain."""
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ReleaseWorkflowTests(unittest.TestCase):
    def step(self, name):
        workflow = (ROOT / ".github/workflows/build.yaml").read_text(encoding="utf-8")
        step = workflow.split(f"      - name: {name}\n", 1)[1]
        return re.split(r"\n(?:      - |  \S)", step, maxsplit=1)[0]

    def script(self, name):
        return textwrap.dedent(self.step(name).split("        run: |\n", 1)[1])

    def plan(self, target, arch="all", system="all"):
        workflow = (ROOT / ".github/workflows/build.yaml").read_text(encoding="utf-8")
        planner = textwrap.dedent(workflow.split("python3 - <<'PY'\n", 1)[1].split("\n          PY", 1)[0])
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "output"
            result = subprocess.run([sys.executable, "-c", planner], capture_output=True, text=True,
                                    env={**os.environ, "BUILD_TYPE": target, "BUILD_ARCH": arch,
                                         "BUILD_OS": system, "GITHUB_OUTPUT": str(output)})
            self.assertEqual(0, result.returncode, result.stderr)
            return dict(line.split("=", 1) for line in output.read_text(encoding="utf-8").splitlines())

    def test_wear_only_ignores_non_android_filters_and_has_both_arm_abis(self):
        plan = self.plan("wear", arch="amd64", system="windows")
        self.assertEqual("false", plan["minecraft"])
        self.assertEqual("true", plan["android"])
        self.assertEqual("false", plan["has_targets"])
        self.assertEqual([], json.loads(plan["matrix"])["include"])
        self.assertEqual([{"target": "wear", "abis": "arm32-arm64"}], json.loads(plan["android_matrix"])["include"])

    def test_android_only_still_builds_phone_arm64(self):
        plan = self.plan("android")
        self.assertEqual("false", plan["minecraft"])
        self.assertEqual([{"target": "android", "abis": "arm64"}], json.loads(plan["android_matrix"])["include"])

    def test_all_includes_minecraft_phone_and_wear_alongside_desktop_and_go_targets(self):
        plan = self.plan("all")
        self.assertEqual("true", plan["minecraft"])
        self.assertEqual([{"target": "android", "abis": "arm64"}, {"target": "wear", "abis": "arm32-arm64"}],
                         json.loads(plan["android_matrix"])["include"])
        self.assertEqual(11, len(json.loads(plan["matrix"])["include"]))

    def test_desktop_only_does_not_start_android_or_wear(self):
        plan = self.plan("desktop")
        self.assertEqual("false", plan["minecraft"])
        self.assertEqual("false", plan["android"])
        self.assertEqual([], json.loads(plan["android_matrix"])["include"])
        self.assertEqual(3, len(json.loads(plan["matrix"])["include"]))

    def test_minecraft_only_ignores_filters_and_does_not_start_other_builds(self):
        for arch in ("all", "amd64", "arm64"):
            for system in ("all", "linux", "windows"):
                with self.subTest(arch=arch, system=system):
                    plan = self.plan("minecraft", arch=arch, system=system)
                    self.assertEqual("true", plan["minecraft"])
                    self.assertEqual("false", plan["has_targets"])
                    self.assertEqual("false", plan["android"])
                    self.assertEqual([], json.loads(plan["matrix"])["include"])
                    self.assertEqual([], json.loads(plan["android_matrix"])["include"])

    def test_go_only_does_not_start_minecraft(self):
        for target in ("core", "cli"):
            with self.subTest(target=target):
                self.assertEqual("false", self.plan(target)["minecraft"])

    def test_minecraft_build_requires_exactly_one_jar_and_no_platform_filters(self):
        for count in (0, 1, 2):
            with self.subTest(count=count), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / "build.sh").write_text(
                    'set -eu\n[[ "$*" == "minecraft --output dist" ]]\n', encoding="utf-8")
                destination = root / "dist/minecraft"
                destination.mkdir(parents=True)
                for index in range(count):
                    (destination / f"hole-forge-{index}.jar").write_bytes(b"forge-fixture")
                result = subprocess.run(["bash", "-c", self.script("Build Minecraft release")],
                                        cwd=root, capture_output=True, text=True)
                if count == 1:
                    self.assertEqual(0, result.returncode, result.stderr)
                else:
                    self.assertNotEqual(0, result.returncode)
                    self.assertIn("Forge JAR", result.stderr)

    def test_minecraft_jar_is_uploaded_and_collected_for_release_with_its_original_name(self):
        workflow = (ROOT / ".github/workflows/build.yaml").read_text(encoding="utf-8")
        release_dependencies = workflow.split("\n  release:\n", 1)[1].split("\n    if:", 1)[0]
        self.assertIn("\n      - minecraft\n", release_dependencies + "\n")
        upload = self.step("Upload Minecraft artifact")
        artifact_name = re.search(r"^          name: (.+)$", upload, re.MULTILINE).group(1)
        artifact_glob = re.search(r"^          path: (.+)$", upload, re.MULTILINE).group(1)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            destination = root / "dist/minecraft"
            destination.mkdir(parents=True)
            filename = "hole-forge-1.20.1-0.1.0.jar"
            (destination / filename).write_bytes(b"forge-fixture")
            (destination / f"{filename}.sha256").write_text("checksum-fixture", encoding="utf-8")
            artifacts = root / "artifacts" / artifact_name
            artifacts.mkdir(parents=True)
            selected = list(root.glob(artifact_glob))
            self.assertEqual([filename], [path.name for path in selected])
            for path in selected:
                shutil.copy2(path, artifacts / path.name)
            result = subprocess.run(["bash", "-c", self.script("Prepare release assets")],
                                    cwd=root, capture_output=True, text=True)
            self.assertEqual(0, result.returncode, result.stderr)
            assets = root / "release-assets"
            self.assertEqual([filename], sorted(path.name for path in assets.iterdir()))
            self.assertEqual(b"forge-fixture", (assets / filename).read_bytes())


if __name__ == "__main__":
    unittest.main()
