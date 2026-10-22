# LAN Share

เซิร์ฟเวอร์แชร์ไฟล์ในวง LAN รันบน Mac แล้วให้อุปกรณ์อื่นเข้าผ่านเบราว์เซอร์ ไม่ต้องติดตั้งแอป

ค่าเริ่มต้นเป็น HTTP ธรรมดา ใช้ในวงที่ไว้ใจกันได้ ถ้าจะเปิดให้คนอื่นเข้า ให้ใช้ `-tls` และเปิดอนุมัติอุปกรณ์ในหน้า Owner

## หน้าตา

ภาพจากต้นแบบ [`mock/ui-mock-v2.html`](mock/ui-mock-v2.html)

แดชบอร์ดบน Mac

![แดชบอร์ด LAN Share](docs/ui/dashboard.png)

ฝั่งมือถือในเบราว์เซอร์: กรอก PIN, ส่งไฟล์, ข้อความ และลิงก์แขก

![หน้าจอมือถือ](docs/ui/mobile.png)

ตรวจปัญหาการเชื่อมต่อ และกล่องถามเมื่อชื่อไฟล์ซ้ำ

![ตรวจปัญหาการเชื่อมต่อ](docs/ui/diag.png)

จัดการห้อง บทบาท และลิงก์แขก

![จัดการห้อง](docs/ui/rooms.png)

Popover จากไอคอน menu bar

![menu bar](docs/ui/menu.png)

## สิ่งที่ต้องมี

- macOS (เครื่องที่รันเซิร์ฟเวอร์)
- Go 1.27 ขึ้นไป

## รัน

จากโฟลเดอร์โปรเจกต์:

```bash
sh scripts/run-local.sh
```

Wi-Fi และสาย USB เข้าเซิร์ฟเวอร์ตัวเดียวกัน กรอก PIN เดียวกัน ถ้ามีเซิร์ฟเวอร์ทำงานอยู่แล้ว (เช่น แอป menu bar ที่พอร์ต `8080`) สคริปต์จะไม่เปิดตัวใหม่ แค่เชื่อมสาย USB เข้าเซิร์ฟเวอร์นั้น มือถือที่เสียบสายเปิด `http://127.0.0.1:8080/?r=main` ได้โดยไม่ต้องมีเน็ต

ถ้ายังไม่มีเซิร์ฟเวอร์ จะ build แล้วรันจากซอร์สที่พอร์ต `8080` ข้อมูลอยู่ที่ `./tmp/local` หยุดด้วย Ctrl+C

หรือรันตรง ๆ:

```bash
go run ./cmd/lanshare
```

เซิร์ฟเวอร์เปิดที่พอร์ต `8080` และเก็บไฟล์กับสถานะไว้ที่ `~/LANShare` ตอนเริ่มจะพิมพ์ที่อยู่ของเครื่องนี้, URL สำหรับอุปกรณ์อื่น, QR code, ชื่อห้อง และ PIN

บน Mac เปิด `http://localhost:8080` แล้วกรอก PIN ที่เห็นในเทอร์มินัล

อุปกรณ์อื่นต้องอยู่ Wi-Fi เดียวกัน เปิด URL ที่พิมพ์ออกมา (รูป `http://<IP>:8080/?r=main`) หรือสแกน QR แล้วกรอก PIN เดียวกัน

หยุดด้วย Ctrl+C

## คำสั่งที่ใช้บ่อย

```bash
go run ./cmd/lanshare -port 9000 -data ./tmp
go run ./cmd/lanshare -iface en0
go run ./cmd/lanshare -h
```

| Flag | ค่าเริ่มต้น | ความหมาย |
|---|---|---|
| `-port` | `8080` | พอร์ต HTTP |
| `-data` | `~/LANShare` | โฟลเดอร์เก็บไฟล์และสถานะ |
| `-iface` | อัตโนมัติ | การ์ดเครือข่ายที่โฆษณาให้เครื่องอื่น เช่น `en0` |
| `-room-name` | `ห้องหลัก` | ชื่อห้องเริ่มต้น |
| `-min-free` | `1GiB` | พื้นที่ว่างที่ต้องเหลือหลังรับไฟล์ หน่วยเป็น `B`, `KiB`, `MiB` หรือ `GiB` |
| `-upload-ttl` | `24h` | ระยะเวลาเก็บอัปโหลดที่ยังไม่เสร็จ อย่างน้อย `1m` |
| `-stall-timeout` | `30s` | ตัดคำขออัปโหลดที่ไม่มีข้อมูลเข้า อย่างน้อย `5s` |
| `-keep-awake` | `true` | กัน Mac หลับระหว่างมีการโอน ปิดด้วย `-keep-awake=false` |
| `-max-pin-failures` | `5` | จำนวนครั้งที่กรอก PIN ผิดก่อนถูกล็อก |
| `-lockout` | `5m0s` | ระยะเวลาล็อกหลังกรอก PIN ผิดเกินกำหนด |
| `-tls` | `false` | เปิด HTTPS ด้วยใบรับรอง self-signed ในโฟลเดอร์ข้อมูล |

ตัวอย่าง:

```bash
go run ./cmd/lanshare -min-free 5GiB -upload-ttl 48h -stall-timeout 60s -keep-awake=false
```

## สร้างไบนารี

```bash
go build -o lanshare ./cmd/lanshare
./lanshare
```

หน้าเว็บถูกฝังในไบนารีแล้ว จึงรันจากโฟลเดอร์อื่นได้

## ส่งไฟล์จากเทอร์มินัล

เซิร์ฟเวอร์ต้องเปิดอยู่บนเครื่องนี้ แล้ว:

```bash
go run ./cmd/share send photo.jpg
go run ./cmd/share send -port 9000 ./report.pdf
```

คำสั่งนี้เข้าเป็น Owner ผ่าน `localhost` แล้วอัปโหลดไฟล์เข้าห้องปัจจุบัน

ติดตั้ง Finder Quick Action:

```bash
sh desktop/FinderQuickAction/install.sh
```

จากนั้นใน Finder คลิกขวาไฟล์ → Quick Actions / Services → **ส่งไป LAN Share** (เซิร์ฟเวอร์ต้องเปิดอยู่)

## แอป menu bar

สร้างและเปิดไอคอนบนแถบเมนู (ไม่โผล่ที่ Dock) ด้วย Swift AppKit:

```bash
sh desktop/LanShareMenu/install.sh
```

เมนูแสดงห้อง, PIN, เครือข่าย, จำนวนอุปกรณ์ และอุปกรณ์ที่รออนุมัติ กดเปิดแดชบอร์ดหรือเปิดเมื่อเข้าสู่ระบบได้จากเมนู

## Shortcuts และ HTTP บนเครื่องนี้

เรียกได้เฉพาะจาก `127.0.0.1` / `localhost` ไม่ต้องใส่ PIN:

| Method และ path | หน้าที่ |
|---|---|
| `GET /api/shortcuts/info` | ห้อง, PIN, URL, โควตา, จำนวนอุปกรณ์ |
| `GET /api/shortcuts/qr.png` | QR ของลิงก์เข้าห้อง |
| `GET /api/shortcuts/files` | รายการไฟล์ในห้องปัจจุบัน |
| `POST /api/shortcuts/files?name=photo.jpg` | อัปโหลดเนื้อหาไฟล์จาก body |

ในแอป Shortcuts ใช้ Get Contents of URL ชี้ `http://127.0.0.1:8080/...`

รูป HEIC/HEIF ที่อัปโหลดบน Mac จะถูกแปลงเป็น JPG อัตโนมัติด้วย `sips` หลังส่งเสร็จ ไฟล์ข้อความเปิดดูในหน้าเว็บพร้อมระบายสี syntax

## HTTPS

```bash
go run ./cmd/lanshare -tls
```

ใบรับรองอยู่ที่ `~/LANShare/tls` เบราว์เซอร์จะเตือน self-signed ให้กดดำเนินการต่อ บน iOS อาจต้องติดตั้งโปรไฟล์ใบรับรองเอง

## ทดสอบ

```bash
go test ./...
```
