#!/bin/sh
# รัน Share Group จากซอร์ส — Wi-Fi และสาย USB เข้าเซิร์ฟเวอร์ตัวเดียวกัน
# ถ้ามีเซิร์ฟเวอร์ทำงานอยู่แล้ว (เช่น แอป menu bar ที่ :8080) จะไม่เปิดตัวใหม่
# แค่เชื่อมสาย USB เข้าเซิร์ฟเวอร์นั้น ให้มือถือกรอก PIN เดียวกับ Wi-Fi
set -e
ROOT="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
PORT="${PORT:-8080}"
DATA="${DATA:-$ROOT/tmp/local}"
BIN="$ROOT/tmp/lanshare"

adb_bin() {
  if command -v adb >/dev/null 2>&1; then
    command -v adb
    return
  fi
  for p in \
    /opt/homebrew/bin/adb \
    /usr/local/bin/adb \
    "$HOME/Library/Android/sdk/platform-tools/adb" \
    "$HOME/Android/Sdk/platform-tools/adb"
  do
    if [ -x "$p" ]; then
      printf '%s\n' "$p"
      return
    fi
  done
  return 1
}

# รุ่นที่ติดตั้งแล้วอาจยังไม่มี /api/shortcuts/info — ถือว่าขึ้นแล้วถ้ามี HTTP ตอบ
server_up() {
  curl -s -o /dev/null --max-time 1 "http://127.0.0.1:$1/"
}

port_busy() {
  lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
}

attach_usb() {
  port="$1"
  if ! ADB="$(adb_bin)"; then
    echo "ไม่พบ adb — เสียบ Android แล้วติดตั้ง platform-tools จึงจะเข้าผ่านสายได้" >&2
    return 1
  fi
  if "$ADB" reverse "tcp:$port" "tcp:$port" >/dev/null 2>&1; then
    echo "สาย USB ใช้เซิร์ฟเวอร์เดิมที่พอร์ต $port — ไม่ได้เปิดเซิร์ฟเวอร์ใหม่"
    echo "มือถือเปิด http://127.0.0.1:$port/?r=main แล้วกรอก PIN เดียวกับ Wi-Fi"
    return 0
  fi
  echo "ยังเชื่อมสายไม่ได้ — เสียบมือถือ เปิด USB debugging แล้วกด Trust" >&2
  return 1
}

# ถ้ามีเซิร์ฟเวอร์อยู่แล้ว ให้ USB เข้าตัวนั้น อย่าเปิดโปรเซสใหม่
for p in 8080 "$PORT"; do
  if server_up "$p"; then
    attach_usb "$p" || true
    exit 0
  fi
done

if port_busy "$PORT"; then
  echo "พอร์ต $PORT ถูกใช้อยู่แล้ว แต่เซิร์ฟเวอร์ไม่ตอบ HTTP — ปิด Share Group.app แล้วลองใหม่" >&2
  lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >&2 || true
  exit 1
fi

mkdir -p "$DATA" "$(dirname "$BIN")"
echo "build → $BIN"
(cd "$ROOT" && go build -o "$BIN" ./cmd/lanshare)

if ADB="$(adb_bin)"; then
  "$ADB" reverse "tcp:$PORT" "tcp:$PORT" >/dev/null 2>&1 || true
fi

echo "เจ้าของ: http://127.0.0.1:$PORT"
echo "USB: มือถือเปิด http://127.0.0.1:$PORT/?r=main กรอก PIN เดียวกับ Wi-Fi (เซิร์ฟเวอร์เดียวกัน)"
echo "ข้อมูล:  $DATA"
echo "หยุดด้วย Ctrl+C"
exec "$BIN" -port "$PORT" -data "$DATA" "$@"
