#!/bin/sh
# Fetches the pinned sing-box core into resources/darwin-<arch>/bin/, where
# `mygo build` picks it up for the app, and links it as bin/sing-box for
# development. Checks it has the Clash API.
set -eu
VER="${SINGBOX_VERSION:-1.14.4}"
ARCH="$(uname -m)"; [ "$ARCH" = "x86_64" ] && ARCH=amd64 || ARCH=arm64
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIR="$ROOT/resources/darwin-$ARCH/bin"
NAME="sing-box-$VER-darwin-$ARCH"
mkdir -p "$DIR"
if [ -x "$DIR/sing-box" ] && "$DIR/sing-box" version | head -1 | grep -q "$VER"; then
  echo "sing-box $VER already in $DIR"; mkdir -p "$ROOT/bin"; ln -sf "$DIR/sing-box" "$ROOT/bin/sing-box"; exit 0
fi
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
gh release download "v$VER" --repo SagerNet/sing-box --pattern "$NAME.tar.gz" --dir "$TMP"
tar -xzf "$TMP/$NAME.tar.gz" -C "$TMP"
install -m 755 "$TMP/$NAME/sing-box" "$DIR/sing-box"
"$DIR/sing-box" version
"$DIR/sing-box" version | grep -q with_clash_api || { echo "this build lacks with_clash_api" >&2; exit 1; }
mkdir -p "$ROOT/bin" "$ROOT/resources/licenses"
ln -sf "$DIR/sing-box" "$ROOT/bin/sing-box"
cp "$TMP/$NAME/LICENSE" "$ROOT/resources/licenses/sing-box-LICENSE.txt" 2>/dev/null || \
  gh api repos/SagerNet/sing-box/contents/LICENSE --jq .content | base64 -d > "$ROOT/resources/licenses/sing-box-LICENSE.txt"
