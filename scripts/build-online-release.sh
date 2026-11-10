#!/usr/bin/env bash
# Build one matching backend/frontend release; the signing key stays outside artifacts.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="${SEEKBOX_VERSION:-$(awk -F= '/^version[[:space:]]*=/{gsub(/[[:space:]]/, "", $2); print $2}' "$ROOT/manifest")}"
ARCH="${GOARCH:-amd64}"
: "${SEEKBOX_RELEASE_PRIVATE_KEY:?Specify the external Ed25519 private key path}"
: "${SEEKBOX_RELEASE_KEY_ID:?Specify the public signing-key identifier}"
OUT="${SEEKBOX_RELEASE_OUT:-$ROOT/build/releases/$VERSION-$ARCH}"
STAGE="$(mktemp -d "${TMPDIR:-/tmp}/seekbox-release-build.XXXXXX")"
trap 'rm -rf "$STAGE"' EXIT
mkdir -p "$STAGE/app/bin" "$STAGE/app/auth"
SEEKBOX_VERSION="$VERSION" GOARCH="$ARCH" SEEKBOX_OUT="$STAGE/app/bin/seekbox" bash "$ROOT/scripts/build-seekbox.sh"
SEEKBOX_VERSION="$VERSION" npm run build --prefix "$ROOT/frontend" -- --outDir "$STAGE/app/web" --emptyOutDir
cp "$ROOT/app/bin/gateway.py" "$STAGE/app/bin/gateway.py"
cp "$ROOT/app/auth/login.cjs" "$ROOT/app/auth/cookies.cjs" "$STAGE/app/auth/"
ARGS=(-root "$STAGE" -out "$OUT" -version "$VERSION" -arch "$ARCH" -channel "${SEEKBOX_RELEASE_CHANNEL:-stable}" -key-id "$SEEKBOX_RELEASE_KEY_ID" -private-key "$SEEKBOX_RELEASE_PRIVATE_KEY")
if [ -n "${SEEKBOX_RELEASE_NOTES_FILE:-}" ]; then
    ARGS+=(-notes-file "$SEEKBOX_RELEASE_NOTES_FILE")
else
    ARGS+=(-notes "${SEEKBOX_RELEASE_NOTES:-}")
fi
GOTOOLCHAIN="${GOTOOLCHAIN:-go1.24.9}" go run "$ROOT/scripts/release/main.go" "${ARGS[@]}"
echo "Release prepared: $OUT (upload release.json and the tar.gz in Version Releases)"
