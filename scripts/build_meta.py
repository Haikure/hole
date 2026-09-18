#!/usr/bin/env python3
"""Shared, archive-friendly source identity and release checksums."""
import hashlib
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


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


def core_version():
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
    elif len(sys.argv) == 3 and sys.argv[1] == "checksum":
        write_checksum(sys.argv[2])
    else:
        sys.exit("Usage: build_meta.py version | checksum FILE")
