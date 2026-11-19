#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$ROOT/scripts/build-env.sh"
cd "$ROOT"
command -v go >/dev/null || { echo '请先安装 Go 1.27.4' >&2; exit 1; }
[[ "$(go env GOVERSION)" == go1.27.4 ]] || { echo '需要 Go 1.27.4' >&2; exit 1; }
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
source "$ROOT/desktop/scripts/toolchain.sh"
hole_load_desktop_toolchain
if [[ "$target_os" == windows ]]; then
  # A Java ProcessBuilder lookup of "bash" can select Windows' WSL launcher.
  export HOLE_DESKTOP_BASH=$(cygpath -m "$BASH")
fi
printf '[Desktop] JDK: %s\n' "$JAVA_HOME"
args=(--no-daemon --console=plain -p "$ROOT/desktop/compose"
  --project-cache-dir "$HOLE_CACHE_ROOT/gradle-project-desktop"
  "-Pkotlin.project.persistent.dir=$HOLE_CACHE_ROOT/kotlin-desktop" "-Djava.io.tmpdir=$TMPDIR")
if [[ "${HOLE_BUILD_OFFLINE:-0}" == 1 ]]; then args+=(--offline); fi
destination="$output/desktop/$target_os-$target_arch"
mkdir -p "$destination"
if [[ "$target_os" == windows ]]; then
  bash "$ROOT/android/gradlew" "${args[@]}" packageReleaseExe
  version=$(python3 -c 'import json; print(json.load(open("versions.json"))["frontends"]["desktop_gui"]["version"])')
  installer="$ROOT/desktop/compose/build/compose/binaries/main-release/exe/Hole-$version.exe"
  [[ -f "$installer" ]] || { echo "未找到 Windows Release 安装包：$installer" >&2; exit 1; }
  cp "$installer" "$destination/"
  python3 scripts/build_meta.py checksum "$destination/$(basename "$installer")"
else
  command -v appimagetool >/dev/null || { echo '请先安装 appimagetool；构建不会自动下载安装。' >&2; exit 1; }
  : "${HOLE_APPIMAGE_RUNTIME:?请设置本架构本地 type2-runtime 的绝对路径}"
  [[ "$HOLE_APPIMAGE_RUNTIME" == /* && -f "$HOLE_APPIMAGE_RUNTIME" ]] || { echo 'HOLE_APPIMAGE_RUNTIME 需要现有文件的绝对路径' >&2; exit 1; }
  bash "$ROOT/android/gradlew" "${args[@]}" createReleaseDistributable
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
  version=$(python3 -c 'import json; print(json.load(open("versions.json"))["frontends"]["desktop_gui"]["version"])')
  case "$target_arch" in amd64) appimage_arch=x86_64 ;; arm64) appimage_arch=aarch64 ;; esac
  package="$destination/Hole-$version-$appimage_arch.AppImage"
  # Supply a local runtime to prevent appimagetool from downloading one implicitly.
  ARCH="$appimage_arch" appimagetool --runtime-file "$HOLE_APPIMAGE_RUNTIME" "$appdir" "$package"
  python3 scripts/build_meta.py checksum "$package"
fi
printf 'Desktop: %s\n' "$destination"
