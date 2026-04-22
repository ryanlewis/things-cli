#!/bin/sh
# install.sh — download and install the latest things-cli release.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/main/install.sh | sh
#
# Environment:
#   INSTALL_DIR   target directory for the binary (default: /usr/local/bin)
#   VERSION       version tag to install (default: latest release)

set -eu

REPO="ryanlewis/things-cli"
BIN="things"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

err() {
	printf 'install.sh: %s\n' "$1" >&2
	exit 1
}

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
[ "$OS" = "darwin" ] || err "things-cli only supports macOS (detected: $OS)"

ARCH=$(uname -m)
case "$ARCH" in
	arm64|aarch64) ARCH="arm64" ;;
	x86_64)        ARCH="amd64" ;;
	*)             err "unsupported architecture: $ARCH" ;;
esac

command -v curl >/dev/null 2>&1 || err "curl is required"
command -v tar  >/dev/null 2>&1 || err "tar is required"
command -v shasum >/dev/null 2>&1 || err "shasum is required"

# Resolve VERSION by following the /releases/latest redirect to /releases/tag/vX.Y.Z.
# Avoids JSON parsing and works without an auth token.
VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
	VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
		"https://github.com/${REPO}/releases/latest" \
		| sed 's|.*/tag/||')
	[ -n "$VERSION" ] || err "failed to detect latest version"
fi

VERSION_NUM="${VERSION#v}"
TARBALL="things_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

printf 'Downloading %s %s (%s/%s)...\n' "$BIN" "$VERSION" "$OS" "$ARCH"
curl -fsSL -o "$TMP/$TARBALL"      "$BASE_URL/$TARBALL"
curl -fsSL -o "$TMP/checksums.txt" "$BASE_URL/checksums.txt"

printf 'Verifying checksum...\n'
(cd "$TMP" && grep "  ${TARBALL}\$" checksums.txt | shasum -a 256 -c -) >/dev/null \
	|| err "checksum verification failed"

tar -xzf "$TMP/$TARBALL" -C "$TMP"
[ -f "$TMP/$BIN" ] || err "archive did not contain expected binary: $BIN"

TARGET="$INSTALL_DIR/$BIN"
if [ -w "$INSTALL_DIR" ] || { [ ! -e "$INSTALL_DIR" ] && [ -w "$(dirname "$INSTALL_DIR")" ]; }; then
	mkdir -p "$INSTALL_DIR"
	mv "$TMP/$BIN" "$TARGET"
else
	printf 'Installing to %s (sudo required)...\n' "$INSTALL_DIR"
	sudo mkdir -p "$INSTALL_DIR"
	sudo mv "$TMP/$BIN" "$TARGET"
fi
chmod +x "$TARGET" 2>/dev/null || sudo chmod +x "$TARGET"

printf '\nInstalled: %s\n' "$("$TARGET" version)"
printf 'Location:  %s\n' "$TARGET"

case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*) printf '\nNote: %s is not on your PATH.\n' "$INSTALL_DIR" ;;
esac
