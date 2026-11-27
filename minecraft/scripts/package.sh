#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$ROOT/scripts/build-env.sh"
output="${1:-$ROOT/dist}"
cd "$ROOT"
stage="$ROOT/minecraft/build/generated/resources/hole"
mkdir -p "$stage/native"
version=$(python3 scripts/build_meta.py version)
build_id=$(python3 scripts/build_meta.py build-id)
for target in linux-amd64 linux-arm64 windows-amd64; do
    os=${target%-*}; arch=${target#*-}; file=hole-core
    [[ "$os" != windows ]] || file+=.exe
    mkdir -p "$stage/native/$target"
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -mod=readonly -trimpath -buildvcs=false \
        -ldflags="-s -w -X hole/core.CoreVersion=$version -X hole/core.CoreBuildID=$build_id" \
        -o "$stage/native/$target/$file" ./cmd/hole-core
done
hole_load_android_toolchain
export JAVA_HOME="${HOLE_MINECRAFT_JAVA_HOME:-${JDK17_HOME:-/usr/lib/jvm/java-17-openjdk-amd64}}"
export PATH="$JAVA_HOME/bin:$PATH"
ndk="${ANDROID_HOME:?需要 Android SDK}/ndk/28.2.13676358/toolchains/llvm/prebuilt/linux-x86_64"
[[ -x "$ndk/bin/aarch64-linux-android26-clang" ]] || { echo 'FCL 原生库构建需要 Linux x64 宿主及 NDK 28.2.13676358' >&2; exit 1; }
mkdir -p "$stage/native/android-arm64" "$stage/android" "$ROOT/minecraft/build/android-classes"
GOOS=android GOARCH=arm64 CGO_ENABLED=1 CC="$ndk/bin/aarch64-linux-android26-clang" \
    go build -mod=readonly -tags minecraftjni -trimpath -buildvcs=false -buildmode=c-shared \
    -ldflags="-s -w -X hole/core.CoreVersion=$version -X hole/core.CoreBuildID=$build_id -extldflags=-Wl,-z,max-page-size=16384" \
    -o "$stage/native/android-arm64/libholemc.so" ./minecraft/native
rm -f "$stage/native/android-arm64/libholemc.h"
"$JAVA_HOME/bin/javac" --release 17 -encoding UTF-8 -classpath "$ANDROID_HOME/platforms/android-37.0/android.jar" \
    -d "$ROOT/minecraft/build/android-classes" "$ROOT/minecraft/android/src/dev/hole/forge/android/"*.java
"$ANDROID_HOME/build-tools/36.0.0/d8" --release --min-api 26 --lib "$ANDROID_HOME/platforms/android-37.0/android.jar" \
    --output "$stage/android" "$ROOT/minecraft/build/android-classes/dev/hole/forge/android/"*.class
mv "$stage/android/classes.dex" "$stage/android/network.dex"
python3 "$ROOT/minecraft/scripts/checksums.py" "$stage/native"
bash "$ROOT/minecraft/scripts/gradle.sh" build
mkdir -p "$output/minecraft"
mod_version=$(python3 -c 'import sys; sys.path.insert(0,"scripts"); from build_meta import frontend_version; print(frontend_version("minecraft"))')
jar="$ROOT/minecraft/build/libs/hole-forge-1.20.1-$mod_version.jar"
cp "$jar" "$output/minecraft/"
python3 scripts/build_meta.py checksum "$output/minecraft/$(basename "$jar")"
printf 'Forge JAR: %s/minecraft/%s\n' "$output" "$(basename "$jar")"
