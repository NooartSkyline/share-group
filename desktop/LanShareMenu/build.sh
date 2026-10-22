#!/bin/sh
# สร้างแอป menu bar LAN Share.app (AppKit, ไม่ใช้ Wails)
set -e
ROOT="$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)"
APP_SRC="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
OUT="${1:-$APP_SRC/build/LAN Share.app}"
MACOS="$OUT/Contents/MacOS"
RES="$OUT/Contents/Resources"
BIN="$HOME/LANShare/bin"

echo "build Go binaries → $BIN"
mkdir -p "$BIN"
(cd "$ROOT" && go build -o "$BIN/lanshare" ./cmd/lanshare)
(cd "$ROOT" && go build -o "$BIN/share" ./cmd/share)

echo "build Swift app → $OUT"
rm -rf "$OUT"
mkdir -p "$MACOS" "$RES"
cp "$APP_SRC/Info.plist" "$OUT/Contents/Info.plist"
swiftc -O \
  -o "$MACOS/LAN Share" \
  "$APP_SRC/Sources/main.swift" \
  -framework AppKit
cp "$BIN/lanshare" "$RES/lanshare"
cp "$BIN/share" "$RES/share"
chmod +x "$MACOS/LAN Share" "$RES/lanshare" "$RES/share"

echo "เสร็จ: $OUT"
echo "เปิดด้วย: open \"$OUT\""
echo "ติดตั้งเปิดเมื่อเข้าสู่ระบบ: $APP_SRC/install.sh"
