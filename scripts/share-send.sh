#!/bin/sh
# ส่งไฟล์เข้า LAN Share บนเครื่องนี้ ใช้เป็นต้นทางของ Finder Quick Action ได้:
# Automator → Quick Action → "Run Shell Script" (Pass input: as arguments)
# แล้วเรียกสคริปต์นี้พร้อม "$@"
set -e
ROOT="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
if [ -x "$HOME/LANShare/bin/share" ]; then
  exec "$HOME/LANShare/bin/share" send "$@"
fi
if [ -x "$ROOT/share" ]; then
  exec "$ROOT/share" send "$@"
fi
exec go run "$ROOT/cmd/share" send "$@"
