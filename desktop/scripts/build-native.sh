#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$ROOT/scripts/build-env.sh"
cd "$ROOT"
source "$ROOT/desktop/scripts/toolchain.sh"
hole_load_desktop_toolchain
host_os=$(go env GOHOSTOS)
host_arch=$(go env GOHOSTARCH)
case "$host_os" in
  linux) library=libholecore.so; jni_platform=linux ;;
  windows) library=holecore.dll; jni_platform=win32 ;;
  *) echo '桌面原生桥接仅支持 Windows / Linux' >&2; exit 1 ;;
esac
[[ "${GOOS:-$host_os}/${GOARCH:-$host_arch}" == "$host_os/$host_arch" ]] || {
  echo 'Compose 桌面需要在目标系统和架构上构建。' >&2; exit 1;
}
[[ -f "$JAVA_HOME/include/jni.h" ]] || { echo 'JAVA_HOME 中缺少 JNI 头文件' >&2; exit 1; }
[[ "$(go env GOVERSION)" == go1.27.4 ]] || { echo '需要 Go 1.27.4' >&2; exit 1; }
output="$ROOT/desktop/compose/build/native"
mkdir -p "$output"
# Go invokes the C compiler directly; use Windows paths rather than Git Bash /c paths.
jni_home="$JAVA_HOME"
if [[ "$host_os" == windows ]]; then jni_home=$(cygpath -m "$JAVA_HOME"); fi
export CGO_CFLAGS="${CGO_CFLAGS:-} -I\"$jni_home/include\" -I\"$jni_home/include/$jni_platform\""
version=$(python3 scripts/build_meta.py version)
build_id=$(python3 scripts/build_meta.py build-id)
CGO_ENABLED=1 go build -tags desktopjni -mod=readonly -trimpath -buildvcs=false -buildmode=c-shared \
  -ldflags="-s -w -X hole/core.CoreVersion=$version -X hole/core.CoreBuildID=$build_id" \
  -o "$output/$library" ./desktop/native
