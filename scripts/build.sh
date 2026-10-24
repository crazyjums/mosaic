#!/usr/bin/env bash
#
# 桌面端一键打包脚本
#
# 默认行为：识别当前操作系统，编译出本系统的桌面应用
#   macOS   → build/bin/myllmapp.app        （默认 universal，Intel + Apple Silicon 通用）
#   Windows → build/bin/myllmapp.exe        （在 Git Bash / MSYS2 中运行）
#   Linux   → build/bin/myllmapp
#
# 也可以在 macOS / Linux 上交叉编译 Windows 版本（Wails 的 Windows 后端不需要 CGO）：
#   ./scripts/build.sh --windows
#
# 用法：
#   ./scripts/build.sh                 编译本系统版本
#   ./scripts/build.sh --windows       编译 Windows 版本（任意系统）
#   ./scripts/build.sh --all           本系统版本 + Windows 版本
#   ./scripts/build.sh --platform darwin/arm64   指定 Wails 平台字符串
#   ./scripts/build.sh --nsis          Windows 额外生成安装包（需要 makensis）
#   ./scripts/build.sh --debug         调试构建（可以打开开发者工具）
#   ./scripts/build.sh --skip-checks   跳过 go test / 前端检查
#   ./scripts/build.sh -h              显示帮助
#
# 说明：macOS 应用只能在 macOS 上编译，Linux 应用只能在 Linux 上编译（都依赖本机的 CGO 和系统 WebView）。

set -euo pipefail

WAILS_VERSION="v2.17.0"
APP_NAME="myllmapp"

# ---------- 输出辅助 ----------
if [[ -t 1 ]]; then
  C_INFO=$'\033[36m'; C_OK=$'\033[32m'; C_WARN=$'\033[33m'; C_ERR=$'\033[31m'; C_END=$'\033[0m'
else
  C_INFO=""; C_OK=""; C_WARN=""; C_ERR=""; C_END=""
fi
info() { echo "${C_INFO}==>${C_END} $*"; }
ok()   { echo "${C_OK}✔${C_END} $*"; }
warn() { echo "${C_WARN}!${C_END} $*"; }
die()  { echo "${C_ERR}✘ $*${C_END}" >&2; exit 1; }

usage() { sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

# ---------- 参数 ----------
BUILD_NATIVE=1
BUILD_WINDOWS=0
PLATFORM_OVERRIDE=""
NSIS=0
DEBUG=0
SKIP_CHECKS=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --windows)     BUILD_NATIVE=0; BUILD_WINDOWS=1 ;;
    --all)         BUILD_NATIVE=1; BUILD_WINDOWS=1 ;;
    --platform)    [[ $# -ge 2 ]] || die "--platform 需要参数，例如 darwin/arm64"
                   PLATFORM_OVERRIDE="$2"; shift ;;
    --nsis)        NSIS=1 ;;
    --debug)       DEBUG=1 ;;
    --skip-checks) SKIP_CHECKS=1 ;;
    -h|--help)     usage ;;
    *)             die "未知参数：$1（-h 查看帮助）" ;;
  esac
  shift
done

# 始终在项目根目录执行（脚本位于 scripts/ 下）
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
[[ -f wails.json ]] || die "没有找到 wails.json，请确认脚本位于项目的 scripts/ 目录下"

# ---------- 识别操作系统与架构 ----------
detect_os() {
  case "$(uname -s)" in
    Darwin)                      echo "darwin" ;;
    Linux)                       echo "linux" ;;
    MINGW*|MSYS*|CYGWIN*|Windows_NT) echo "windows" ;;
    *)                           die "不支持的操作系统：$(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)             die "不支持的 CPU 架构：$(uname -m)" ;;
  esac
}

HOST_OS="$(detect_os)"
HOST_ARCH="$(detect_arch)"
info "当前系统：${HOST_OS}/${HOST_ARCH}"

# ---------- 检查依赖 ----------
need() { command -v "$1" >/dev/null 2>&1 || die "缺少 $1：$2"; }

need go   "请安装 Go 1.26+：https://go.dev/dl/"
need node "请安装 Node.js 20.19+ 或 22.12+：https://nodejs.org/"
need npm  "请安装 npm（随 Node.js 一起安装）"
ok "go $(go env GOVERSION) · node $(node -v)"

# Wails CLI：没有就自动安装到 $(go env GOPATH)/bin
GOBIN_DIR="$(go env GOBIN)"
[[ -n "$GOBIN_DIR" ]] || GOBIN_DIR="$(go env GOPATH)/bin"
export PATH="$GOBIN_DIR:$PATH"
if ! command -v wails >/dev/null 2>&1; then
  info "未找到 Wails CLI，正在安装 ${WAILS_VERSION} ..."
  go install "github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}"
fi
need wails "安装失败，请手动执行：go install github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}"
ok "Wails CLI $(wails version 2>/dev/null | head -n1 | sed $'s/\x1b\[[0-9;]*m//g')"

# Linux：检测系统 WebKitGTK 版本。新发行版（Ubuntu 24.04+ 等）只有 4.1，需要额外的构建标签。
LINUX_TAGS=""
check_linux_deps() {
  need pkg-config "sudo apt install pkg-config"
  pkg-config --exists gtk+-3.0 || die "缺少 GTK3 开发包：sudo apt install libgtk-3-dev"
  if pkg-config --exists webkit2gtk-4.1; then
    LINUX_TAGS="webkit2_41"
    ok "WebKitGTK 4.1（使用构建标签 webkit2_41）"
  elif pkg-config --exists webkit2gtk-4.0; then
    ok "WebKitGTK 4.0"
  else
    die "缺少 WebKitGTK 开发包：sudo apt install libwebkit2gtk-4.1-dev（旧系统用 libwebkit2gtk-4.0-dev）"
  fi
}

# ---------- 构建前检查 ----------
if [[ $SKIP_CHECKS -eq 0 ]]; then
  info "运行 Go 测试 ..."
  # 成功时只显示一行结果；失败时打印完整输出，方便定位是哪个测试没过。
  if ! test_output="$(go test ./... 2>&1)"; then
    echo "$test_output" | grep -vE '^(ok|\?) ' >&2
    die "go test 未通过，失败详情见上方（可以用 --skip-checks 跳过，但不建议）"
  fi
  ok "Go 测试通过"
fi

info "安装前端依赖 ..."
if [[ -f frontend/package-lock.json ]]; then
  (cd frontend && npm ci --no-audit --no-fund >/dev/null)
else
  (cd frontend && npm install --no-audit --no-fund >/dev/null)
fi
ok "前端依赖就绪"

# ---------- 构建 ----------
# 参数：$1 = Wails 平台字符串（如 windows/amd64），$2 = 额外的 Go 构建标签（可为空）
wails_build() {
  local platform="$1" tags="$2"
  local args=(build -platform "$platform" -clean -trimpath)
  if [[ -n "$tags" ]]; then args+=(-tags "$tags"); fi
  if [[ $DEBUG -eq 1 ]]; then args+=(-debug); fi
  if [[ $NSIS -eq 1 && "$platform" == windows/* ]]; then args+=(-nsis); fi

  info "wails ${args[*]}"
  wails "${args[@]}"
}

# 收集产物，最后统一打印
ARTIFACTS=()
collect() { [[ -e "$1" ]] && ARTIFACTS+=("$1"); return 0; }

build_native() {
  local platform="$PLATFORM_OVERRIDE"
  case "$HOST_OS" in
    darwin)
      need xcode-select "请先执行：xcode-select --install"
      [[ -n "$platform" ]] || platform="darwin/universal"
      wails_build "$platform" ""
      collect "build/bin/${APP_NAME}.app"
      ;;
    linux)
      check_linux_deps
      [[ -n "$platform" ]] || platform="linux/${HOST_ARCH}"
      wails_build "$platform" "$LINUX_TAGS"
      collect "build/bin/${APP_NAME}"
      ;;
    windows)
      [[ -n "$platform" ]] || platform="windows/${HOST_ARCH}"
      wails_build "$platform" ""
      collect "build/bin/${APP_NAME}.exe"
      ;;
  esac
}

build_windows() {
  # -clean 会清空 build/bin，所以本系统产物先挪到一边，构建完再放回来
  local stash=""
  if [[ ${#ARTIFACTS[@]} -gt 0 ]]; then
    stash="$(mktemp -d)"
    mv build/bin/* "$stash"/
  fi

  local arch="amd64"
  [[ "$HOST_OS" == "windows" ]] && arch="$HOST_ARCH"
  wails_build "windows/${arch}" ""

  if [[ -n "$stash" ]]; then
    mv "$stash"/* build/bin/ && rmdir "$stash"
  fi
  collect "build/bin/${APP_NAME}.exe"
  collect "build/bin/${APP_NAME}-${arch}-installer.exe"
}

if [[ $BUILD_NATIVE -eq 1 ]]; then
  build_native
fi
# 在 Windows 上 --all 时本系统版本就是 Windows 版本，不必再编译一次
if [[ $BUILD_WINDOWS -eq 1 ]] && ! [[ $BUILD_NATIVE -eq 1 && "$HOST_OS" == "windows" ]]; then
  build_windows
fi

# ---------- 结果 ----------
echo
[[ ${#ARTIFACTS[@]} -gt 0 ]] || die "构建结束但没有找到产物，请检查上面的输出"
ok "构建完成："
for a in "${ARTIFACTS[@]}"; do
  size="$(du -sh "$a" | cut -f1)"
  echo "    $a  ($size)"
done

case "$HOST_OS" in
  darwin)  echo; echo "运行：open build/bin/${APP_NAME}.app" ;;
  linux)   echo; echo "运行：./build/bin/${APP_NAME}" ;;
  windows) echo; echo "运行：./build/bin/${APP_NAME}.exe" ;;
esac
