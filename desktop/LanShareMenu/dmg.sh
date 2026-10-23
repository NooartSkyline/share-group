#!/bin/sh
# สร้าง Share Group.app แล้วแพ็กเป็น .dmg สำหรับลากไป Applications
set -e
APP_SRC="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
ROOT="$(CDPATH= cd -- "$APP_SRC/../.." && pwd)"
VERSION="${VERSION:-0.1.0}"
APP="$APP_SRC/build/Share Group.app"
DMG="${1:-$ROOT/Share Group-$VERSION.dmg}"
VOL="Share Group"
STAGE="$(mktemp -d "${TMPDIR:-/tmp}/share-group-dmg.XXXXXX")"

cleanup() { rm -rf "$STAGE"; }
trap cleanup EXIT

"$APP_SRC/build.sh" "$APP"

mkdir -p "$STAGE"
cp -R "$APP" "$STAGE/Share Group.app"
ln -s /Applications "$STAGE/Applications"
cat > "$STAGE/วิธีติดตั้ง.txt" <<EOF
Share Group $VERSION

1. ลาก Share Group ไปโฟลเดอร์ Applications
2. เปิดแอปจาก Applications (ไอคอนอยู่บน menu bar ไม่โผล่ที่ Dock)
3. ถ้า macOS เตือนว่าแอปมาจากผู้พัฒนาที่ไม่รู้จัก ให้คลิกขวาที่แอปแล้วเลือก เปิด

เซิร์ฟเวอร์เปิดที่พอร์ต 8080 ข้อมูลอยู่ที่ ~/LANShare
ยังไม่ได้ลงนาม (unsigned) จึงยังไม่ผ่าน Gatekeeper ของเครื่องอื่นโดยอัตโนมัติ
EOF

echo "create DMG → $DMG"
rm -f "$DMG"
hdiutil create \
  -volname "$VOL" \
  -srcfolder "$STAGE" \
  -ov \
  -format UDZO \
  "$DMG" >/dev/null

echo "เสร็จ: $DMG"
ls -lh "$DMG"
