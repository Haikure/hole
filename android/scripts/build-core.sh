#!/usr/bin/env bash
# Uses installed tools only. No go install, SDK installation or Gradle download.
set -euo pipefail
# Keep source ordering and version digests independent of the caller's locale.
export LC_ALL=C
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$ROOT/scripts/build-env.sh"
hole_load_android_toolchain
# A caller may be cross-compiling the CLI in the same shell.
unset GOOS GOARCH GOARM GOARM64 GOAMD64 GO386
export CGO_ENABLED=1
MOBILE_VERSION=v0.0.0-20260908204917-8b95e45f8d3e
NDK_VERSION=28.2.13676358
for command in go gomobile gobind java javac python3; do
  command -v "$command" >/dev/null || { echo "缺少工具 $command，请先安装。" >&2; exit 1; }
done
[[ "$(GOTOOLCHAIN=local go env GOVERSION)" == go1.26.4 ]] || { echo '本工程锁定 Go 1.26.4。' >&2; exit 1; }
[[ "$(javac -version 2>&1)" == 'javac 17.'* ]] || { echo '本工程需要 JDK 17。' >&2; exit 1; }
for binary in gomobile gobind; do
  info=$(go version -m "$(command -v "$binary")")
  [[ "$info" == *$'\tmod\tgolang.org/x/mobile\t'"$MOBILE_VERSION"$'\t'* ]] || {
    echo "$binary 需要 x/mobile $MOBILE_VERSION。" >&2; exit 1;
  }
done
[[ -f "${ANDROID_HOME:?请设置 ANDROID_HOME}/platforms/android-37.0/android.jar" ]] || { echo '缺少 SDK Platform 37.0。' >&2; exit 1; }
export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/$NDK_VERSION"
export ANDROID_NDK_ROOT="$ANDROID_NDK_HOME"
[[ -f "$ANDROID_NDK_HOME/source.properties" ]] || { echo "缺少 NDK $NDK_VERSION。" >&2; exit 1; }
export GOTOOLCHAIN=local GOWORK=off
OUTPUT=${1:-$ROOT/android/corebridge/libs/holecore.aar}
mkdir -p "$(dirname "$OUTPUT")"
OUTPUT=$(cd "$(dirname "$OUTPUT")" && pwd)/$(basename "$OUTPUT")
WORK=$(mktemp -d "$(dirname "$OUTPUT")/.core-build.XXXXXXXX")
trap 'rm -rf -- "$WORK"' EXIT
CORE_VERSION=$(python3 "$ROOT/scripts/build_meta.py" version)
cd "$ROOT/android/corebridge/gobuild"
gomobile bind \
  -target=android/arm,android/arm64 -androidapi=26 -javapkg=dev.hole.core \
  -trimpath -ldflags="-s -w -X hole/core.CoreVersion=$CORE_VERSION -extldflags=-Wl,-z,max-page-size=16384" \
  -o "$WORK/holecore.aar" hole/mobile
mv "$WORK/holecore.aar" "$OUTPUT"
printf 'AAR: %s\nCore: %s\nx/mobile: %s\n' "$OUTPUT" "$CORE_VERSION" "$MOBILE_VERSION"
