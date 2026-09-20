#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
HERE=$ROOT/penmods
OUT=${OUT:-$ROOT/dist/penmods/hole_plugin}
QT=${QT:-/home/haiku/program/qt}
CROSS=${CROSS:-aarch64-linux-gnu.2.27}

mkdir -p "$OUT"

source "$ROOT/scripts/build-env.sh"

(
    cd "$ROOT"
    GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build \
        -trimpath -ldflags='-s -w' \
        -o "$HERE/build/hole-desktop-core" \
        ./cmd/hole-desktop-core
)

(
    cd "$HERE"
    xmake f --qt="$QT" --arch=arm64-v8a --toolchain=zigcc --cross="$CROSS" -m release -vD
    xmake build hole_plugin
)

PLUGIN_SO=$(find "$HERE/build" -type f -name 'libhole_plugin.so' -path '*/release/*' | head -n 1)
if [[ -z "$PLUGIN_SO" ]]; then
    echo 'libhole_plugin.so was not produced' >&2
    exit 1
fi

rm -rf "$OUT"
mkdir -p "$OUT"
cp "$PLUGIN_SO" "$OUT/libhole_plugin.so"
cp "$HERE/build/hole-desktop-core" "$OUT/hole-desktop-core"
cp "$HERE/metadata.json" "$OUT/metadata.json"
cp "$HERE/icon.png" "$OUT/icon.png"
cp "$HERE/Main.qml" "$OUT/Main.qml"
cp "$HERE/SettingsPage.qml" "$OUT/SettingsPage.qml"
cp "$HERE/ConfigPage.qml" "$OUT/ConfigPage.qml"
rm -rf "$OUT/components"
cp -R "$HERE/components" "$OUT/components"
chmod 0755 "$OUT/libhole_plugin.so" "$OUT/hole-desktop-core"

printf 'Plugin package: %s\n' "$OUT"
