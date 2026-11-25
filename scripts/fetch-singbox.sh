#!/bin/sh
# Fetches the pinned sing-box core into resources/darwin-<arch>/bin/, where
# `mygo build` picks it up for that architecture's app. For this Mac's own
# architecture it is also linked as bin/sing-box, for development and tests,
# and checked to have the Clash API.
#
#   ./scripts/fetch-singbox.sh            this Mac's architecture
#   ./scripts/fetch-singbox.sh amd64      another (arm64 or amd64), to package it
set -eu
VER="${SINGBOX_VERSION:-1.14.4}"
HOST="$(uname -m)"; [ "$HOST" = "x86_64" ] && HOST=amd64 || HOST=arm64
ARCH="${1:-$HOST}"
case "$ARCH" in arm64|amd64) ;; *) echo "architecture: arm64 or amd64, not $ARCH" >&2; exit 1 ;; esac
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIR="$ROOT/resources/darwin-$ARCH/bin"
NAME="sing-box-$VER-darwin-$ARCH"
MARK="$ROOT/bin/.sing-box-$ARCH.version" # outside resources/: nothing there but the core ships
mkdir -p "$DIR" "$ROOT/bin"

link() { # this Mac's own core is the one development and tests run
  [ "$ARCH" = "$HOST" ] || return 0
  mkdir -p "$ROOT/bin"; ln -sf "$DIR/sing-box" "$ROOT/bin/sing-box"
}

if [ -x "$DIR/sing-box" ] && [ "$(cat "$MARK" 2>/dev/null)" = "$VER" ]; then
  echo "sing-box $VER ($ARCH) already in $DIR"; link; exit 0
fi
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
gh release download "v$VER" --repo SagerNet/sing-box --pattern "$NAME.tar.gz" --dir "$TMP"
tar -xzf "$TMP/$NAME.tar.gz" -C "$TMP"
install -m 755 "$TMP/$NAME/sing-box" "$DIR/sing-box"
echo "$VER" > "$MARK"
if [ "$ARCH" = "$HOST" ]; then # it can run here: check it is the build Faro needs
  "$DIR/sing-box" version
  "$DIR/sing-box" version | grep -q with_clash_api || { echo "this build lacks with_clash_api" >&2; exit 1; }
else
  file "$DIR/sing-box" | grep -q "$( [ "$ARCH" = amd64 ] && echo x86_64 || echo arm64 )" || { echo "not a $ARCH program" >&2; exit 1; }
  echo "sing-box $VER ($ARCH) in $DIR"
fi
link
mkdir -p "$ROOT/resources/licenses"
cp "$TMP/$NAME/LICENSE" "$ROOT/resources/licenses/sing-box-LICENSE.txt" 2>/dev/null || \
  gh api repos/SagerNet/sing-box/contents/LICENSE --jq .content | base64 -d > "$ROOT/resources/licenses/sing-box-LICENSE.txt"
