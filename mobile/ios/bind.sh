#!/bin/sh
# ผูกแพ็กเกจ mobilehost เป็น xcframework ให้แอป Swift เรียก Start/Stop
set -e
ROOT="$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)"
OUT="$ROOT/mobile/ios/Mobilehost.xcframework"

GOBIN="$(go env GOPATH)/bin"
export PATH="$GOBIN:$PATH"
if [ ! -x "$GOBIN/gomobile" ] || [ ! -x "$GOBIN/gobind" ]; then
  echo "install gomobile → $GOBIN"
  go install golang.org/x/mobile/cmd/gomobile
  go install golang.org/x/mobile/cmd/gobind
fi
"$GOBIN/gomobile" init

echo "bind → $OUT"
(cd "$ROOT" && "$GOBIN/gomobile" bind -v -target ios -iosversion 15.0 -o "$OUT" ./mobilehost)
echo "เสร็จ: เปิด mobile/ios/ShareGroupHost.xcodeproj ใน Xcode แล้ว Run"
echo "เปลือก Swift ห้ามใส่กฎห้อง — แดชบอร์ดคือ web/ ที่ http://127.0.0.1:8080"
echo "โฮสต์ iOS ใช้ได้ตอนแอปเปิดอยู่ ไม่มีไฟล์บนเครื่อง / OTG"
