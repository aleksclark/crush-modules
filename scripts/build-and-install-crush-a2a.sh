#!/usr/bin/env bash
#
# Build and install crush-a2a as an Arch Linux package.
#
# Downloads the latest crush-a2a release from GitHub, creates a PKGBUILD,
# builds a .pkg.tar.zst, and installs it via pacman.
#
# Usage: ./build-and-install-crush-a2a.sh
#
set -euo pipefail

REPO="aleksclark/crush-modules"
PKGNAME="crush-a2a"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "▸ Fetching latest release info..."
TAG=$(gh release view --repo "$REPO" --json tagName --jq '.tagName')
echo "  Release: $TAG"

ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  ASSET_ARCH="x86_64" ;;
    aarch64) ASSET_ARCH="arm64" ;;
    *)       echo "✗ Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

ASSET_NAME="crush-a2a_${TAG}_linux_${ASSET_ARCH}.tar.gz"
ASSET_URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET_NAME}"

echo "▸ Fetching checksum..."
SHA256=$(gh release view --repo "$REPO" --json assets \
    --jq ".assets[] | select(.name == \"checksums.txt\") | .url" \
    | xargs curl -sL \
    | grep "$ASSET_NAME" \
    | awk '{print $1}')

if [ -z "$SHA256" ]; then
    echo "✗ Could not find checksum for $ASSET_NAME" >&2
    exit 1
fi
echo "  SHA256: $SHA256"

# Convert calver tag (2026.04.29.3) to a valid pkgver (2026.04.29.3)
PKGVER="$TAG"

cat > "$WORKDIR/PKGBUILD" << PKGBUILD_EOF
# Maintainer: Aleks Clark <aleks.clark@gmail.com>
pkgname=${PKGNAME}-bin
pkgver=${PKGVER}
pkgrel=1
pkgdesc="Crush AI assistant with A2A v1.0 protocol plugin (unofficial build)"
arch=('x86_64' 'aarch64')
url="https://github.com/${REPO}"
license=('MIT')
provides=('crush')
conflicts=('crush' 'crush-extended-bin')

source_x86_64=("crush-a2a_\${pkgver}_linux_x86_64.tar.gz::https://github.com/${REPO}/releases/download/\${pkgver}/crush-a2a_\${pkgver}_linux_x86_64.tar.gz")
source_aarch64=("crush-a2a_\${pkgver}_linux_arm64.tar.gz::https://github.com/${REPO}/releases/download/\${pkgver}/crush-a2a_\${pkgver}_linux_arm64.tar.gz")

sha256sums_x86_64=('$(gh release view --repo "$REPO" --json assets --jq '.assets[] | select(.name == "checksums.txt") | .url' | xargs curl -sL | grep "crush-a2a_${TAG}_linux_x86_64.tar.gz" | awk '{print $1}')')
sha256sums_aarch64=('$(gh release view --repo "$REPO" --json assets --jq '.assets[] | select(.name == "checksums.txt") | .url' | xargs curl -sL | grep "crush-a2a_${TAG}_linux_arm64.tar.gz" | awk '{print $1}')')

package() {
    install -Dm755 "crush" "\${pkgdir}/usr/bin/crush"

    if [ -f LICENSE ]; then
        install -Dm644 "LICENSE" "\${pkgdir}/usr/share/licenses/\${pkgname}/LICENSE"
    fi
}
PKGBUILD_EOF

echo "▸ Building package in $WORKDIR..."
cd "$WORKDIR"
makepkg -sf --noconfirm 2>&1 | tail -20

PKG_FILE=$(ls -1 *.pkg.tar.zst 2>/dev/null | head -1)
if [ -z "$PKG_FILE" ]; then
    echo "✗ Package build failed" >&2
    exit 1
fi

echo "▸ Installing $PKG_FILE..."
sudo pacman -U --noconfirm --ask 4 "$PKG_FILE"

echo ""
echo "✓ crush-a2a installed successfully"
crush --version 2>&1 || true
crush --list-plugins 2>&1 | head -20
