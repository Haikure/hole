#!/usr/bin/env bash
# Shared Gradle entry for local development and release packaging.
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$ROOT/scripts/build-env.sh"
source "$ROOT/desktop/scripts/toolchain.sh"
hole_load_desktop_toolchain
cd "$ROOT"

args=(--no-daemon --console=plain -p "$ROOT/desktop/compose"
  --project-cache-dir "$HOLE_CACHE_ROOT/gradle-project-desktop"
  "-Pkotlin.project.persistent.dir=$HOLE_CACHE_ROOT/kotlin-desktop" "-Djava.io.tmpdir=$TMPDIR")
if [[ "${HOLE_BUILD_OFFLINE:-0}" == 1 ]]; then args+=(--offline); fi

if bash "$ROOT/android/gradlew" "${args[@]}" "$@"; then
  exit 0
else
  status=$?
fi
# Compose redirects jpackage diagnostics to files; show them locally and in CI.
for log in "$ROOT"/desktop/compose/build/compose/logs/*/jpackage-*.txt; do
  [[ -f "$log" ]] || continue
  printf '\n[Desktop] %s\n' "$log" >&2
  tail -n 160 "$log" >&2
done
exit "$status"
