#!/bin/sh
# ผูกแพ็กเกจ mobilehost เป็น AAR ให้แอป Kotlin เรียก Start/Stop
set -e
ROOT="$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)"
AAR="$ROOT/mobile/android/app/libs/lanshare.aar"
export ANDROID_HOME="${ANDROID_HOME:-$HOME/Library/Android/sdk}"
export ANDROID_NDK_HOME="${ANDROID_NDK_HOME:-}"
if [ -z "$ANDROID_NDK_HOME" ]; then
  NDK="$(ls -d "$ANDROID_HOME"/ndk/* 2>/dev/null | sort | tail -n 1 || true)"
  if [ -n "$NDK" ]; then
    export ANDROID_NDK_HOME="$NDK"
  fi
fi
if [ -z "$JAVA_HOME" ]; then
  JBR="/Applications/Android Studio.app/Contents/jbr/Contents/Home"
  if [ -x "$JBR/bin/javac" ]; then
    export JAVA_HOME="$JBR"
  fi
fi
if [ ! -x "${JAVA_HOME:-}/bin/javac" ] && ! command -v javac >/dev/null 2>&1; then
  echo "ต้องมี JDK (javac) สำหรับ gomobile bind — เปิด Android Studio หรือตั้ง JAVA_HOME แล้วลองใหม่"
  exit 1
fi

GOBIN="$(go env GOPATH)/bin"
export PATH="$GOBIN:$PATH"
if [ ! -x "$GOBIN/gomobile" ] || [ ! -x "$GOBIN/gobind" ]; then
  echo "install gomobile → $GOBIN"
  go install golang.org/x/mobile/cmd/gomobile
  go install golang.org/x/mobile/cmd/gobind
fi
"$GOBIN/gomobile" init

mkdir -p "$(dirname "$AAR")"
echo "bind → $AAR"
(cd "$ROOT" && "$GOBIN/gomobile" bind -v -target android -androidapi 26 -o "$AAR" ./mobilehost)
echo "เสร็จ: เปิดโฟลเดอร์ mobile/android ใน Android Studio แล้ว Run"
echo "เปลือก Kotlin ห้ามใส่กฎห้อง — แดชบอร์ดคือ web/ ที่ http://127.0.0.1:8080"
