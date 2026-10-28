#!/usr/bin/env bash
# Build every published artifact into dist/: Windows exe, Linux binaries and .deb.
#   ./scripts/build-release.sh
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." >/dev/null 2>&1 && pwd)"
VERSION="${VERSION:-$(cat "$ROOT/VERSION")}"
DIST="$ROOT/dist"
command -v go >/dev/null 2>&1 || { echo "Ошибка: нужен Go" >&2; exit 1; }

mkdir -p "$DIST"
rm -f "$DIST"/*.exe "$DIST"/*.deb "$DIST"/meow-zond-linux-* 2>/dev/null || true

cd "$ROOT"
echo "windows/amd64"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$DIST/meow-zond-windows-amd64.exe" ./cmd/meow-zond
echo "windows/arm64"
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o "$DIST/meow-zond-windows-arm64.exe" ./cmd/meow-zond
for arch in amd64 arm64; do
  echo "linux/$arch"
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags="-s -w" -o "$DIST/meow-zond-linux-$arch" ./cmd/meow-zond
  if command -v dpkg-deb >/dev/null 2>&1; then
    VERSION="$VERSION" "$ROOT/scripts/package-deb.sh" $arch "$DIST/meow-zond-linux-$arch"
  else
    echo "dpkg-deb не найден: .deb для $arch не собран" >&2
  fi
done
(
  cd "$DIST"
  sha256sum -- * > SHA256SUMS
)
ls -l "$DIST"
