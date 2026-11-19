#!/usr/bin/env bash
# Explicit, optional tool preparation; normal builds never download these tools.
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
if [[ $# == 1 && "$1" == --help ]]; then
  echo '用法：bash desktop/scripts/setup-appimage.sh'
  echo '在 Linux 下载固定版本的本机 AppImage 工具到 .cache/，供本地和 CI 共用。'
  exit 0
fi
[[ $# == 0 ]] || { echo '仅支持 --help 或无参数运行' >&2; exit 1; }
[[ "$(uname -s)" == Linux ]] || { echo 'AppImage 工具准备仅支持 Linux' >&2; exit 1; }
source "$ROOT/scripts/build-env.sh"
source "$ROOT/desktop/scripts/toolchain.sh"
arch=$(hole_desktop_appimage_arch "$(uname -m)")
tools_dir=$(hole_desktop_appimage_tools_dir "$arch")
if [[ -x "$tools_dir/squashfs-root/AppRun" && -s "$tools_dir/runtime" ]]; then
  printf 'AppImage 工具已就绪：%s\n' "$tools_dir"
  exit 0
fi
[[ "${HOLE_BUILD_OFFLINE:-0}" != 1 ]] || { echo '离线模式缺少 AppImage 工具缓存' >&2; exit 1; }
command -v curl >/dev/null || { echo '准备 AppImage 工具需要 curl' >&2; exit 1; }
mkdir -p "$(dirname "$tools_dir")"
staging=$(mktemp -d "$(dirname "$tools_dir")/.prepare.XXXXXX")
trap 'rm -rf -- "$staging"' EXIT
curl --fail --location --retry 3 \
  "https://github.com/AppImage/appimagetool/releases/download/$HOLE_DESKTOP_APPIMAGETOOL_VERSION/appimagetool-$arch.AppImage" \
  --output "$staging/tool.AppImage"
curl --fail --location --retry 3 \
  "https://github.com/AppImage/type2-runtime/releases/download/$HOLE_DESKTOP_APPIMAGE_RUNTIME_VERSION/runtime-$arch" \
  --output "$staging/runtime"
chmod +x "$staging/tool.AppImage"
(cd "$staging" && ./tool.AppImage --appimage-extract >/dev/null)
[[ -x "$staging/squashfs-root/AppRun" && -s "$staging/runtime" ]] || {
  echo '下载的 AppImage 工具不完整' >&2; exit 1;
}
rm "$staging/tool.AppImage"
# Replace only this versioned tool cache, after all downloads have succeeded.
rm -rf -- "$tools_dir"
mv "$staging" "$tools_dir"
printf 'AppImage 工具已就绪：%s\n下一步：bash build.sh desktop\n' "$tools_dir"
