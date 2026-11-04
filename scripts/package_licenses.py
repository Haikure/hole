#!/usr/bin/env python3
"""Package current corresponding source and original dependency license texts."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tomllib
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def objects(text):
    decoder = json.JSONDecoder()
    while text.strip():
        value, end = decoder.raw_decode(text.lstrip())
        yield value
        text = text.lstrip()[end:]


def package(output):
    output = Path(output)
    licenses = output / "licenses"
    licenses.mkdir(parents=True, exist_ok=True)
    shutil.copy2(ROOT / "LICENSE", licenses / "GPL-2.0.txt")
    shutil.copytree(ROOT / "LICENSES", licenses, dirs_exist_ok=True)
    shutil.copy2(ROOT / "THIRD_PARTY_NOTICES.md", licenses)
    manifest = []
    dependency_sources = {}

    def collect(name, version, directory, ecosystem):
        directory = Path(directory)
        texts = [p for p in directory.iterdir() if p.is_file() and p.name.upper().startswith(("LICENSE", "COPYING", "NOTICE", "COPYRIGHT"))]
        target = licenses / ecosystem / (name.replace("/", "_") + "@" + version)
        if str(target.relative_to(licenses)) in dependency_sources:
            return
        target.mkdir(parents=True, exist_ok=True)
        for path in texts:
            destination = target / path.name
            if destination.exists():
                destination.chmod(0o644)
            shutil.copyfile(path, destination)
        manifest.append({"name": name, "version": version, "ecosystem": ecosystem, "license_files": [p.name for p in texts]})
        dependency_sources[str(target.relative_to(licenses))] = directory

    for graph in (ROOT, ROOT / "android/corebridge/gobuild"):
        modules = subprocess.check_output(["go", "list", "-mod=readonly", "-m", "-json", "all"], cwd=graph, text=True)
        for module in objects(modules):
            if module.get("Main") or module["Path"] == "hole":
                continue
            resolved = module.get("Replace", module)
            if resolved.get("Dir"):
                collect(module["Path"], module.get("Version", "local"), resolved["Dir"], "go")
    lock = ROOT / "desktop/gui/Cargo.lock"
    if lock.exists():
        for crate in tomllib.loads(lock.read_text())["package"]:
            for path in (ROOT / ".cache/cargo-home/registry/src").glob("*/" + crate["name"] + "-" + crate["version"]):
                collect(crate["name"], crate["version"], path, "cargo")
    # Maven/Gradle libraries often carry original notices inside META-INF.
    for path in (ROOT / ".cache/gradle/caches/modules-2/files-2.1").glob("**/*"):
        if path.suffix not in {".jar", ".aar"}:
            continue
        try:
            with zipfile.ZipFile(path) as archive:
                for entry in archive.infolist():
                    if entry.file_size > 1024 * 1024 or not Path(entry.filename).name.upper().startswith(("LICENSE", "NOTICE", "COPYING")):
                        continue
                    if ".." in Path(entry.filename).parts:
                        continue
                    dest = licenses / "gradle" / path.parent.parent.name / entry.filename
                    dest.parent.mkdir(parents=True, exist_ok=True)
                    dest.write_bytes(archive.read(entry))
        except zipfile.BadZipFile:
            continue
    (licenses / "dependencies.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    # Source distributions can be rebuilt without a .git directory.
    if (ROOT / ".git").exists():
        paths = subprocess.check_output(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=ROOT).decode().split("\0")
    else:
        paths = []
        excluded = {".cache", "dist", "build", "target", ".git", ".gradle", ".kotlin", ".xmake", ".ci-bin", "tools", "signing keys", "sdk"}
        for current, directories, files in os.walk(ROOT):
            directories[:] = [d for d in directories if d not in excluded]
            paths.extend(str((Path(current) / name).relative_to(ROOT)) for name in files)
    with tarfile.open(output / "hole-source.tar.gz", "w:gz", compresslevel=1) as archive:
        for relative in sorted(set(paths)):
            path = ROOT / relative
            if not relative or not path.is_file() or path.is_symlink():
                continue
            if path.resolve().is_relative_to(output.resolve()):
                continue
            if relative.startswith(("dist/", ".cache/")) or path.suffix in (".keystore", ".jks", ".apk", ".aar"):
                continue
            if path.name.endswith(".env") or path.name in {"config.yaml", ".dev.vars", "wrangler.toml", "local.properties", "calls.jsonl"}:
                continue
            archive.add(path, arcname="hole/" + relative, recursive=False)
        for relative, directory in sorted(dependency_sources.items()):
            for path in sorted(directory.rglob("*")):
                if path.is_file() and not path.is_symlink():
                    archive.add(path, arcname="hole/third_party/" + relative + "/" + str(path.relative_to(directory)), recursive=False)


if __name__ == "__main__":
    package(sys.argv[1])
