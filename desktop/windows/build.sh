#!/bin/sh
# สร้างถาดระบบ Windows (Go) และไบนารี lanshare ให้วางในโฟลเดอร์เดียวกัน
set -e
ROOT="$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)"
OUT="$ROOT/desktop/windows/build"
mkdir -p "$OUT"

echo "build tray → $OUT/ShareGroup.exe"
(cd "$ROOT" && GOOS=windows GOARCH=amd64 go build -ldflags="-H=windowsgui" -o "$OUT/ShareGroup.exe" ./desktop/windows)

echo "build lanshare → $OUT/lanshare.exe"
(cd "$ROOT" && GOOS=windows GOARCH=amd64 go build -o "$OUT/lanshare.exe" ./cmd/lanshare)

echo "build share → $OUT/share.exe"
(cd "$ROOT" && GOOS=windows GOARCH=amd64 go build -o "$OUT/share.exe" ./cmd/share)

cp "$ROOT/desktop/windows/install.cmd" "$OUT/install.cmd"
cp "$ROOT/desktop/windows/uninstall.cmd" "$OUT/uninstall.cmd"

echo "เสร็จ: $OUT"
echo "บน Windows ให้วาง ShareGroup.exe กับ lanshare.exe และ share.exe ไว้โฟลเดอร์เดียวกัน แล้วเปิด ShareGroup.exe"
echo "คลิกขวา Explorer: รัน install.cmd (เทียบ Finder Quick Action)"
echo "แดชบอร์ดเปิดในเบราว์เซอร์ที่ http://127.0.0.1:8080 — ยังไม่มี WebView2 ในรอบนี้"
