#!/bin/sh
# สร้าง Share Group.app แล้วแพ็กเป็น .dmg สำหรับลากไป Applications
set -e
APP_SRC="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
ROOT="$(CDPATH= cd -- "$APP_SRC/../.." && pwd)"
if [ -z "${VERSION:-}" ]; then
  VERSION="$(tr -d '[:space:]' < "$ROOT/VERSION")"
fi
APP="$APP_SRC/build/Share Group.app"
DIST="$ROOT/build"
DMG="${1:-$DIST/Share Group-$VERSION.dmg}"
VOL="Share Group"
STAGE="$(mktemp -d "${TMPDIR:-/tmp}/share-group-dmg.XXXXXX")"

cleanup() { rm -rf "$STAGE"; }
trap cleanup EXIT

"$APP_SRC/build.sh" "$APP"

mkdir -p "$DIST" "$STAGE"
cp -R "$APP" "$STAGE/Share Group.app"
ln -s /Applications "$STAGE/Applications"
cat > "$STAGE/วิธีติดตั้ง.txt" <<EOF
Share Group $VERSION

1. ลาก Share Group ไปโฟลเดอร์ Applications
2. เปิดแอปจาก Applications (ไอคอนอยู่บน menu bar ไม่โผล่ที่ Dock)
3. ถ้า macOS ขึ้นว่าแอปเสียหาย (damaged) หรือมาจากผู้พัฒนาที่ไม่รู้จัก:
   - อย่ากด Move to Trash ไฟล์ไม่ได้พัง
   - คลิกขวาที่ Share Group แล้วเลือก เปิด
   - หรือเปิด Terminal แล้ววางคำสั่งนี้ แล้วเปิดแอปอีกครั้ง:

     xattr -dr com.apple.quarantine "/Applications/Share Group.app"

   - หรือไป System Settings → Privacy & Security แล้วกดเปิดแอปที่ถูกบล็อก

เซิร์ฟเวอร์เปิดที่พอร์ต 8080 ข้อมูลอยู่ที่ ~/LANShare
ยังไม่ได้ลงนาม (unsigned) เครื่องที่โหลดจากอินเทอร์เน็ตหรือ GitHub
(รวมเครื่องที่ build เอง) จะเจอ Gatekeeper เพราะ Chrome ติด quarantine ให้
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
