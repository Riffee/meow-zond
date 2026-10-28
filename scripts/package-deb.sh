#!/usr/bin/env bash
# Build a Debian package: ./scripts/package-deb.sh [amd64|arm64] [path/to/prebuilt-binary]
# Needs dpkg-deb; the binary is compiled with Go unless one is supplied.
set -euo pipefail

ARCH="${1:-amd64}"
PREBUILT="${2:-}"
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." >/dev/null 2>&1 && pwd)"
VERSION="${VERSION:-$(cat "$ROOT/VERSION")}"

die() {
  echo "Ошибка: $*" >&2
  exit 1
}

case "$ARCH" in
  amd64) GOARCH=amd64 ;;
  arm64) GOARCH=arm64 ;;
  *) die "поддерживаются архитектуры amd64 и arm64" ;;
esac
command -v dpkg-deb >/dev/null 2>&1 || die "нужен dpkg-deb (пакет dpkg)"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
PKG="$WORK/root"

if [[ -n "$PREBUILT" ]]; then
  [[ -f "$PREBUILT" ]] || die "файл $PREBUILT не найден"
  BIN="$PREBUILT"
else
  command -v go >/dev/null 2>&1 || die "нужен Go или путь к готовому бинарнику вторым аргументом"
  BIN="$WORK/meow-zond"
  (
    cd "$ROOT"
    CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -trimpath -ldflags="-s -w" -o "$BIN" ./cmd/meow-zond
  )
fi

install -d -m 755 "$PKG/DEBIAN" "$PKG/usr/bin" \
  "$PKG/usr/share/pixmaps" "$PKG/usr/share/applications" \
  "$PKG/lib/systemd/system" "$PKG/usr/share/doc/meow-zond"
install -m 755 "$BIN" "$PKG/usr/bin/meow-zond"
install -m 644 "$ROOT/assets/logo.png" "$PKG/usr/share/pixmaps/meow-zond.png"
install -m 644 "$ROOT/assets/meow-zond.desktop" "$PKG/usr/share/applications/meow-zond.desktop"
install -m 644 "$ROOT/assets/meow-zond.service" "$PKG/lib/systemd/system/meow-zond.service"
install -m 644 "$ROOT/LICENSE" "$PKG/usr/share/doc/meow-zond/copyright"
gzip -9cn "$ROOT/README.ru.md" > "$PKG/usr/share/doc/meow-zond/README.ru.md.gz"

cat > "$WORK/control" <<EOF
Package: meow-zond
Version: $VERSION
Architecture: $ARCH
Maintainer: meow! status project <https://github.com/Riffee/meow-zond>
Homepage: https://github.com/Riffee/meow-zond
Section: net
Priority: optional
Depends: systemd
Description: сетевой зонд для meow! status
 Зонд выполняет проверки ICMP, TCP, DNS, TLS и HTTP из собственной сети и
 отправляет результаты на статус-страницу по HTTPS. Входящие порты не нужны.
 .
 Служба устанавливается выключенной: включите её сами через
 sudo systemctl enable --now meow-zond или программой meow-zond без параметров.
EOF
cp "$WORK/control" "$PKG/DEBIAN/control"

cat > "$PKG/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
echo "Зонд: /usr/bin/meow-zond (запуск без параметров — меню и подключение)."
echo "Фоновая служба пока выключена: sudo systemctl enable --now meow-zond"
EOF
cat > "$PKG/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e
if [ -d /run/systemd/system ]; then
  systemctl disable --now meow-zond.service >/dev/null 2>&1 || true
fi
EOF
cat > "$PKG/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
EOF
chmod 755 "$PKG/DEBIAN/postinst" "$PKG/DEBIAN/prerm" "$PKG/DEBIAN/postrm"

mkdir -p "$ROOT/dist"
OUT="$ROOT/dist/meow-zond_${VERSION}_${ARCH}.deb"
dpkg-deb --root-owner-group --build "$PKG" "$OUT"
echo "$OUT"
