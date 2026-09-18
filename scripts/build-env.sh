#!/usr/bin/env bash
# Source this file before invoking Go/Gradle tools directly.
if [[ -n "${BASH_VERSION:-}" ]]; then
  _hole_env_source="${BASH_SOURCE[0]}"
elif [[ -n "${ZSH_VERSION:-}" ]]; then
  _hole_env_source="${(%):-%x}"
else
  printf '请使用 Bash 或 Zsh 加载 scripts/build-env.sh\n' >&2
  return 1
fi
HOLE_ROOT=$(cd "$(dirname "$_hole_env_source")/.." && pwd)
unset _hole_env_source

hole_use_project_cache() {
  export HOLE_CACHE_ROOT="$HOLE_ROOT/.cache"
  export GOCACHE="$HOLE_CACHE_ROOT/go-build"
  export GOPATH="$HOLE_CACHE_ROOT/go"
  export GOMODCACHE="$GOPATH/pkg/mod"
  export GRADLE_USER_HOME="$HOLE_CACHE_ROOT/gradle"
  export XDG_CACHE_HOME="$HOLE_CACHE_ROOT/xdg"
  export TMPDIR="$HOLE_CACHE_ROOT/tmp"
  export GOTMPDIR="$TMPDIR"
  export GOTOOLCHAIN=local GOWORK=off
  export PYTHONPYCACHEPREFIX="$HOLE_CACHE_ROOT/python"
  if [[ "${HOLE_BUILD_OFFLINE:-0}" == 1 ]]; then export GOPROXY=off; fi
  mkdir -p "$GOCACHE" "$GOMODCACHE" "$GRADLE_USER_HOME" \
    "$XDG_CACHE_HOME" "$TMPDIR" "$HOLE_CACHE_ROOT/gradle-project" "$HOLE_CACHE_ROOT/kotlin"
}

hole_load_android_toolchain() {
  local env_file="${HOLE_TOOLCHAIN_ENV:-$HOME/.local/share/hole-android/env.sh}"
  if [[ -f "$env_file" ]]; then
    # Tool locations may come from an existing installation. Cache locations do not.
    source "$env_file"
  elif [[ -n "${HOLE_TOOLCHAIN_ENV:-}" ]]; then
    printf '工具链环境文件不存在：%s\n' "$env_file" >&2
    return 1
  fi
  if [[ -n "${JDK17_HOME:-}" ]]; then
    export JAVA_HOME="$JDK17_HOME"
    export PATH="$JAVA_HOME/bin:$PATH"
  fi
  hole_use_project_cache
}

hole_use_project_cache
