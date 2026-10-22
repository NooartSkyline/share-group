#!/bin/sh
# สร้างแอป menu bar แล้วติดตั้งไป ~/Applications และเปิดทันที
set -e
SRC="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
APP_SRC="$SRC/build/LAN Share.app"
DEST="$HOME/Applications/LAN Share.app"

"$SRC/build.sh"

mkdir -p "$HOME/Applications"
rm -rf "$DEST"
cp -R "$APP_SRC" "$DEST"
open "$DEST"
echo "ติดตั้งแล้ว: $DEST"
echo "ไอคอน 📡 อยู่บน menu bar — ไม่โผล่ที่ Dock"
echo "เปิดเมื่อเข้าสู่ระบบ: กดที่ไอคอนแล้วเลือกเมนูนั้น"
