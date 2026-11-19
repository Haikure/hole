#!/usr/bin/env bash
# Sourced only by desktop build entry points; never imports the Android env.
HOLE_DESKTOP_APPIMAGETOOL_VERSION=1.9.1
HOLE_DESKTOP_APPIMAGE_RUNTIME_VERSION=20251108

hole_require_desktop_go() {
  command -v go >/dev/null || { echo '请先安装 Go 1.27.4' >&2; return 1; }
  [[ "$(go env GOVERSION)" == go1.27.4 ]] || { echo '需要 Go 1.27.4' >&2; return 1; }
}

hole_load_desktop_toolchain() {
  local desktop_java_bin
  : "${HOLE_ROOT:?请先加载 scripts/build-env.sh}"
  JAVA_HOME=$(python3 "$HOLE_ROOT/desktop/scripts/toolchain.py") || return 1
  export JAVA_HOME
  desktop_java_bin="$JAVA_HOME/bin"
  if command -v cygpath >/dev/null 2>&1; then
    desktop_java_bin=$(cygpath -u "$desktop_java_bin")
    # Java's lookup of "bash" on Windows can select the WSL launcher.
    if [[ -z "${HOLE_DESKTOP_BASH:-}" ]]; then
      HOLE_DESKTOP_BASH=$(cygpath -m "${BASH:-$(command -v bash)}")
    fi
    export HOLE_DESKTOP_BASH
  fi
  export PATH="$desktop_java_bin:$PATH"
  hole_use_project_cache
}

hole_desktop_appimage_arch() {
  case "$1" in
    amd64|x86_64) printf 'x86_64\n' ;;
    arm64|aarch64) printf 'aarch64\n' ;;
    *) printf '不支持 AppImage 架构：%s\n' "$1" >&2; return 1 ;;
  esac
}

hole_desktop_appimage_tools_dir() {
  local arch
  arch=$(hole_desktop_appimage_arch "$1") || return 1
  printf '%s/desktop-tools/appimage/%s/appimagetool-%s-runtime-%s\n' \
    "$HOLE_CACHE_ROOT" "$arch" "$HOLE_DESKTOP_APPIMAGETOOL_VERSION" "$HOLE_DESKTOP_APPIMAGE_RUNTIME_VERSION"
}

hole_load_desktop_appimage_tools() {
  local tools_dir
  tools_dir=$(hole_desktop_appimage_tools_dir "$1") || return 1
  if [[ -n "${HOLE_APPIMAGETOOL:-}" ]]; then
    HOLE_APPIMAGETOOL=$(command -v "$HOLE_APPIMAGETOOL") || {
      echo 'HOLE_APPIMAGETOOL 必须指向可执行的 appimagetool' >&2; return 1;
    }
  elif command -v appimagetool >/dev/null 2>&1; then
    HOLE_APPIMAGETOOL=$(command -v appimagetool)
  elif [[ -x "$tools_dir/squashfs-root/AppRun" ]]; then
    HOLE_APPIMAGETOOL="$tools_dir/squashfs-root/AppRun"
  else
    echo '缺少 appimagetool；请安装到 PATH，或先执行 bash desktop/scripts/setup-appimage.sh' >&2
    return 1
  fi
  HOLE_APPIMAGE_RUNTIME="${HOLE_APPIMAGE_RUNTIME:-$tools_dir/runtime}"
  [[ "$HOLE_APPIMAGE_RUNTIME" == /* && -s "$HOLE_APPIMAGE_RUNTIME" ]] || {
    echo '缺少本架构的 AppImage runtime；请设置 HOLE_APPIMAGE_RUNTIME 绝对路径，或先执行 bash desktop/scripts/setup-appimage.sh' >&2
    return 1
  }
  export HOLE_APPIMAGETOOL HOLE_APPIMAGE_RUNTIME
}
