#!/bin/sh
# สร้างไฟล์ติดตั้งโฮสต์ Mac / Android / Windows แล้ววางใน dist/<เวอร์ชัน>/
# รุ่นเก่าใน dist/ ไม่ถูกลบ
set -e
ROOT="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
if [ -z "${VERSION:-}" ]; then
  VERSION="$(tr -d '[:space:]' < "$ROOT/VERSION")"
fi
DEST="$ROOT/dist/$VERSION"
mkdir -p "$DEST"

echo "=== Share Group $VERSION → $DEST ==="

echo "--- macOS DMG ---"
VERSION="$VERSION" sh "$ROOT/desktop/LanShareMenu/dmg.sh" "$DEST/Share Group-$VERSION.dmg"

echo "--- Windows amd64 ---"
sh "$ROOT/desktop/windows/build.sh"
WIN="$ROOT/desktop/windows/build"
WIN_ZIP="$DEST/Share-Group-$VERSION-windows-amd64.zip"
rm -f "$WIN_ZIP"
(
  cd "$WIN"
  zip -9 -r "$WIN_ZIP" ShareGroup.exe lanshare.exe share.exe install.cmd uninstall.cmd
)

echo "--- Android APK ---"
sh "$ROOT/mobile/android/bind.sh"
(
  cd "$ROOT/mobile/android"
  ./gradlew assembleDebug
)
cp "$ROOT/mobile/android/app/build/outputs/apk/debug/app-debug.apk" \
  "$DEST/Share-Group-$VERSION-android.apk"

echo "เสร็จ $VERSION"
ls -lh "$DEST"
