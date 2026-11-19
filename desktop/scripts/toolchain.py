#!/usr/bin/env python3
"""Select an existing desktop JDK without installing or running Java tools."""
import os
from pathlib import Path
import re
import shutil
import sys


def jdk_path(value):
    # Accept Git Bash paths as well as Windows JAVA_HOME values.
    if os.name == "nt" and re.match(r"^/[A-Za-z]/", value):
        value = value[1] + ":" + value[2:]
    return Path(value).expanduser().resolve()


def jdk_version(path):
    try:
        release = (path / "release").read_text(encoding="utf-8")
    except OSError:
        return None
    match = re.search(r'^JAVA_VERSION="(\d+)(?:[.\-+"]|$)', release, re.MULTILINE)
    if not match or int(match[1]) < 21:
        return None
    suffix = ".exe" if os.name == "nt" else ""
    for tool in ("java", "javac", "jlink", "jpackage"):
        if not os.access(path / "bin" / (tool + suffix), os.X_OK):
            return None
    if not (path / "include" / "jni.h").is_file():
        return None
    # Compose ProGuard reads JDK classes from jmods; some JDK 25 distributions
    # omit these even though javac, jlink and jpackage are present.
    if not (path / "jmods" / "java.base.jmod").is_file():
        return None
    return int(match[1])


def installed_jdks():
    javac = shutil.which("javac")
    if javac:
        yield Path(javac).resolve().parent.parent
    roots = [Path("/usr/lib/jvm"), Path("/usr/java"), Path.home() / ".sdkman/candidates/java"]
    if os.name == "nt":
        for name in ("ProgramFiles", "ProgramW6432"):
            if os.environ.get(name):
                base = Path(os.environ[name])
                roots.extend(base / vendor for vendor in ("Java", "Eclipse Adoptium", "Microsoft", "Zulu"))
    for root in roots:
        try:
            yield from root.iterdir()
        except OSError:
            continue


def select_jdk(environ, discovered):
    explicit = environ.get("HOLE_DESKTOP_JAVA_HOME")
    if explicit:
        path = jdk_path(explicit)
        if jdk_version(path) is None:
            raise ValueError("HOLE_DESKTOP_JAVA_HOME 需要完整 JDK 21+（含 javac、jlink、jpackage、JNI 头文件与 jmods/java.base.jmod）：" + str(path))
        return path
    for name in ("JAVA_HOME", "JDK25_HOME", "JDK21_HOME"):
        if environ.get(name):
            path = jdk_path(environ[name])
            if jdk_version(path) is not None:
                return path
    candidates = {}
    for value in discovered:
        path = jdk_path(str(value))
        version = jdk_version(path)
        if version is not None:
            candidates[path] = version
    if candidates:
        return max(candidates, key=lambda path: (candidates[path], str(path)))
    raise ValueError("未找到完整桌面 JDK 21+（需含 jmods/java.base.jmod）；可使用带 jmods 的 JDK 25，通过 HOLE_DESKTOP_JAVA_HOME 指定目录。不会自动安装 JDK。")


if __name__ == "__main__":
    try:
        print(select_jdk(os.environ, installed_jdks()))
    except ValueError as failure:
        sys.exit(str(failure))
