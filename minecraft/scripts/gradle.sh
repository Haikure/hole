#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$ROOT/scripts/build-env.sh"
hole_load_android_toolchain
export JAVA_HOME="${HOLE_MINECRAFT_JAVA_HOME:-${JDK17_HOME:-/usr/lib/jvm/java-17-openjdk-amd64}}"
export PATH="$JAVA_HOME/bin:$PATH"
[[ -x "$JAVA_HOME/bin/javac" ]] || { echo '需要已有 JDK 17，使用 HOLE_MINECRAFT_JAVA_HOME 指定' >&2; exit 1; }
gradle_cmd="${HOLE_MINECRAFT_GRADLE:-$(command -v gradle || true)}"
[[ -x "$gradle_cmd" ]] || { echo '需要已有 Gradle 8，使用 HOLE_MINECRAFT_GRADLE 指定' >&2; exit 1; }
[[ "$($gradle_cmd --version)" == *'Gradle 8.'* ]] || { echo 'ForgeGradle 6 需要 Gradle 8' >&2; exit 1; }
args=(--no-daemon --console=plain -p "$ROOT/minecraft" --project-cache-dir "$HOLE_CACHE_ROOT/gradle-project-minecraft" "-Djava.io.tmpdir=$TMPDIR")
if [[ "${HOLE_BUILD_OFFLINE:-0}" == 1 ]]; then args+=(--offline); fi
exec "$gradle_cmd" "${args[@]}" "$@"
