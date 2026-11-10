#!/usr/bin/env bash
# 从 backend/pansou 编译 linux/amd64 或 linux/arm64 后端
# 交飞牛的是二进制；日常改代码在 backend/pansou/
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="${PANSOU_SRC:-$ROOT/backend/pansou}"
TARGET_ARCH="${GOARCH:-amd64}"
case "$TARGET_ARCH" in
  amd64) DEFAULT_OUT="$ROOT/app/bin/seekbox" ;;
  arm64) DEFAULT_OUT="$ROOT/build/seekbox-linux-arm64" ;;
  *) echo "不支持的 GOARCH: $TARGET_ARCH（仅支持 amd64 / arm64）" >&2; exit 1 ;;
esac
OUT="${SEEKBOX_OUT:-$DEFAULT_OUT}"
[[ "$OUT" = /* ]] || OUT="$ROOT/$OUT"
GO_IMAGE="${GO_IMAGE:-golang:1.24.9-bookworm}"
APP_VERSION="${SEEKBOX_VERSION:-$(awk -F= '/^version[[:space:]]*=/{gsub(/[[:space:]]/, "", $2); print $2}' "$ROOT/manifest")}"
[[ "$APP_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "无效的构建版本" >&2; exit 1; }
# sonic 依赖须使用模块指定工具链；避免宿主较新 Go 的运行时变更。
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.24.9}"

if [ ! -f "$SRC/main.go" ]; then
  echo "[build-seekbox] 缺少源码: $SRC"
  echo "  请确认 backend/pansou/ 已纳入仓库（见 backend/README.md）"
  exit 1
fi

mkdir -p "$(dirname "$OUT")"

# 国内 NAS 默认走 goproxy.cn，避免 proxy.golang.org 超时
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

build_with_go() {
  echo "[build-seekbox] target=linux/$TARGET_ARCH local go: $(go version) GOPROXY=$GOPROXY"
  (
    cd "$SRC"
    CGO_ENABLED=0 GOOS=linux GOARCH="$TARGET_ARCH" GOPROXY="$GOPROXY" \
      go build -trimpath -ldflags="-s -w -X pansou/buildinfo.Version=$APP_VERSION" -o "$OUT" .
  )
}

build_with_docker() {
  echo "[build-seekbox] target=linux/$TARGET_ARCH docker: $GO_IMAGE GOPROXY=$GOPROXY"
  docker run --rm \
    -v "$SRC:/src" \
    -v "$(dirname "$OUT"):/out" \
    -w /src \
    -e CGO_ENABLED=0 \
    -e GOOS=linux \
    -e GOARCH="$TARGET_ARCH" \
    -e GOPROXY="$GOPROXY" \
    "$GO_IMAGE" \
    go build -trimpath -ldflags="-s -w -X pansou/buildinfo.Version=$APP_VERSION" -o "/out/$(basename "$OUT")" .
}

if command -v go >/dev/null 2>&1; then
  build_with_go
elif command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  build_with_docker
else
  echo "[build-seekbox] 需要本机 Go 1.24+，或已启动的 Docker"
  exit 1
fi

chmod +x "$OUT"
file "$OUT" || true
ls -lh "$OUT"
echo "[build-seekbox] done → $OUT"
echo "  下一步：./pack.sh 打出 FPK（交飞牛）；源码仍在 backend/pansou/"
