#!/usr/bin/env bash
# 在干净目录里打 FPK，避免把 frontend/node_modules、backend/ 源码、旧 .fpk 等打进包。
# 交飞牛：只含 app/bin/seekbox（二进制）+ gateway + web 产物 + cmd/config/wizard。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
STAGE="$(mktemp -d "${TMPDIR:-/tmp}/fn-seekbox-pack.XXXXXX")"
trap 'rm -rf "$STAGE"' EXIT

FNPACK="${FNPACK:-fnpack}"
PLATFORM="${PLATFORM:-$(awk -F= '/^platform/{gsub(/[[:space:]]/, "", $2); print $2}' "$ROOT/manifest")}"
PLATFORM="${PLATFORM//[[:space:]]/}"
case "$PLATFORM" in
  x86) DEFAULT_BIN="$ROOT/app/bin/seekbox" ;;
  arm) DEFAULT_BIN="$ROOT/build/seekbox-linux-arm64" ;;
  *) echo "当前入口生成单架构包，仅支持 PLATFORM=x86 或 arm" >&2; exit 1 ;;
esac
SEEKBOX_BIN="${SEEKBOX_BIN:-$DEFAULT_BIN}"
QR_CHROMIUM_DIR="${QR_CHROMIUM_DIR:-$ROOT/app/auth/chromium}"
INCLUDE_LOCAL_BROWSER="${SEEKBOX_INCLUDE_LOCAL_BROWSER:-0}"
[[ "$INCLUDE_LOCAL_BROWSER" = 0 || "$INCLUDE_LOCAL_BROWSER" = 1 ]] || { echo "SEEKBOX_INCLUDE_LOCAL_BROWSER 只能为 0 或 1" >&2; exit 1; }
[[ "$SEEKBOX_BIN" = /* ]] || SEEKBOX_BIN="$ROOT/$SEEKBOX_BIN"

# 不能只修改 manifest：确保包里的真正程序与目标架构一致。
python3 - "$SEEKBOX_BIN" "$PLATFORM" <<'PYELF'
import pathlib, struct, sys
binary, platform = sys.argv[1:]
try:
    with pathlib.Path(binary).open('rb') as stream:
        header = stream.read(64)
except OSError as exc:
    raise SystemExit(f"无法读取后端程序: {exc}")
if len(header) < 64 or header[:4] != b'\x7fELF' or header[4:6] != bytes([2, 1]):
    raise SystemExit("后端必须为64位小端ELF程序")
kind, machine = struct.unpack_from('<HH', header, 16)
expected = {'x86': 62, 'arm': 183}[platform]
if kind not in (2, 3) or machine != expected:
    raise SystemExit(f"程序架构与 PLATFORM={platform} 不匹配（ELF machine={machine}）")
print(f"[pack] verified {platform} ELF: {binary}")
PYELF
python3 - "$ROOT/app/auth" "$PLATFORM" "$QR_CHROMIUM_DIR" "$INCLUDE_LOCAL_BROWSER" <<'PYQR'
import pathlib, struct, sys
root, platform = pathlib.Path(sys.argv[1]), sys.argv[2]
if not (root / 'node_modules/playwright-core/package.json').is_file():
    raise SystemExit('缺少扫码运行库，请先运行 scripts/prepare-qr-runtime.sh')
if sys.argv[4] == '0':
    print('[pack] local browser omitted; Xunlei QR uses remote scan center')
    raise SystemExit(0)
browser = pathlib.Path(sys.argv[3]) / 'chrome-headless-shell'
if not browser.is_file():
    raise SystemExit('缺少 Chromium，请先运行 scripts/prepare-qr-runtime.sh')
with browser.open('rb') as f:
    header = f.read(64)
if len(header) < 64 or header[:6] != b'\x7fELF\x02\x01' or struct.unpack_from('<H', header, 18)[0] != {'x86':62,'arm':183}[platform]:
    raise SystemExit('扫码浏览器与目标架构不匹配；请准备该架构的运行库后打包')
print('[pack] verified QR runtime architecture:', platform)
PYQR
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
rsync -a --exclude=/auth/chromium/ --exclude=__pycache__/ --exclude='*.pyc' "$ROOT/app/" "$STAGE/app/"
if [[ "$INCLUDE_LOCAL_BROWSER" = 1 ]]; then
  mkdir -p "$STAGE/app/auth/chromium"
  rsync -a "$QR_CHROMIUM_DIR/" "$STAGE/app/auth/chromium/"
fi

if [ ! -f "$SEEKBOX_BIN" ]; then
  echo "后端二进制不存在: $SEEKBOX_BIN"
  exit 1
fi
cp "$SEEKBOX_BIN" "$STAGE/app/bin/seekbox"
# The immutable bootstrap remains outside online release archives.
if [ -n "${SEEKBOX_RELEASE_PUBLIC_KEYS_FILE:-}" ]; then
  python3 - "$SEEKBOX_RELEASE_PUBLIC_KEYS_FILE" "$STAGE/app/etc/update-public-keys.json" <<'PYKEY'
import base64, json, pathlib, sys
source, target = map(pathlib.Path, sys.argv[1:])
keys = json.loads(source.read_text())
if not isinstance(keys, dict) or not keys:
    raise SystemExit('发布公钥文件必须是非空 JSON 对象')
for key_id, key in keys.items():
    if not key_id or not isinstance(key, str) or len(base64.b64decode(key, validate=True)) != 32:
        raise SystemExit('发布公钥无效；这里只允许 Ed25519 公钥，禁止使用私钥文件')
target.parent.mkdir(parents=True, exist_ok=True)
target.write_text(json.dumps(keys) + '\n')
PYKEY
fi
cp "$ROOT/cmd/main" "$STAGE/app/bin/seekbox-bootstrap.sh"
chmod +x "$STAGE/app/bin/seekbox-bootstrap.sh"
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
