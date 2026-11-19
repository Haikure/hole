#!/usr/bin/env bash
# Sourced only by desktop build entry points; never imports the Android env.
hole_load_desktop_toolchain() {
  local desktop_java_bin
  : "${HOLE_ROOT:?请先加载 scripts/build-env.sh}"
  JAVA_HOME=$(python3 "$HOLE_ROOT/desktop/scripts/toolchain.py") || return 1
  export JAVA_HOME
  desktop_java_bin="$JAVA_HOME/bin"
  if command -v cygpath >/dev/null 2>&1; then
    desktop_java_bin=$(cygpath -u "$desktop_java_bin")
  fi
  export PATH="$desktop_java_bin:$PATH"
  hole_use_project_cache
}
