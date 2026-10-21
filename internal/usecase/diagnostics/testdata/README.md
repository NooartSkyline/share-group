# Fixture ผลลัพธ์คำสั่ง macOS สำหรับ diagnostics

ไฟล์ในโฟลเดอร์นี้คือข้อความจริงที่คำสั่งของ macOS พิมพ์ออกมา ใช้เป็นข้อมูลทดสอบของ parser ใน `internal/usecase/diagnostics` (checks `firewall` และ `wifi_band`) parser ต้องคืน `unknown` พร้อม reason `parse_failed` เมื่อเจอข้อความที่ไม่รู้จัก จึงควรเก็บข้อความจริงของทุกกรณีไว้ที่นี่ ไม่เดาจากความจำ

## สถานะ fixture

| โฟลเดอร์ | สถานการณ์ | สถานะ |
|---|---|---|
| `firewall-off/` | Firewall ปิด (State = 0), โปรแกรมไม่ถูกบล็อก | เก็บแล้ว (macOS 26.x, Wi-Fi `en0` ที่ 5GHz) |
| `firewall-on-permitted/` | Firewall เปิด และโปรแกรมถูกอนุญาต | ยังไม่ได้เก็บ (ผู้ใช้ต้องทำเอง) |
| `firewall-on-blocked/` | Firewall เปิด และโปรแกรมถูกบล็อก | ยังไม่ได้เก็บ (ผู้ใช้ต้องทำเอง) |
| `firewall-on-unlisted/` | Firewall เปิด และโปรแกรมไม่อยู่ในรายการ | ยังไม่ได้เก็บ (ผู้ใช้ต้องทำเอง) |
| `firewall-on-blockall/` | (ไม่บังคับ) Firewall เปิดและเปิด "Block all incoming connections" | ยังไม่ได้เก็บ |

แต่ละโฟลเดอร์มี

- `globalstate.txt` จาก `socketfilterfw --getglobalstate`
- `blockall.txt` จาก `socketfilterfw --getblockall`
- `appblocked.txt` จาก `socketfilterfw --getappblocked <path>` (path ถูกแทนด้วย `/path/to/lanshare`)
- `hardwareports.txt` จาก `networksetup -listallhardwareports` (MAC ถูกแทนด้วย `00:00:00:00:00:00`)
- `wifi.json` จาก `system_profiler SPAirPortDataType -json` (เหลือเฉพาะชื่อ interface, สถานะการเชื่อมต่อ และสตริง channel เช่น `149 (5GHz, 20MHz)`)

## ความปลอดภัยของสคริปต์

`capture.sh` อ่านอย่างเดียว

- เรียก `socketfilterfw` ได้เฉพาะ `--getglobalstate`, `--getblockall`, `--getappblocked` (option อื่นถูกปฏิเสธในสคริปต์)
- ไม่ใช้ `sudo` ไม่เปลี่ยนค่า Firewall หรือค่าอื่นของระบบ
- เก็บผลดิบไว้ในตัวแปรของสคริปต์เท่านั้น ไม่เขียนลงดิสก์
- ข้อมูลระบุตัวตนไม่ถูกบันทึกเพราะสร้างผลลัพธ์จาก allow-list แล้วตรวจซ้ำก่อนเขียนไฟล์: ไม่มี SSID, ชื่อเครือข่ายข้างเคียง, MAC, hostname, ชื่อผู้ใช้/home, serial/UUID ของเครื่อง ถ้าพบสิ่งเหล่านี้ในผลลัพธ์ สคริปต์จะยกเลิกโดยไม่เขียนไฟล์
- ต้องมี `jq` (macOS 15 ขึ้นไปมีมากับระบบที่ `/usr/bin/jq`)

ก่อน commit ให้เปิดอ่านไฟล์ที่ได้ด้วยตาอีกครั้งเสมอ

## วิธีเก็บกรณี "Firewall เปิด" (ผู้ใช้เป็นผู้เปลี่ยนค่าเอง)

เอเจนต์และสคริปต์ไม่แก้ค่า Firewall ให้ ทุกขั้นตอนที่เปลี่ยนค่าทำโดยผู้ใช้ใน System Settings

1. สร้างไบนารีไว้ในที่ที่ไม่ต้องการเก็บถาวร

   ```bash
   go build -o /tmp/lanshare-fx/lanshare ./cmd/lanshare
   ```

2. เปิด Firewall: System Settings > Network > Firewall > เปิดสวิตช์ Firewall
3. เลือกกรณีที่ต้องการแล้วรันสคริปต์ (พาธของไบนารีต้องเป็นพาธเต็ม)

   - **โปรแกรมถูกอนุญาต**: กด Options... > ปุ่ม `+` > เลือก `/tmp/lanshare-fx/lanshare` > ตั้งเป็น "Allow incoming connections" แล้วรัน

     ```bash
     internal/usecase/diagnostics/testdata/capture.sh firewall-on-permitted /tmp/lanshare-fx/lanshare
     ```

   - **โปรแกรมถูกบล็อก**: เปลี่ยนรายการเดิมเป็น "Block incoming connections" แล้วรัน

     ```bash
     internal/usecase/diagnostics/testdata/capture.sh firewall-on-blocked /tmp/lanshare-fx/lanshare
     ```

   - **โปรแกรมไม่อยู่ในรายการ**: ลบรายการนั้นออกจาก Options... (หรือคัดลอกไบนารีไปพาธใหม่ที่ไม่เคยเพิ่ม เช่น `/tmp/lanshare-fx2/lanshare`) แล้วรัน

     ```bash
     internal/usecase/diagnostics/testdata/capture.sh firewall-on-unlisted /tmp/lanshare-fx2/lanshare
     ```

   - **(ไม่บังคับ) Block all**: เปิด "Block all incoming connections" แล้วรัน `capture.sh firewall-on-blockall <path>`

4. เปิดอ่านไฟล์ในโฟลเดอร์ที่ได้ ตรวจว่าไม่มีชื่อ Wi-Fi ชื่อเครื่อง หรือ MAC หลงเหลือ
5. คืนค่า Firewall เป็นแบบเดิมที่ต้องการ และลบไบนารีชั่วคราว (`rm -rf /tmp/lanshare-fx /tmp/lanshare-fx2`)
6. แจ้งผู้พัฒนาว่าเก็บ fixture ครบแล้ว เพื่อเขียน parser ให้ตรงกับข้อความจริง

หมายเหตุ

- `socketfilterfw` ติดตามโปรแกรมตามพาธและลายเซ็น ถ้าสถานะที่ได้ไม่ตรงกรณีที่ต้องการ ให้ตรวจ Options... อีกครั้ง
- ถ้าต้องการเก็บกรณี Wi-Fi 2.4GHz หรือเครื่องที่ไม่ได้ต่อ Wi-Fi ให้รัน `capture.sh` ด้วยชื่อ case ใหม่ (เช่น `wifi-2ghz`) ตอนเชื่อมต่อในสภาพนั้น
