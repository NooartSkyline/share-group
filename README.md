# LAN Share

เซิร์ฟเวอร์แชร์ไฟล์ในวง LAN รันบน Mac แล้วให้อุปกรณ์อื่นเข้าผ่านเบราว์เซอร์ ไม่ต้องติดตั้งแอป

ค่าเริ่มต้นเป็น HTTP ธรรมดา ใช้ในวงที่ไว้ใจกันได้ ถ้าจะเปิดให้คนอื่นเข้า ให้ใช้ `-tls` และเปิดอนุมัติอุปกรณ์ในหน้า Owner

## หน้าตา

ภาพจากต้นแบบ [`mock/ui-mock-v2.html`](mock/ui-mock-v2.html) หน้า menu bar ยังเป็นต้นแบบของเฟสที่ยังไม่ทำ

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

คำสั่งนี้เข้าเป็น Owner ผ่าน `localhost` แล้วอัปโหลดไฟล์เข้าห้องปัจจุบัน สคริปต์ [`scripts/share-send.sh`](scripts/share-send.sh) ใช้เป็นต้นทางของ Finder Quick Action ได้ (Automator → Quick Action → Run Shell Script ส่งไฟล์เป็น arguments)

## HTTPS

```bash
go run ./cmd/lanshare -tls
```

ใบรับรองอยู่ที่ `~/LANShare/tls` เบราว์เซอร์จะเตือน self-signed ให้กดดำเนินการต่อ บน iOS อาจต้องติดตั้งโปรไฟล์ใบรับรองเอง

## ทดสอบ

```bash
go test ./...
```
