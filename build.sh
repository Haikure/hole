#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

usage() {
  cat <<'EOF'
用法：./build.sh <cli|desktop-core|desktop|android|wear|all> [更多目标] [选项]

目标（可组合，重复目标只构建一次）：
  cli                Go CLI，CGO_ENABLED=0、trimpath、-s -w
  desktop-core       桌面 GUI 的独立 Go stdio 桥接，Release；不需要 Qt
  desktop            桌面 GUI + Go 桥接 Release 目录包，需要 Rust stable / Cargo
  android / phone    Android 手机 Release，ARM64、R8、资源收缩、native strip
  wear               Wear OS Release，ARM32 + ARM64，同上
  all                cli + android + wear（不包含 desktop-core 或 desktop）

选项：
  -t, --target NAME      与位置参数相同，可重复使用
  --os GOOS             CLI / 桌面目标系统（默认 GOOS 或宿主系统）
  --arch GOARCH         CLI / 桌面目标架构（默认 GOARCH 或宿主架构）
  -o, --output DIR      交付目录（默认仓库 dist/）
  --signing-config FILE 签名环境文件（默认仓库 signing.env）
  --offline            仅使用已有 Go / Cargo / Gradle 依赖缓存
  -h, --help           显示帮助

Android 签名使用四个环境变量；已设置的非空变量优先于签名文件：
  HOLE_SIGNING_STORE_FILE、HOLE_SIGNING_STORE_PASSWORD
  HOLE_SIGNING_KEY_ALIAS、HOLE_SIGNING_KEY_PASSWORD
密钥库相对路径以仓库根目录为基准。脚本不生成或替换签名密钥。
所有构建缓存固定在仓库 .cache/；SDK、JDK 和签名密钥保留原位置。

示例：
  ./build.sh cli --os linux --arch arm64
  ./build.sh desktop --os windows --arch amd64
  ./build.sh android wear --signing-config signing.env
  ./build.sh all --offline
EOF
}

die() { printf '构建失败：%s\n' "$*" >&2; exit 1; }
need_value() { [[ $# -ge 2 && -n "$2" && "$2" != --* ]] || die "$1 需要参数"; }
BUILD_CLI=0 BUILD_DESKTOP_CORE=0 BUILD_DESKTOP=0 BUILD_ANDROID=0 BUILD_WEAR=0
CLI_OS="${GOOS:-}" CLI_ARCH="${GOARCH:-}"
OUTPUT="$ROOT/dist"
SIGNING_CONFIG="$ROOT/signing.env"
EXPLICIT_SIGNING_CONFIG=0 OFFLINE=0

add_target() {
  case "$1" in
    cli) BUILD_CLI=1 ;;
    desktop-core) BUILD_DESKTOP_CORE=1 ;;
    desktop) BUILD_DESKTOP=1; BUILD_DESKTOP_CORE=1 ;;
    android|phone) BUILD_ANDROID=1 ;;
    wear) BUILD_WEAR=1 ;;
    all) BUILD_CLI=1; BUILD_ANDROID=1; BUILD_WEAR=1 ;;
    *) die "未知目标：$1（可选 cli、desktop-core、desktop、android、wear、all）" ;;
  esac
}

if [[ $# -eq 0 ]]; then usage; exit 0; fi
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    -t|--target) need_value "$@"; add_target "$2"; shift 2 ;;
    --os) need_value "$@"; CLI_OS="$2"; shift 2 ;;
    --arch) need_value "$@"; CLI_ARCH="$2"; shift 2 ;;
    -o|--output) need_value "$@"; OUTPUT="$2"; shift 2 ;;
    --signing-config) need_value "$@"; SIGNING_CONFIG="$2"; EXPLICIT_SIGNING_CONFIG=1; shift 2 ;;
    --offline) OFFLINE=1; shift ;;
    -*) die "未知选项：$1" ;;
    *) add_target "$1"; shift ;;
  esac
done
(( BUILD_CLI || BUILD_DESKTOP_CORE || BUILD_DESKTOP || BUILD_ANDROID || BUILD_WEAR )) || die '请选择至少一个构建目标'
command -v python3 >/dev/null || die '请安装 Python 3'
OUTPUT=$(python3 -c 'from pathlib import Path; import sys; print(Path(sys.argv[1]).expanduser().resolve())' "$OUTPUT")
if [[ -f "$SIGNING_CONFIG" ]]; then
  SIGNING_CONFIG=$(python3 -c 'from pathlib import Path; import sys; print(Path(sys.argv[1]).resolve())' "$SIGNING_CONFIG")
fi

# shellcheck source=scripts/build-env.sh
source "$ROOT/scripts/build-env.sh"

load_signing() {
  local name index
  local -a inherited_names=() inherited_values=()
  for name in HOLE_SIGNING_STORE_FILE HOLE_SIGNING_STORE_PASSWORD HOLE_SIGNING_KEY_ALIAS HOLE_SIGNING_KEY_PASSWORD; do
    if [[ -n "${!name:-}" ]]; then
      inherited_names+=("$name")
      inherited_values+=("${!name}")
    fi
  done
  if [[ -f "$SIGNING_CONFIG" ]]; then
    # The config is a local Bash env file, not an APK or an untrusted download.
    source "$SIGNING_CONFIG"
  elif (( EXPLICIT_SIGNING_CONFIG )); then
    die "签名配置文件不存在：$SIGNING_CONFIG"
  fi
  for (( index=0; index<${#inherited_names[@]}; index++ )); do
    export "${inherited_names[index]}=${inherited_values[index]}"
  done
  for name in HOLE_SIGNING_STORE_FILE HOLE_SIGNING_STORE_PASSWORD HOLE_SIGNING_KEY_ALIAS HOLE_SIGNING_KEY_PASSWORD; do
    [[ -n "${!name:-}" ]] || die "缺少签名设置 $name；参考 signing.env.example"
    export "$name"
  done
  HOLE_SIGNING_STORE_FILE=$(python3 -c \
    'from pathlib import Path; import sys; p=Path(sys.argv[2]).expanduser(); print((Path(sys.argv[1])/p).resolve())' \
    "$ROOT" "$HOLE_SIGNING_STORE_FILE")
  export HOLE_SIGNING_STORE_FILE
  [[ -f "$HOLE_SIGNING_STORE_FILE" ]] || die 'HOLE_SIGNING_STORE_FILE 指定的密钥库不存在'
}

if (( BUILD_ANDROID || BUILD_WEAR )); then
  hole_load_android_toolchain
  load_signing
  # Neither a legacy toolchain env nor a signing env can redirect build caches.
  hole_use_project_cache
  for tool in go java javac keytool gomobile gobind; do
    command -v "$tool" >/dev/null || die "缺少工具 $tool；参见 docs/BUILDING.md"
  done
  [[ "$(go env GOVERSION)" == go1.26.4 ]] || die 'Android 绑定锁定 Go 1.26.4'
  [[ "$(javac -version 2>&1)" == 'javac 17.'* ]] || die 'Android 绑定需要 JDK 17'
  [[ -n "${ANDROID_HOME:-}" ]] || die '请配置 ANDROID_HOME 或 HOLE_TOOLCHAIN_ENV'
  for sdk_file in platforms/android-37.0/android.jar ndk/28.2.13676358/source.properties \
      build-tools/36.0.0/apksigner build-tools/36.0.0/zipalign build-tools/36.0.0/aapt2; do
    [[ -f "$ANDROID_HOME/$sdk_file" ]] || die "缺少 Android SDK/NDK 文件：$sdk_file"
  done
  keytool -list -keystore "$HOLE_SIGNING_STORE_FILE" \
    -storepass:env HOLE_SIGNING_STORE_PASSWORD -alias "$HOLE_SIGNING_KEY_ALIAS" >/dev/null
fi
command -v go >/dev/null || die '请安装 Go 1.26+'
if (( BUILD_DESKTOP )); then
  command -v cargo >/dev/null || die '构建 desktop 需要 Rust stable 的 Cargo；参见 docs/BUILDING.md'
  command -v rustc >/dev/null || die '构建 desktop 需要 rustc；参见 docs/BUILDING.md'
fi
if (( OFFLINE )); then export HOLE_BUILD_OFFLINE=1 GOPROXY=off; fi

resolve_desktop_target() {
  DESKTOP_OS="${CLI_OS:-$(go env GOHOSTOS)}"
  DESKTOP_ARCH="${CLI_ARCH:-$(go env GOHOSTARCH)}"
  case "$DESKTOP_OS/$DESKTOP_ARCH" in
    linux/amd64|linux/arm64|windows/amd64|windows/arm64) ;;
    *) die "桌面 GUI 不支持目标 $DESKTOP_OS/$DESKTOP_ARCH（支持 Linux / Windows ARM64 / AMD64）" ;;
  esac
  DESKTOP_RUST_TARGET=""
  if [[ "$DESKTOP_OS/$DESKTOP_ARCH" != "$(go env GOHOSTOS)/$(go env GOHOSTARCH)" ]]; then
    case "$DESKTOP_OS/$DESKTOP_ARCH" in
      linux/amd64) DESKTOP_RUST_TARGET=x86_64-unknown-linux-gnu ;;
      linux/arm64) DESKTOP_RUST_TARGET=aarch64-unknown-linux-gnu ;;
      windows/amd64) DESKTOP_RUST_TARGET=x86_64-pc-windows-gnu ;;
      windows/arm64) DESKTOP_RUST_TARGET=aarch64-pc-windows-msvc ;;
    esac
  fi
}

if (( BUILD_DESKTOP )); then resolve_desktop_target; fi
mkdir -p "$OUTPUT"

build_cli() (
  cd "$ROOT"
  local_os="${CLI_OS:-$(go env GOHOSTOS)}"
  local_arch="${CLI_ARCH:-$(go env GOHOSTARCH)}"
  go tool dist list | grep -Fx "$local_os/$local_arch" >/dev/null || die "Go 不支持目标 $local_os/$local_arch"
  name="hole-$local_os-$local_arch"
  [[ "$local_os" != windows ]] || name+=.exe
  mkdir -p "$OUTPUT/cli"
  work=$(mktemp -d "$HOLE_CACHE_ROOT/tmp/cli.XXXXXXXX")
  trap 'rm -rf -- "$work"' EXIT
  version=$(python3 "$ROOT/scripts/build_meta.py" version)
  printf '\n[CLI] %s/%s · Release / stripped\n' "$local_os" "$local_arch"
  GOOS="$local_os" GOARCH="$local_arch" CGO_ENABLED=0 \
    go build -mod=readonly -trimpath -buildvcs=false \
    -ldflags="-s -w -X hole/core.CoreVersion=$version" -o "$work/$name" .
  mv "$work/$name" "$OUTPUT/cli/$name"
  python3 "$ROOT/scripts/build_meta.py" checksum "$OUTPUT/cli/$name"
  printf 'CLI: %s\n' "$OUTPUT/cli/$name"
)

if (( BUILD_CLI )); then build_cli; fi

# An opt-in host binary, deliberately excluded from cli/all and the AAR input
# graph. Keep the established CLI command and Android toolchain path unchanged.
build_desktop_core() (
  cd "$ROOT"
  local_os="${CLI_OS:-$(go env GOHOSTOS)}"
  local_arch="${CLI_ARCH:-$(go env GOHOSTARCH)}"
  go tool dist list | grep -Fx "$local_os/$local_arch" >/dev/null || die "Go 不支持目标 $local_os/$local_arch"
  name="hole-desktop-core-$local_os-$local_arch"
  [[ "$local_os" != windows ]] || name+=.exe
  mkdir -p "$OUTPUT/desktop-core"
  work=$(mktemp -d "$HOLE_CACHE_ROOT/tmp/desktop-core.XXXXXXXX")
  trap 'rm -rf -- "$work"' EXIT
  version=$(python3 "$ROOT/scripts/build_meta.py" version)
  printf '\n[Desktop Core] %s/%s · Release / stripped\n' "$local_os" "$local_arch"
  GOOS="$local_os" GOARCH="$local_arch" CGO_ENABLED=0 \
    go build -mod=readonly -trimpath -buildvcs=false \
    -ldflags="-s -w -X hole/core.CoreVersion=$version" -o "$work/$name" ./cmd/hole-desktop-core
  mv "$work/$name" "$OUTPUT/desktop-core/$name"
  python3 "$ROOT/scripts/build_meta.py" checksum "$OUTPUT/desktop-core/$name"
  printf 'Desktop Core: %s\n' "$OUTPUT/desktop-core/$name"
)

if (( BUILD_DESKTOP_CORE )); then build_desktop_core; fi

build_desktop_gui() (
  cd "$ROOT"
  local_os="$DESKTOP_OS"
  local_arch="$DESKTOP_ARCH"
  target_suffix=""
  [[ -z "$DESKTOP_RUST_TARGET" ]] || target_suffix="/$DESKTOP_RUST_TARGET"
  gui_name=hole-desktop
  bridge_name="hole-desktop-core-$local_os-$local_arch"
  if [[ "$local_os" == windows ]]; then
    gui_name+=.exe
    bridge_name+=.exe
  fi
  cargo_output="$ROOT/.cache/cargo-target$target_suffix/release"
  gui_binary="$cargo_output/$gui_name"
  [[ -f "$gui_binary" ]] || die "Cargo 未生成桌面程序：$gui_binary"
  bridge_binary="$OUTPUT/desktop-core/$bridge_name"
  [[ -f "$bridge_binary" ]] || die "未找到桌面桥接程序：$bridge_binary"

  package_parent="$OUTPUT/desktop"
  package="$package_parent/$local_os-$local_arch"
  mkdir -p "$package_parent"
  stage=$(mktemp -d "$package_parent/.$local_os-$local_arch.XXXXXXXX")
  backup=""
  cleanup_package() {
    if [[ -n "$backup" && ( -e "$backup" || -L "$backup" ) && ! -e "$package" && ! -L "$package" ]]; then
      mv "$backup" "$package" || true
    fi
    if [[ -n "$stage" && -d "$stage" ]]; then rm -rf -- "$stage"; fi
    if [[ -n "$backup" && ( -e "$backup" || -L "$backup" ) && ( -e "$package" || -L "$package" ) ]]; then
      rm -rf -- "$backup"
    fi
  }
  trap cleanup_package EXIT

  cp "$gui_binary" "$stage/$gui_name"
  cp "$bridge_binary" "$stage/$bridge_name"
  python3 "$ROOT/scripts/build_meta.py" checksum "$stage/$gui_name"
  python3 "$ROOT/scripts/build_meta.py" checksum "$stage/$bridge_name"
  if [[ -e "$package" || -L "$package" ]]; then
    backup=$(mktemp -d "$package_parent/.$local_os-$local_arch.backup.XXXXXXXX")
    rmdir "$backup"
    mv "$package" "$backup"
  fi
  mv "$stage" "$package"
  stage=""
  if [[ -n "$backup" ]]; then rm -rf -- "$backup"; backup=""; fi
  printf 'Desktop: %s\n' "$package"
)

if (( BUILD_DESKTOP )); then
  printf '\n[Desktop] %s/%s · GUI + Go bridge · Release\n' "$DESKTOP_OS" "$DESKTOP_ARCH"
  cargo_args=(build --release --locked)
  if [[ -n "$DESKTOP_RUST_TARGET" ]]; then cargo_args+=(--target "$DESKTOP_RUST_TARGET"); fi
  if (( OFFLINE )); then cargo_args+=(--offline); fi
  # Go cross-compilation settings select the target above; Cargo receives only its Rust target.
  unset GOOS GOARCH GOARM GOARM64 GOAMD64 GO386 CGO_ENABLED
  (cd "$ROOT/desktop/gui"; cargo "${cargo_args[@]}")
  build_desktop_gui
fi
if (( BUILD_ANDROID || BUILD_WEAR )); then
  # CLI cross-compilation settings must never leak into gomobile/Gradle.
  unset GOOS GOARCH GOARM GOARM64 GOAMD64 GO386 CGO_ENABLED
  gradle_args=(--no-daemon --console=plain --project-cache-dir "$HOLE_CACHE_ROOT/gradle-project"
    "-Pkotlin.project.persistent.dir=$HOLE_CACHE_ROOT/kotlin" "-Djava.io.tmpdir=$TMPDIR")
  if (( OFFLINE )); then gradle_args+=(--offline); fi
  tasks=()
  if (( BUILD_ANDROID )); then tasks+=(:app:assembleRelease); fi
  if (( BUILD_WEAR )); then tasks+=(:wear:assembleRelease); fi
  printf '\n[Android] Release / R8 / native strip\n'
  (cd "$ROOT/android"; ./gradlew "${gradle_args[@]}" "${tasks[@]}")
  if (( BUILD_ANDROID )); then
    python3 "$ROOT/android/scripts/verify-apk.py" "$ROOT/android/app/build/outputs/apk/release/app-release.apk" \
      --module app --variant release --output "$OUTPUT/android"
  fi
  if (( BUILD_WEAR )); then
    python3 "$ROOT/android/scripts/verify-apk.py" "$ROOT/android/wear/build/outputs/apk/release/wear-release.apk" \
      --module wear --variant release --output "$OUTPUT/wear"
  fi
fi
printf '\n构建完成：%s\n' "$OUTPUT"
