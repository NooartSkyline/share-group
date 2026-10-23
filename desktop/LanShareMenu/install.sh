#!/bin/sh
# สร้างแอป menu bar แล้วติดตั้งไป ~/Applications และเปิดทันที
set -e
SRC="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
APP_SRC="$SRC/build/Share Group.app"
DEST="$HOME/Applications/Share Group.app"

"$SRC/build.sh"

mkdir -p "$HOME/Applications"
rm -rf "$DEST"
cp -R "$APP_SRC" "$DEST"
open "$DEST"
echo "ติดตั้งแล้ว: $DEST"
echo "ไอคอนอยู่บน menu bar — เปิดโปรแกรม หรือเปิดในเบราว์เซอร์ได้จากเมนู"
