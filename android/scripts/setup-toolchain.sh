#!/usr/bin/env bash
# Linux x86_64。仅在用户执行本脚本时安装；不使用 sudo、不修改 shell 配置。
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)

usage() {
  cat <<'EOF'
用法：bash android/scripts/setup-toolchain.sh [--accept-licenses]

--accept-licenses  自动接受 Android SDK/NDK 许可；省略时交互确认。
安装目录：${TOOLCHAIN_DIR:-$HOME/.local/share/hole-android}
可选环境变量：JDK17_HOME、MOBILE_VERSION、GOPROXY。
默认锁定工程使用的 x/mobile 版本；gomobile/gobind 使用同一版本。
需要现有 Go 1.26.4、JDK 17、curl、unzip、python3、sha256sum、flock。
构建缓存写入仓库 .cache/；工具安装位置不变。
EOF
}

AUTO_LICENSES=0
for arg in "$@"; do
  case "$arg" in
    --accept-licenses) AUTO_LICENSES=1 ;;
    -h|--help) usage; exit 0 ;;
    *) printf '未知参数：%s\n' "$arg" >&2; usage; exit 2 ;;
  esac
done

die() { printf '\n失败：%s\n' "$*" >&2; exit 1; }
[[ "$(uname -s)/$(uname -m)" == Linux/x86_64 ]] || die '本脚本针对 Linux x86_64。'
for tool in go curl unzip python3 sha256sum flock yes; do
  command -v "$tool" >/dev/null || die "请先安装 $tool。"
done
export GOTOOLCHAIN=local
GO_BIN=$(command -v go)
GO_VERSION=$("$GO_BIN" version)
[[ "$GO_VERSION" == 'go version go1.26.4 '* ]] || die "需要 Go 1.26.4，当前：$GO_VERSION"

JAVA_FOUND=''
for candidate in "${JDK17_HOME:-}" "${JAVA_HOME:-}" /usr/lib/jvm/java-17-openjdk-amd64; do
  if [[ -n "$candidate" && -x "$candidate/bin/javac" ]] &&
     [[ "$("$candidate/bin/javac" -version 2>&1)" == 'javac 17.'* ]]; then
    JAVA_FOUND=$candidate
    break
  fi
done
[[ -n "$JAVA_FOUND" ]] || die '请安装 JDK 17，或用 JDK17_HOME 指定已有目录。'
export JAVA_HOME="$JAVA_FOUND"

PREFIX=${TOOLCHAIN_DIR:-$HOME/.local/share/hole-android}
PREFIX=$(python3 -c 'import os,sys; print(os.path.abspath(os.path.expanduser(sys.argv[1])))' "$PREFIX")
[[ "$PREFIX" != / && "$PREFIX" != "$HOME" ]] || die '请使用专用工具链目录。'
mkdir -p "$PREFIX"
exec 9>"$PREFIX/.setup.lock"
flock -n 9 || die '该目录已有安装任务运行。'
source "$ROOT/scripts/build-env.sh"
WORK=$(mktemp -d "$HOLE_CACHE_ROOT/tmp/setup.XXXXXXXX")
trap 'rm -rf -- "$WORK"' EXIT

GRADLE_VERSION=9.4.1
SDK_API=37.0
BUILD_TOOLS_VERSION=36.0.0
NDK_VERSION=28.2.13676358
CMDLINE_VERSION=19.0
export ANDROID_HOME="$PREFIX/android-sdk"
export ANDROID_SDK_ROOT="$ANDROID_HOME"
export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/$NDK_VERSION"
export ANDROID_NDK_ROOT="$ANDROID_NDK_HOME"
export GRADLE_HOME="$PREFIX/gradle-$GRADLE_VERSION"
export GOBIN="$PREFIX/bin"
# 工具依赖只写入独立模块，不修改项目 go.mod，也不自动下载其他 Go 版本。
export GOWORK=off GO111MODULE=on
PATH_PREFIX="$GOBIN:$GRADLE_HOME/bin:$ANDROID_HOME/cmdline-tools/$CMDLINE_VERSION/bin:$ANDROID_HOME/platform-tools:$JAVA_HOME/bin:$(dirname "$GO_BIN")"
export PATH="$PATH_PREFIX:$PATH"
mkdir -p "$GOBIN" "$ANDROID_HOME/cmdline-tools" "$HOLE_CACHE_ROOT/mobile-tools"

download() {
  curl --fail --location --proto '=https' --proto-redir '=https' \
    --retry 3 --connect-timeout 30 --output "$2.part" "$1"
  mv -- "$2.part" "$2"
}

printf '\n[1/4] Android Command-line Tools %s\n' "$CMDLINE_VERSION"
SDKMANAGER="$ANDROID_HOME/cmdline-tools/$CMDLINE_VERSION/bin/sdkmanager"
if [[ ! -x "$SDKMANAGER" ]]; then
  download https://dl.google.com/android/repository/repository2-1.xml "$WORK/repository.xml"
  # 从官方元数据读取固定版本的 Linux 下载地址与校验和。
  python3 - "$WORK/repository.xml" "$CMDLINE_VERSION" > "$WORK/archive.txt" <<'PY'
import re
import sys
import xml.etree.ElementTree as ET

root = ET.parse(sys.argv[1]).getroot()
for element in root.iter():
    element.tag = element.tag.rsplit('}', 1)[-1]
package = next((p for p in root.iter('remotePackage')
                if p.get('path') == 'cmdline-tools;' + sys.argv[2]), None)
if package is None:
    sys.exit('官方元数据中未找到指定的 Command-line Tools 版本。')
for archive in package.findall('./archives/archive'):
    if archive.findtext('host-os') != 'linux':
        continue
    complete = archive.find('complete')
    name = complete.findtext('url', '').strip()
    checksum = complete.find('checksum')
    if not re.fullmatch(r'commandlinetools-linux-[0-9]+_latest\.zip', name):
        sys.exit('下载文件名与预期不符。')
    digest = (checksum.text or '').strip().lower()
    algorithm = checksum.get('type', 'sha1').lower().replace('-', '')
    if algorithm not in ('sha1', 'sha256') or not re.fullmatch(
            r'[0-9a-f]{%d}' % (40 if algorithm == 'sha1' else 64), digest):
        sys.exit('官方校验和格式与预期不符。')
    print('https://dl.google.com/android/repository/' + name)
    print(algorithm)
    print(digest)
    break
else:
    sys.exit('未找到 Linux 下载包。')
PY
  mapfile -t ARCHIVE < "$WORK/archive.txt"
  download "${ARCHIVE[0]}" "$WORK/cmdline.zip"
  python3 - "$WORK/cmdline.zip" "${ARCHIVE[1]}" "${ARCHIVE[2]}" <<'PY'
import hashlib
import sys
digest = hashlib.new(sys.argv[2])
with open(sys.argv[1], 'rb') as source:
    for block in iter(lambda: source.read(1024 * 1024), b''):
        digest.update(block)
if digest.hexdigest() != sys.argv[3]:
    sys.exit('Command-line Tools 校验失败。')
PY
  unzip -q "$WORK/cmdline.zip" -d "$WORK/sdk"
  [[ ! -e "$ANDROID_HOME/cmdline-tools/$CMDLINE_VERSION" ]] || die '目标目录已存在但缺少 sdkmanager，请检查后重试。'
  mv "$WORK/sdk/cmdline-tools" "$ANDROID_HOME/cmdline-tools/$CMDLINE_VERSION"
fi

printf '\n[2/4] SDK %s / Build-Tools %s / NDK %s\n' "$SDK_API" "$BUILD_TOOLS_VERSION" "$NDK_VERSION"
if (( AUTO_LICENSES )); then
  # yes 在 sdkmanager 结束后可能以 SIGPIPE 退出；仅检查 sdkmanager 的状态。
  yes | "$SDKMANAGER" --sdk_root="$ANDROID_HOME" --licenses || {
    (( ${PIPESTATUS[1]} == 0 )) || die 'Android 许可确认失败。'
  }
else
  "$SDKMANAGER" --sdk_root="$ANDROID_HOME" --licenses
fi
"$SDKMANAGER" --sdk_root="$ANDROID_HOME" --install \
  'platform-tools' "platforms;android-$SDK_API" \
  "build-tools;$BUILD_TOOLS_VERSION" "ndk;$NDK_VERSION"

printf '\n[3/4] Gradle %s\n' "$GRADLE_VERSION"
if [[ ! -x "$GRADLE_HOME/bin/gradle" ]]; then
  URL="https://services.gradle.org/distributions/gradle-$GRADLE_VERSION-bin.zip"
  download "$URL.sha256" "$WORK/gradle.sha256"
  HASH=$(tr -d '[:space:]' < "$WORK/gradle.sha256")
  [[ "$HASH" =~ ^[[:xdigit:]]{64}$ ]] || die 'Gradle SHA-256 格式错误。'
  download "$URL" "$WORK/gradle.zip"
  printf '%s  %s\n' "$HASH" "$WORK/gradle.zip" | sha256sum --check --status
  unzip -q "$WORK/gradle.zip" -d "$WORK/gradle"
  [[ ! -e "$GRADLE_HOME" ]] || die 'Gradle 目标目录已存在但不完整，请检查后重试。'
  mv "$WORK/gradle/gradle-$GRADLE_VERSION" "$GRADLE_HOME"
fi

printf '\n[4/4] gomobile / gobind\n'
cd "$HOLE_CACHE_ROOT/mobile-tools"
[[ -f go.mod ]] || "$GO_BIN" mod init local/hole-android-toolchain
if [[ -z "${MOBILE_VERSION:-}" && -s "$PREFIX/x-mobile.version" ]]; then
  MOBILE_VERSION=$(cat "$PREFIX/x-mobile.version")
fi
MOBILE_VERSION=$("$GO_BIN" list -m -f '{{.Version}}' "golang.org/x/mobile@${MOBILE_VERSION:-v0.0.0-20260908204917-8b95e45f8d3e}")
[[ "$MOBILE_VERSION" == v* ]] || die 'x/mobile 版本解析失败。'
printf '%s\n' "$MOBILE_VERSION" > "$PREFIX/x-mobile.version"
"$GO_BIN" get "golang.org/x/mobile/cmd/gomobile@$MOBILE_VERSION" "golang.org/x/mobile/cmd/gobind@$MOBILE_VERSION"
"$GO_BIN" install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
"$GOBIN/gomobile" init

[[ -f "$ANDROID_HOME/platforms/android-$SDK_API/android.jar" ]] || die 'SDK Platform 检查失败。'
[[ -x "$ANDROID_HOME/build-tools/$BUILD_TOOLS_VERSION/aapt2" ]] || die 'Build-Tools 检查失败。'
for compiler in armv7a-linux-androideabi26-clang aarch64-linux-android26-clang; do
  [[ -x "$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin/$compiler" ]] || die "NDK $compiler 检查失败。"
done
for binary in gomobile gobind; do
  INFO=$("$GO_BIN" version -m "$GOBIN/$binary")
  [[ "$INFO" == *$'\tmod\tgolang.org/x/mobile\t'"$MOBILE_VERSION"$'\t'* ]] || die "$binary 版本检查失败。"
done
"$SDKMANAGER" --version
"$GRADLE_HOME/bin/gradle" --offline --no-daemon --version
"$GOBIN/gomobile" version

{
  printf '# 由 setup-toolchain.sh 生成；供 bash / zsh source 使用。\n'
  for name in JAVA_HOME ANDROID_HOME ANDROID_SDK_ROOT ANDROID_NDK_HOME ANDROID_NDK_ROOT GRADLE_HOME GOBIN GOTOOLCHAIN; do
    printf 'export %s=%q\n' "$name" "${!name}"
  done
  printf 'export PATH=%q:"$PATH"\n' "$PATH_PREFIX"
} > "$WORK/env.sh"
mv "$WORK/env.sh" "$PREFIX/env.sh"
printf '\n工具链已就绪。当前终端加载环境：\nsource %q\n' "$PREFIX/env.sh"
printf 'Go：%s\nx/mobile：%s\n环境文件：%s/env.sh\n' "$GO_VERSION" "$MOBILE_VERSION" "$PREFIX"
