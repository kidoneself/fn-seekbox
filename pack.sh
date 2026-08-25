#!/usr/bin/env bash
# 在干净目录里打 FPK，避免把 frontend/node_modules、backend/ 源码、旧 .fpk 等打进包。
# 交飞牛：只含 app/bin/seekbox（二进制）+ gateway + web 产物 + cmd/config/wizard。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
STAGE="$(mktemp -d /tmp/fn-seekbox-pack.XXXXXX)"
trap 'rm -rf "$STAGE"' EXIT

FNPACK="${FNPACK:-fnpack}"
if ! command -v "$FNPACK" >/dev/null 2>&1; then
  echo "未找到 fnpack。请先从飞牛开发者站下载并安装："
  echo "  https://static2.fnnas.com/fnpack/"
  exit 1
fi

echo "[pack] staging -> $STAGE"

rsync -a \
  "$ROOT/manifest" \
  "$ROOT/ICON.PNG" \
  "$ROOT/ICON_256.PNG" \
  "$ROOT/cmd/" "$STAGE/cmd/" \
  "$ROOT/config/" "$STAGE/config/" \
  "$ROOT/wizard/" "$STAGE/wizard/" \
  "$ROOT/app/" "$STAGE/app/"

# 前端只打构建产物，不打源码树
rm -rf "$STAGE/app/web"
mkdir -p "$STAGE/app/web"
rsync -a "$ROOT/app/web/" "$STAGE/app/web/"

chmod +x "$STAGE/cmd/"* "$STAGE/app/bin/seekbox" "$STAGE/app/bin/gateway.py" 2>/dev/null || true

cd "$STAGE"
"$FNPACK" build

OUT="$ROOT/fn-seekbox-$(grep '^version' "$ROOT/manifest" | awk -F= '{print $2}' | xargs).fpk"
mv -f fn-seekbox.fpk "$OUT"
ls -lh "$OUT"
echo "[pack] done: $OUT"
