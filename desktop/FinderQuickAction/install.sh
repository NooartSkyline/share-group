#!/bin/sh
# ติดตั้ง Finder Quick Action "ส่งไป LAN Share" และคำสั่ง share บนเครื่องนี้
set -e
ROOT="$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)"
BIN="$HOME/LANShare/bin"
SERVICES="$HOME/Library/Services"
WORKFLOW_SRC="$ROOT/desktop/FinderQuickAction/LAN Share.workflow"
WORKFLOW_DST="$SERVICES/ส่งไป LAN Share.workflow"

mkdir -p "$BIN"
echo "build share → $BIN/share"
(cd "$ROOT" && go build -o "$BIN/share" ./cmd/share)

mkdir -p "$SERVICES"
rm -rf "$WORKFLOW_DST"
cp -R "$WORKFLOW_SRC" "$WORKFLOW_DST"

if [ -x /System/Library/CoreServices/pbs ]; then
  /System/Library/CoreServices/pbs -flush >/dev/null 2>&1 || true
fi

echo "ติดตั้งแล้ว: $WORKFLOW_DST"
echo "ใน Finder คลิกขวาไฟล์ → Quick Actions / Services → ส่งไป LAN Share"
echo "เซิร์ฟเวอร์ LAN Share ต้องเปิดอยู่บนเครื่องนี้"
