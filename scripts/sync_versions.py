#!/usr/bin/env python3
"""Sync frontend versions that must also appear in native package metadata."""
import argparse
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))
from build_meta import frontend_version, versions


def replace_once(path, pattern, replacement, label):
    text = path.read_text(encoding="utf-8")
    updated, count = re.subn(pattern, replacement, text, count=1, flags=re.MULTILINE)
    if count != 1:
        raise ValueError(f"Expected one {label} in {path.relative_to(ROOT)}, found {count}")
    return updated


def desired_files():
    config = versions()
    android_code = config["frontends"]["android"]["version_code"]
    if not isinstance(android_code, int) or android_code < 1:
        raise ValueError("versions.json frontends.android.version_code must be a positive integer")

    desktop_version = frontend_version("desktop_gui")
    plugin_version = frontend_version("penmods")

    cargo_manifest = ROOT / "desktop/gui/Cargo.toml"
    cargo_text = cargo_manifest.read_text(encoding="utf-8")
    package = re.search(r"(?ms)^\[package\]\n(.*?)(?=^\[|\Z)", cargo_text)
    if not package:
        raise ValueError("Could not find [package] in desktop/gui/Cargo.toml")
    package_text, count = re.subn(
        r'(?m)^version\s*=\s*"[^"]+"$',
        f'version = "{desktop_version}"',
        package.group(1),
        count=1,
    )
    if count != 1:
        raise ValueError("Could not find package version in desktop/gui/Cargo.toml")
    cargo_text = cargo_text[:package.start(1)] + package_text + cargo_text[package.end(1):]

    lockfile = ROOT / "desktop/gui/Cargo.lock"
    lock_text = replace_once(
        lockfile,
        r'(^\[\[package\]\]\nname = "hole-desktop"\nversion = ")[^"]+(")',
        rf'\g<1>{desktop_version}\g<2>',
        "hole-desktop package version",
    )

    xmake = ROOT / "penmods/xmake.lua"
    xmake_text = replace_once(
        xmake,
        r"^set_version\('[^']+'\)$",
        f"set_version('{plugin_version}')",
        "xmake version",
    )

    metadata = ROOT / "penmods/metadata.json"
    metadata_doc = json.loads(metadata.read_text(encoding="utf-8"))
    metadata_doc["version"] = plugin_version
    metadata_text = json.dumps(metadata_doc, ensure_ascii=False, indent=2) + "\n"

    return {
        cargo_manifest: cargo_text,
        lockfile: lock_text,
        xmake: xmake_text,
        metadata: metadata_text,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="report stale generated versions without writing files")
    args = parser.parse_args()
    stale = []
    for path, expected in desired_files().items():
        current = path.read_text(encoding="utf-8")
        if current == expected:
            continue
        stale.append(path)
        if not args.check:
            path.write_text(expected, encoding="utf-8")
    if stale:
        names = ", ".join(path.relative_to(ROOT).as_posix() for path in stale)
        if args.check:
            print(f"版本文件未同步：{names}\n运行 python3 scripts/sync_versions.py。", file=sys.stderr)
            return 1
        print(f"已同步版本文件：{names}")
    elif args.check:
        print("版本文件与 versions.json 一致。")
    else:
        print("版本文件与 versions.json 一致，无需更新。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
