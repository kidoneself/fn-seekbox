#!/usr/bin/env bash
# 同一源码、同一前端产物，分别构建 x86 / ARM64 的 FPK。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

npm --prefix "$ROOT/frontend" run build
rsync -a --delete "$ROOT/frontend/dist/" "$ROOT/app/web/"

GOARCH=amd64 SEEKBOX_OUT="$ROOT/app/bin/seekbox" "$ROOT/scripts/build-seekbox.sh"
QR_PLATFORM=x86 QR_CHROMIUM_DIR="$ROOT/build/chromium-x86" "$ROOT/scripts/prepare-qr-runtime.sh"
PLATFORM=x86 QR_CHROMIUM_DIR="$ROOT/build/chromium-x86" SEEKBOX_BIN="$ROOT/app/bin/seekbox" "$ROOT/pack.sh"

GOARCH=arm64 SEEKBOX_OUT="$ROOT/build/seekbox-linux-arm64" "$ROOT/scripts/build-seekbox.sh"
QR_PLATFORM=arm QR_CHROMIUM_DIR="$ROOT/build/chromium-arm" "$ROOT/scripts/prepare-qr-runtime.sh"
PLATFORM=arm QR_CHROMIUM_DIR="$ROOT/build/chromium-arm" SEEKBOX_BIN="$ROOT/build/seekbox-linux-arm64" "$ROOT/pack.sh"

VERSION="$(awk -F= '/^version[[:space:]]*=/{gsub(/[[:space:]]/, "", $2); print $2}' "$ROOT/manifest")"
cd "$ROOT"
sha256sum "fn-seekbox-$VERSION-x86.fpk" "fn-seekbox-$VERSION-arm.fpk" > "build/fn-seekbox-$VERSION-SHA256SUMS.txt"
cat "build/fn-seekbox-$VERSION-SHA256SUMS.txt"
