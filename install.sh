#!/bin/sh
# install.sh — download and install the latest things-cli release.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/main/install.sh | sh
#
# Environment:
#   INSTALL_DIR        target directory for the binary (default: /usr/local/bin)
#   VERSION            version tag to install, e.g. vX.Y.Z (default: latest release)
#   RELEASE_BASE_URL   override the asset download base URL (for mirrors / testing)

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

# Resolve VERSION by following the /releases/latest redirect.
# Normalize to a leading "v" so VERSION=1.2.3 also works.
VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
	VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
		"https://github.com/${REPO}/releases/latest" \
		| sed 's|.*/tag/||')
fi
case "$VERSION" in
	v[0-9]*)   ;;
	[0-9]*)    VERSION="v$VERSION" ;;
	*)         err "could not determine version (got: '${VERSION}')" ;;
esac

TARBALL="things_${VERSION#v}_${OS}_${ARCH}.tar.gz"
BASE_URL="${RELEASE_BASE_URL:-https://github.com/${REPO}/releases/download/${VERSION}}"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM HUP

printf 'Downloading %s %s (%s/%s)...\n' "$BIN" "$VERSION" "$OS" "$ARCH"
curl -fsSL -o "$TMP/$TARBALL"      "$BASE_URL/$TARBALL"
curl -fsSL -o "$TMP/checksums.txt" "$BASE_URL/checksums.txt"

printf 'Verifying checksum...\n'
EXPECTED=$(awk -v f="$TARBALL" '$2 == f {print $1}' "$TMP/checksums.txt")
[ -n "$EXPECTED" ] || err "no checksum entry for $TARBALL in checksums.txt"
ACTUAL=$(shasum -a 256 "$TMP/$TARBALL" | awk '{print $1}')
[ "$EXPECTED" = "$ACTUAL" ] || err "checksum mismatch (expected $EXPECTED, got $ACTUAL)"

# Verify build provenance when the GitHub CLI is available: proves the tarball
# was built by this repo's release workflow, not just that it downloaded
# intact. Skipped (with a note) when gh is missing or logged out, since the
# checksum has already passed. Releases before v0.5.1 predate attestations, so
# "no attestations found" warns rather than fails. Opt out with SKIP_ATTESTATION=1.
if [ -n "${SKIP_ATTESTATION:-}" ]; then
	printf 'Note: provenance verification skipped (SKIP_ATTESTATION set).\n'
elif command -v gh >/dev/null 2>&1; then
	printf 'Verifying build provenance...\n'
	if ATTEST_OUT=$(gh attestation verify "$TMP/$TARBALL" --repo "$REPO" 2>&1); then
		printf 'Provenance verified: built by the %s release workflow.\n' "$REPO"
	elif ! gh auth status >/dev/null 2>&1; then
		printf 'Note: skipping provenance check (gh is not logged in).\n'
	elif printf '%s' "$ATTEST_OUT" | grep -Eqi 'no attestations found|HTTP 404.*attestations'; then
		printf 'Note: no provenance attestation for %s (releases before v0.5.1 predate attestations).\n' "$VERSION"
	else
		err "build provenance verification FAILED for $TARBALL — refusing to install.
This artifact does not verify as built by the ${REPO} release workflow.
(To bypass at your own risk: SKIP_ATTESTATION=1)"
	fi
else
	printf 'Note: install gh (https://cli.github.com) to enable build provenance verification.\n'
fi

tar -xzf "$TMP/$TARBALL" -C "$TMP"
[ -f "$TMP/$BIN" ] || err "archive did not contain expected binary: $BIN"

SUDO=""
if [ ! -w "$INSTALL_DIR" ] && { [ -e "$INSTALL_DIR" ] || [ ! -w "$(dirname "$INSTALL_DIR")" ]; }; then
	SUDO="sudo"
	printf 'Installing to %s (sudo required)...\n' "$INSTALL_DIR"
fi
$SUDO mkdir -p "$INSTALL_DIR"
$SUDO install -m 0755 "$TMP/$BIN" "$INSTALL_DIR/$BIN"

TARGET="$INSTALL_DIR/$BIN"
printf '\nInstalled: %s\n' "$("$TARGET" version)"
printf 'Location:  %s\n' "$TARGET"

case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*) printf '\nNote: %s is not on your PATH.\n' "$INSTALL_DIR" ;;
esac
