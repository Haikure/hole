#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$ROOT/scripts/build-env.sh"
cd "$ROOT"
source "$ROOT/desktop/scripts/toolchain.sh"
hole_require_desktop_go
output=${1:-$ROOT/dist}
target_os=${2:-$(go env GOHOSTOS)}
target_arch=${3:-$(go env GOHOSTARCH)}
[[ "$output" == /* || "$output" == [A-Za-z]:* ]] || output="$ROOT/$output"
[[ "$target_os/$target_arch" == "$(go env GOHOSTOS)/$(go env GOHOSTARCH)" ]] || {
  echo 'Kotlin 桌面安装包必须在目标系统及架构上构建；旧 Rust 交叉构建请使用 desktop-rust。' >&2; exit 1;
}
case "$target_os/$target_arch" in
  linux/amd64|linux/arm64|windows/amd64|windows/arm64) ;;
  *) echo "不支持桌面目标：$target_os/$target_arch" >&2; exit 1 ;;
esac
hole_load_desktop_toolchain
printf '[Desktop] JDK: %s\n' "$JAVA_HOME"
destination="$output/desktop/$target_os-$target_arch"
mkdir -p "$destination"
version=$(python3 -c 'import json; print(json.load(open("versions.json"))["frontends"]["desktop_gui"]["version"])')
if [[ "$target_os" == windows ]]; then
  for tool in candle.exe light.exe; do
    command -v "$tool" >/dev/null || { echo "缺少 WiX 3 工具 $tool；请将 WiX bin 目录加入 PATH" >&2; exit 1; }
  done
  bash "$ROOT/desktop/scripts/gradle.sh" packageReleaseExe
  installer="$ROOT/desktop/compose/build/compose/binaries/main-release/exe/Hole-$version.exe"
  [[ -f "$installer" ]] || { echo "未找到 Windows Release 安装包：$installer" >&2; exit 1; }
  cp "$installer" "$destination/"
  python3 scripts/build_meta.py checksum "$destination/$(basename "$installer")"
else
  hole_load_desktop_appimage_tools "$target_arch"
  bash "$ROOT/desktop/scripts/gradle.sh" createReleaseDistributable
  appdir="$HOLE_CACHE_ROOT/desktop-appimage/$target_arch/Hole.AppDir"
  mkdir -p "$(dirname "$appdir")"
  # Only replace this dedicated generated directory, never a published package.
  rm -rf -- "$appdir"
  mkdir -p "$appdir/usr"
  cp -a "$ROOT/desktop/compose/build/compose/binaries/main-release/app/Hole" "$appdir/usr/"
  cp "$ROOT/desktop/packaging/AppRun" "$appdir/AppRun"
  cp "$ROOT/desktop/packaging/hole.desktop" "$appdir/hole.desktop"
  cp "$ROOT/desktop/gui/ui/assets/app-icon.png" "$appdir/hole.png"
  chmod +x "$appdir/AppRun"
  appimage_arch=$(hole_desktop_appimage_arch "$target_arch")
  package="$destination/Hole-$version-$appimage_arch.AppImage"
  # Supply a local runtime to prevent appimagetool from downloading one implicitly.
  ARCH="$appimage_arch" "$HOLE_APPIMAGETOOL" --runtime-file "$HOLE_APPIMAGE_RUNTIME" "$appdir" "$package"
  python3 scripts/build_meta.py checksum "$package"
fi
printf 'Desktop: %s\n' "$destination"
