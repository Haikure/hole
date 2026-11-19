"""Exercise the workflow's actual matrix planner without GitHub or a build."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ReleaseWorkflowTests(unittest.TestCase):
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
        self.assertEqual("true", plan["android"])
        self.assertEqual("false", plan["has_targets"])
        self.assertEqual([], json.loads(plan["matrix"])["include"])
        self.assertEqual([{"target": "wear", "abis": "arm32-arm64"}], json.loads(plan["android_matrix"])["include"])

    def test_android_only_still_builds_phone_arm64(self):
        plan = self.plan("android")
        self.assertEqual([{"target": "android", "abis": "arm64"}], json.loads(plan["android_matrix"])["include"])

    def test_all_includes_phone_and_wear_alongside_existing_desktop_and_go_targets(self):
        plan = self.plan("all")
        self.assertEqual([{"target": "android", "abis": "arm64"}, {"target": "wear", "abis": "arm32-arm64"}],
                         json.loads(plan["android_matrix"])["include"])
        self.assertEqual(11, len(json.loads(plan["matrix"])["include"]))

    def test_desktop_only_does_not_start_android_or_wear(self):
        plan = self.plan("desktop")
        self.assertEqual("false", plan["android"])
        self.assertEqual([], json.loads(plan["android_matrix"])["include"])
        self.assertEqual(3, len(json.loads(plan["matrix"])["include"]))


if __name__ == "__main__":
    unittest.main()
