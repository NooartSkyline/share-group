#!/bin/bash
# capture.sh - เก็บ fixture ผลลัพธ์คำสั่ง macOS สำหรับ parser ของ diagnostics
#
# สคริปต์นี้ "อ่านอย่างเดียว" ไม่เปลี่ยนค่า Firewall หรือค่าใดของระบบ และไม่ใช้ sudo
# คำสั่งที่เรียกมีเพียง:
#   socketfilterfw --getglobalstate | --getblockall | --getappblocked <path>
#   networksetup -listallhardwareports
#   system_profiler SPAirPortDataType -json
#
# การใช้งาน:
#   ./capture.sh <case> <absolute-path-to-binary>
#   เช่น  ./capture.sh firewall-off /tmp/lanshare
#
# <case> คือชื่อสถานการณ์ (a-z, 0-9, -) ผลลัพธ์ถูกเขียนที่ testdata/<case>/
#   globalstate.txt     socketfilterfw --getglobalstate
#   blockall.txt        socketfilterfw --getblockall
#   appblocked.txt      socketfilterfw --getappblocked <path>   (path ถูกแทนด้วย /path/to/lanshare)
#   hardwareports.txt   networksetup -listallhardwareports      (MAC ถูกแทนด้วย 00:00:00:00:00:00)
#   wifi.json           system_profiler SPAirPortDataType -json  (เก็บเฉพาะชื่อ interface, สถานะ, channel)
#
# ข้อมูลระบุตัวตน (SSID, ชื่อเครือข่ายข้างเคียง, MAC, hostname, ชื่อผู้ใช้, serial)
# ไม่ถูกบันทึกโดยการสร้างจาก allow-list และมีการตรวจซ้ำก่อนเขียนไฟล์ ถ้าพบให้ยกเลิกทั้งหมด

set -euo pipefail

FW=/usr/libexec/ApplicationFirewall/socketfilterfw
PLACEHOLDER_BIN=/path/to/lanshare
PLACEHOLDER_MAC=00:00:00:00:00:00

die() {
  echo "capture.sh: $*" >&2
  exit 1
}

# เรียก socketfilterfw ได้เฉพาะ option --get* ที่ระบุไว้ (ห้าม --set* โดยเด็ดขาด)
fw() {
  case "${1:-}" in
    --getglobalstate | --getblockall | --getappblocked) ;;
    *) die "ปฏิเสธคำสั่ง socketfilterfw ที่ไม่อยู่ในรายการอ่านอย่างเดียว: ${1:-}" ;;
  esac
  "$FW" "$@"
}

[ "$(uname -s)" = "Darwin" ] || die "ใช้ได้บน macOS เท่านั้น"
command -v jq > /dev/null 2>&1 || die "ต้องมี jq (macOS 15 ขึ้นไปมีมากับระบบที่ /usr/bin/jq)"
[ -x "$FW" ] || die "ไม่พบ $FW"
[ "$#" -eq 2 ] || die "usage: $0 <case> <absolute-path-to-binary>"

CASE=$1
BIN=$2
case "$CASE" in
  '' | *[!a-z0-9-]*) die "ชื่อ case ต้องเป็น a-z, 0-9 และ - เท่านั้น" ;;
esac
case "$BIN" in
  /*) ;;
  *) die "พาธของไบนารีต้องเป็นพาธเต็ม (ขึ้นต้นด้วย /)" ;;
esac

DIR=$(cd "$(dirname "$0")" && pwd)
OUT="$DIR/$CASE"

# --- เก็บผลดิบไว้ในตัวแปรเท่านั้น ไม่เขียนลงดิสก์ ---
raw_global=$(fw --getglobalstate) || die "--getglobalstate ล้มเหลว"
raw_blockall=$(fw --getblockall) || die "--getblockall ล้มเหลว"
raw_app=$(fw --getappblocked "$BIN") || die "--getappblocked ล้มเหลว"
raw_ports=$(networksetup -listallhardwareports) || die "networksetup ล้มเหลว"
raw_wifi=$(system_profiler SPAirPortDataType -json 2> /dev/null) || die "system_profiler ล้มเหลว"
printf '%s' "$raw_wifi" | jq -e . > /dev/null || die "system_profiler ไม่ได้คืน JSON ที่ถูกต้อง"

# --- ตัดข้อมูลระบุตัวตนออก ---
out_global=$raw_global
out_blockall=$raw_blockall
out_app=${raw_app//"$BIN"/"$PLACEHOLDER_BIN"}

# เก็บเฉพาะบรรทัดที่ parser ใช้ ชื่อ port ที่ไม่ใช่ชนิดมาตรฐานอาจเป็นชื่อที่ผู้ใช้ตั้งเอง จึงแทนด้วย Other
out_ports=$(printf '%s\n' "$raw_ports" | awk -v mac="$PLACEHOLDER_MAC" '
  /^Hardware Port: / {
    name = substr($0, 16)
    if (name ~ /^(Wi-Fi|Ethernet.*|Thunderbolt.*|USB.*|iPhone USB|iPad USB|Bluetooth PAN|FireWire|AirPort)$/) {
      print $0
    } else {
      print "Hardware Port: Other"
    }
    next
  }
  /^Device: / {
    if ($0 ~ /^Device: [a-z]+[0-9]+$/) print $0; else print "Device: (null)"
    next
  }
  /^Ethernet Address: / { print "Ethernet Address: " mac; next }
  /^VLAN Configurations$/ { print; next }
  /^=+$/ { print; next }
  /^$/ { print; next }
')

# allow-list: ชื่อ interface, สถานะการเชื่อมต่อ และสตริง channel ของเครือข่ายปัจจุบันเท่านั้น
# ไม่เก็บ SSID, เครือข่ายข้างเคียง, MAC, country code, firmware
out_wifi=$(printf '%s' "$raw_wifi" | jq -S '
  {
    SPAirPortDataType: [
      {
        spairport_airport_interfaces: [
          (.SPAirPortDataType[0].spairport_airport_interfaces // [])[]
          | {
              _name: (if ((._name // "") | test("^[a-z]+[0-9]+$")) then ._name else "redacted" end),
              spairport_status_information: .spairport_status_information,
              spairport_current_network_information: (
                if .spairport_current_network_information.spairport_network_channel != null
                then { spairport_network_channel: .spairport_current_network_information.spairport_network_channel }
                else null end
              )
            }
          | with_entries(select(.value != null))
        ]
      }
    ]
  }') || die "สร้าง wifi.json ไม่สำเร็จ"

# --- ตรวจซ้ำ: ห้ามมีข้อมูลระบุตัวตนหลงเหลือ ---
sensitive=$(
  {
    printf '%s' "$raw_wifi" | jq -r '
      (.SPAirPortDataType[0].spairport_airport_interfaces // [])[]
      | (.spairport_current_network_information._name?),
        (.spairport_airport_other_local_wireless_networks[]?._name?),
        (.spairport_wireless_mac_address?)
      | select(. != null)' 2> /dev/null || true
    hostname 2> /dev/null || true
    hostname -s 2> /dev/null || true
    scutil --get ComputerName 2> /dev/null || true
    scutil --get LocalHostName 2> /dev/null || true
    scutil --get HostName 2> /dev/null || true
    id -un 2> /dev/null || true
    printf '%s\n' "$HOME"
    ioreg -rd1 -c IOPlatformExpertDevice 2> /dev/null \
      | awk -F'"' '/IOPlatformSerialNumber|IOPlatformUUID/ { print $4 }' || true
  } | awk 'length($0) >= 3' | sort -u
)

bad=0
check_file_content() { # <label> <content>
  local label=$1 content=$2 s
  while IFS= read -r s; do
    [ -n "$s" ] || continue
    if printf '%s\n' "$content" | grep -Fqi -- "$s"; then
      echo "capture.sh: พบข้อมูลระบุตัวตนในผลลัพธ์ของ $label (ยกเลิก)" >&2
      bad=1
    fi
  done <<< "$sensitive"
  # MAC ใดๆ ที่ไม่ใช่ค่า placeholder
  if printf '%s\n' "$content" | grep -Eio '([0-9a-f]{2}[:-]){5}[0-9a-f]{2}' | grep -vqx "$PLACEHOLDER_MAC"; then
    echo "capture.sh: พบรูปแบบ MAC address ในผลลัพธ์ของ $label (ยกเลิก)" >&2
    bad=1
  fi
}
check_file_content globalstate "$out_global"
check_file_content blockall "$out_blockall"
check_file_content appblocked "$out_app"
check_file_content hardwareports "$out_ports"
check_file_content wifi "$out_wifi"
[ "$bad" -eq 0 ] || exit 1

# --- เขียนไฟล์ ---
mkdir -p "$OUT"
printf '%s\n' "$out_global" > "$OUT/globalstate.txt"
printf '%s\n' "$out_blockall" > "$OUT/blockall.txt"
printf '%s\n' "$out_app" > "$OUT/appblocked.txt"
printf '%s\n' "$out_ports" > "$OUT/hardwareports.txt"
printf '%s\n' "$out_wifi" > "$OUT/wifi.json"

echo "บันทึก fixture ที่ $OUT"
echo "กรุณาเปิดอ่านไฟล์ทุกไฟล์ด้วยตาก่อน commit"
