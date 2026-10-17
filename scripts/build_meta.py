#!/usr/bin/env python3
"""Shared release versions, source identity and checksums."""
import hashlib
import json
import re
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
VERSION_FILE = ROOT / "versions.json"
SEMVER = re.compile(r"\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?\Z")


def sha256(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def source_commit(short=False):
    command = ["git", "-C", str(ROOT), "rev-parse"]
    if short:
        command.append("--short")
    try:
        top = subprocess.check_output(["git", "-C", str(ROOT), "rev-parse", "--show-toplevel"], stderr=subprocess.DEVNULL, text=True).strip()
        if Path(top).resolve() != ROOT:
            return "unknown"
        return subprocess.check_output(command + ["HEAD"], stderr=subprocess.DEVNULL, text=True).strip()
    except (OSError, subprocess.CalledProcessError):
        return "unknown"


def versions():
    with VERSION_FILE.open(encoding="utf-8") as stream:
        return json.load(stream)


def core_version():
    version = versions()["core"]["version"]
    if not isinstance(version, str) or not SEMVER.fullmatch(version):
        raise ValueError("versions.json core.version must be a semantic version")
    return version


def frontend_version(name):
    version = versions()["frontends"][name]["version"]
    if not isinstance(version, str) or not SEMVER.fullmatch(version):
        raise ValueError(f"versions.json frontends.{name}.version must be a semantic version")
    return version


def core_build_id():
    sources = sorted([*ROOT.glob("core/**/*.go"), *ROOT.glob("mobile/**/*.go")])
    sources = [path for path in sources if not path.name.endswith("_test.go")]
    sources += [ROOT / name for name in (
        "go.mod", "go.sum", "android/corebridge/gobuild/go.mod",
        "android/corebridge/gobuild/go.sum", "core/compat/anet/go.mod",
    )]
    listing = "".join(f"{sha256(path)}  {path.relative_to(ROOT).as_posix()}\n" for path in sources)
    digest = hashlib.sha256(listing.encode()).hexdigest()[:12]
    return f"{source_commit(short=True)}-worktree-{digest}"


def write_checksum(path):
    path = Path(path)
    path.with_name(path.name + ".sha256").write_text(f"{sha256(path)}  {path.name}\n")


if __name__ == "__main__":
    if sys.argv[1:] == ["version"]:
        print(core_version())
    elif sys.argv[1:] == ["build-id"]:
        print(core_build_id())
    elif len(sys.argv) == 3 and sys.argv[1] == "checksum":
        write_checksum(sys.argv[2])
    else:
        sys.exit("Usage: build_meta.py version | build-id | checksum FILE")
