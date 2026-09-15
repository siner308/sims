#!/bin/sh
# Installs the latest sims release binary for this machine.
#   curl -fsSL https://raw.githubusercontent.com/siner308/sims/main/install.sh | sh
# Options via env: SIMS_VERSION=v0.2.0  SIMS_INSTALL_DIR=/usr/local/bin
set -eu

REPO="siner308/sims"
VERSION="${SIMS_VERSION:-latest}"
INSTALL_DIR="${SIMS_INSTALL_DIR:-}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "install.sh: $1 is required" >&2; exit 1; }; }
need curl
need tar
need uname

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os" in
  darwin|linux) ;;
  mingw*|msys*|cygwin*) echo "install.sh: on Windows download the .zip from https://github.com/$REPO/releases" >&2; exit 1 ;;
  *) echo "install.sh: unsupported OS $os" >&2; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "install.sh: unsupported arch $arch" >&2; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
  [ -n "$VERSION" ] || { echo "install.sh: could not resolve the latest release" >&2; exit 1; }
fi

if [ -z "$INSTALL_DIR" ]; then
  if [ -w /usr/local/bin ]; then INSTALL_DIR=/usr/local/bin; else INSTALL_DIR="$HOME/.local/bin"; fi
fi
mkdir -p "$INSTALL_DIR"

asset="sims_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/$VERSION/$asset"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "downloading $url"
curl -fsSL "$url" -o "$tmp/$asset"
curl -fsSL "https://github.com/$REPO/releases/download/$VERSION/checksums.txt" -o "$tmp/checksums.txt"
if command -v shasum >/dev/null 2>&1; then
  (cd "$tmp" && grep " $asset\$" checksums.txt | shasum -a 256 -c - >/dev/null) || { echo "install.sh: checksum mismatch" >&2; exit 1; }
elif command -v sha256sum >/dev/null 2>&1; then
  (cd "$tmp" && grep " $asset\$" checksums.txt | sha256sum -c - >/dev/null) || { echo "install.sh: checksum mismatch" >&2; exit 1; }
fi
tar -xzf "$tmp/$asset" -C "$tmp" sims
install -m 0755 "$tmp/sims" "$INSTALL_DIR/sims"

echo "installed sims $VERSION to $INSTALL_DIR/sims"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "add $INSTALL_DIR to your PATH, e.g. export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac
