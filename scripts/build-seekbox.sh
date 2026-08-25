#!/usr/bin/env bash
# 从 backend/pansou 编译 linux/amd64 后端 → app/bin/seekbox
# 交飞牛的是二进制；日常改代码在 backend/pansou/
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="${PANSOU_SRC:-$ROOT/backend/pansou}"
OUT="$ROOT/app/bin/seekbox"
GO_IMAGE="${GO_IMAGE:-golang:1.24-bookworm}"

if [ ! -f "$SRC/main.go" ]; then
  echo "[build-seekbox] 缺少源码: $SRC"
  echo "  请确认 backend/pansou/ 已纳入仓库（见 backend/README.md）"
  exit 1
fi

mkdir -p "$(dirname "$OUT")"

# 国内 NAS 默认走 goproxy.cn，避免 proxy.golang.org 超时
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

build_with_go() {
  echo "[build-seekbox] local go: $(go version) GOPROXY=$GOPROXY"
  (
    cd "$SRC"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOPROXY="$GOPROXY" GOSUMDB=off \
      go build -trimpath -ldflags="-s -w" -o "$OUT" .
  )
}

build_with_docker() {
  echo "[build-seekbox] docker: $GO_IMAGE GOPROXY=$GOPROXY"
  docker run --rm \
    -v "$SRC:/src" \
    -w /src \
    -e CGO_ENABLED=0 \
    -e GOOS=linux \
    -e GOARCH=amd64 \
    -e GOPROXY="$GOPROXY" \
    -e GOSUMDB=off \
    "$GO_IMAGE" \
    go build -trimpath -ldflags="-s -w" -o /src/seekbox .
  mv -f "$SRC/seekbox" "$OUT"
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
