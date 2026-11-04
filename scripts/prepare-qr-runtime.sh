#!/usr/bin/env bash
# Stage public runtime distributions; never copy private broker sources/configs.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
QR_PLATFORM="${QR_PLATFORM:-$(uname -m)}"
case "$QR_PLATFORM" in
  x86|amd64|x86_64) BROWSER_PLATFORM=debian12 ;;
  arm|arm64|aarch64) BROWSER_PLATFORM=debian12-arm64 ;;
  *) echo "不支持的扫码运行库架构：$QR_PLATFORM" >&2; exit 1 ;;
esac
QR_CHROMIUM_DIR="${QR_CHROMIUM_DIR:-$ROOT/app/auth/chromium}"
STAGE="$(mktemp -d /tmp/seekbox-qr-runtime.XXXXXX)"
trap 'rm -rf "$STAGE"' EXIT
npm install --prefix "$STAGE" --ignore-scripts --no-audit --no-fund playwright-core@1.57.0
PLAYWRIGHT_HOST_PLATFORM_OVERRIDE="$BROWSER_PLATFORM" PLAYWRIGHT_BROWSERS_PATH="$STAGE/browsers" node "$STAGE/node_modules/playwright-core/cli.js" install chromium --only-shell
shells=("$STAGE"/browsers/chromium_headless_shell-*/chrome-headless-shell-linux*)
if [[ ${#shells[@]} -ne 1 || ! -x "${shells[0]}/chrome-headless-shell" ]]; then
  echo "未找到当前构建主机架构的 Chromium headless shell" >&2
  exit 1
fi
mkdir -p "$ROOT/app/auth/node_modules/playwright-core" "$QR_CHROMIUM_DIR"
rsync -a --delete "$STAGE/node_modules/playwright-core/" "$ROOT/app/auth/node_modules/playwright-core/"
rsync -a --delete "${shells[0]}/" "$QR_CHROMIUM_DIR/"
echo "扫码运行库已暂存；pack.sh 会核对浏览器与目标架构。"
