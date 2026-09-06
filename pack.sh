#!/usr/bin/env bash
# 在干净目录里打 FPK，避免把 frontend/node_modules、backend/ 源码、旧 .fpk 等打进包。
# 交飞牛：只含 app/bin/seekbox（二进制）+ gateway + web 产物 + cmd/config/wizard。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
STAGE="$(mktemp -d /tmp/fn-seekbox-pack.XXXXXX)"
trap 'rm -rf "$STAGE"' EXIT

FNPACK="${FNPACK:-fnpack}"
PLATFORM="${PLATFORM:-$(awk -F= '/^platform/{gsub(/[[:space:]]/, "", $2); print $2}' "$ROOT/manifest") }"
PLATFORM="${PLATFORM//[[:space:]]/}"
SEEKBOX_BIN="${SEEKBOX_BIN:-$ROOT/app/bin/seekbox}"
if ! command -v "$FNPACK" >/dev/null 2>&1; then
  echo "未找到 fnpack。请先从飞牛开发者站下载并安装："
  echo "  https://static2.fnnas.com/fnpack/"
  exit 1
fi

echo "[pack] staging -> $STAGE"

mkdir -p "$STAGE/cmd" "$STAGE/config" "$STAGE/wizard" "$STAGE/app"
rsync -a "$ROOT/manifest" "$ROOT/ICON.PNG" "$ROOT/ICON_256.PNG" "$STAGE/"
rsync -a "$ROOT/cmd/" "$STAGE/cmd/"
rsync -a "$ROOT/config/" "$STAGE/config/"
rsync -a "$ROOT/wizard/" "$STAGE/wizard/"
rsync -a "$ROOT/app/" "$STAGE/app/"

if [ ! -f "$SEEKBOX_BIN" ]; then
  echo "后端二进制不存在: $SEEKBOX_BIN"
  exit 1
fi
cp "$SEEKBOX_BIN" "$STAGE/app/bin/seekbox"
sed -i.bak -E "s/^platform[[:space:]]*=.*/platform              = $PLATFORM/" "$STAGE/manifest"
rm "$STAGE/manifest.bak"

# 前端只打构建产物，不打源码树
rm -rf "$STAGE/app/web"
mkdir -p "$STAGE/app/web"
rsync -a "$ROOT/app/web/" "$STAGE/app/web/"

chmod +x "$STAGE/cmd/"* "$STAGE/app/bin/seekbox" "$STAGE/app/bin/gateway.py" 2>/dev/null || true

cd "$STAGE"
"$FNPACK" build

OUT="$ROOT/fn-seekbox-$(grep '^version' "$ROOT/manifest" | awk -F= '{print $2}' | xargs)-$PLATFORM.fpk"
mv -f fn-seekbox.fpk "$OUT"
ls -lh "$OUT"
echo "[pack] done: $OUT"
