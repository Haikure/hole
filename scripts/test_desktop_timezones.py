"""Run Rust NAT timestamp cases in separate processes with real local time zones.

CI sources scripts/build-env.sh and supplies HOLE_EMBED_CORE before this script.
Windows Chrono reads OS time zone rules, so TZ alone cannot test that platform.
"""
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
ZONES = (
    ("UTC", "UTC"),
    ("Asia/Shanghai", "China Standard Time"),
    ("America/New_York", "Eastern Standard Time"),
)


def main():
    windows = os.name == "nt"
    original_zone = None
    if windows:
        original_zone = subprocess.check_output(["tzutil", "/g"], text=True).strip()
    try:
        for zone, windows_zone in ZONES:
            env = os.environ.copy()
            env["HOLE_TEST_TIME_ZONE"] = zone
            if windows:
                env.pop("TZ", None)
                subprocess.run(["tzutil", "/s", windows_zone], check=True)
            else:
                env["TZ"] = zone
            print(f"NAT timestamp tests: {zone}", flush=True)
            subprocess.run([
                "cargo", "test", "--locked", "--release", "--features", "embedded-core",
                "snapshot::tests::nat_time_system_timezone_cases", "--", "--ignored", "--exact", "--nocapture",
            ], cwd=ROOT / "desktop/gui", env=env, check=True)
    finally:
        if original_zone is not None:
            subprocess.run(["tzutil", "/s", original_zone], check=True)


if __name__ == "__main__":
    main()
