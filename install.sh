#!/usr/bin/env bash
# Installs (or updates) the latest release for the current user: binaries in ~/.local/bin and the
# systemd user service, through the `make install-user` shipped inside the release zip.
#
#   curl -fsSL https://raw.githubusercontent.com/taurzon5/Nyaanchiver/master/install.sh | bash
#
# On a headless server or LXC container (no desktop, no logged-in user), --system, as root, installs
# a system service instead (`make install-system`): binaries in /usr/local/bin and a service started
# at boot, running as SERVICE_USER (default autoanimedownloader, created when missing):
#
#   curl -fsSL https://raw.githubusercontent.com/taurzon5/Nyaanchiver/master/install.sh | sudo bash -s -- --system
#
# Running it again updates: install-user stops the service, replaces the binaries and starts it.
# The asset is picked by its suffix, not by its full name, so the project prefix in the zip name can
# change without breaking this script. The checksum check is mandatory: this is curl into bash.
set -euo pipefail

REPO="taurzon5/Nyaanchiver"

die() {
	echo "error: $*" >&2
	exit 1
}

TARGET=install-user
case "${1:-}" in
"") ;;
--system) TARGET=install-system ;;
*) die "unknown option: $1 (the only option is --system)" ;;
esac

[ "$(uname -s)" = "Linux" ] || die "only Linux is supported by this script (Windows: download the .exe from the releases page)"
if [ "$TARGET" = install-system ] && [ "$(id -u)" != "0" ]; then
	die "--system needs root: run it with sudo"
fi

for bin in curl unzip sha256sum make; do
	command -v "$bin" >/dev/null 2>&1 || die "'$bin' is required but not installed"
done

case "$(uname -m)" in
x86_64 | amd64) ARCH=Linux_x86 ;;
aarch64 | arm64) ARCH=Linux_Arm64 ;;
*) die "unsupported architecture: $(uname -m)" ;;
esac

RELEASE_JSON=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest") ||
	die "failed to query the latest release from GitHub"

ZIP_URL=$(printf '%s\n' "$RELEASE_JSON" |
	sed -n 's/.*"browser_download_url": *"\([^"]*_'"$ARCH"'_v[^"]*\.zip\)".*/\1/p' | head -n 1)
[ -n "$ZIP_URL" ] || die "no $ARCH zip found in the latest release"

ZIP=$(basename "$ZIP_URL")
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "Downloading $ZIP..."
curl -fsSL -o "$TMP/$ZIP" "$ZIP_URL" || die "failed to download $ZIP_URL"
curl -fsSL -o "$TMP/$ZIP.sha256" "$ZIP_URL.sha256" || die "failed to download $ZIP_URL.sha256"

(cd "$TMP" && sha256sum -c --quiet "$ZIP.sha256") || die "checksum mismatch for $ZIP, aborting"
unzip -q "$TMP/$ZIP" -d "$TMP"

PKG_DIR="$TMP/${ZIP%.zip}"
[ -f "$PKG_DIR/Makefile" ] || die "unexpected zip layout: $PKG_DIR/Makefile not found"

if [ "$TARGET" = install-system ] && [ ! -f "$PKG_DIR/autoanimedownloader-system.service" ]; then
	die "the latest release has no system service yet; install without --system, or wait for the next release"
fi
make --no-print-directory -C "$PKG_DIR" "$TARGET"

if [ "$TARGET" = install-user ]; then
	case ":$PATH:" in
	*":$HOME/.local/bin:"*) ;;
	*) echo "note: $HOME/.local/bin is not in your PATH; add it to use the CLI" ;;
	esac
fi
