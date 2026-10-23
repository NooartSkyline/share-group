# โครงสร้างโปรเจกต์

ใช้ Clean Architecture ในภาษา Go กฎการพึ่งพา: ชั้นนอกรู้จักชั้นใน แต่ชั้นในห้ามรู้จักชั้นนอก

```
cmd (composition root) -> adapter -> usecase -> domain
                          infra  ->  implements ports declared by usecase
```

```
cmd/lanshare/main.go        CLI: โหลด flag แล้วเรียก internal/app (composition root ที่ mobile ใช้ร่วม)
internal/app/               Composition root ที่ใช้ร่วม: ประกอบ adapter แล้วเปิด HTTP
mobilehost/                 API ที่ gomobile bind ได้: Start/Stop/URL ไม่มีกฎห้อง
internal/
  domain/                   เฉพาะ entity และกฎทางธุรกิจ: Room, Device, File, Chat/Message/Clip, Network, errors ห้ามมี HTTP, JSON, SQL หรือ disk
  usecase/
    port/                   Interface ที่ use case ต้องใช้ (RoomRepository, FileStore, TokenService, EventPublisher, Clock, ...)
    auth/ room/ device/     ล็อกอินด้วย PIN, lockout, token; จัดการห้อง; สถานะการออนไลน์ (presence)
    transfer/ chat/         อัปโหลด/ดาวน์โหลดแบบ streaming (รวม resumable upload), thumbnail, zip; ข้อความและคลิปบอร์ด
    network/                เลือก LAN interface และสร้าง URL สำหรับเข้าร่วม
    power/                  Power_Guard กัน Mac หลับระหว่างมีการโอน
    storage/                โควตาและพื้นที่ว่างขั้นต่ำ (disk guard)
    diagnostics/            หน้าตรวจปัญหาการเชื่อมต่อสำหรับ Owner (มี testdata/)
    fake/                   Test double แบบ in-memory ของ port
  adapter/
    httpapi/                Router (server.go), handler, middleware, DTO, uploads.go (resumable), admin.go (Owner/diagnostics)
    ws/                     WebSocket hub (implement EventPublisher)
    filestore/              เนื้อหาไฟล์บน disk แยกโฟลเดอร์ตามห้อง รวม partial/ (FileStore, ThumbStore)
    jsonstore/              Repository แบบ state.json (ห้อง, ไฟล์)
    sessionstore/           สถานะ Upload_Session ต่อเซสชันใต้ sessions/
    memory/                 อุปกรณ์, token, rate limiter, แชท แบบ in-memory
    caffeinate/ cmdrunner/  กัน Mac หลับด้วยคำสั่ง caffeinate / รันคำสั่งภายนอกแบบมี context+timeout
    diskspace/ peerlog/     พื้นที่ว่างของดิสก์ (statfs) / บันทึกเหตุการณ์การเชื่อมต่อเพื่อวินิจฉัย
    netinfo/ qr/ thumb/     Network interface, QR PNG/terminal, thumbnail ของรูปภาพ
    hostfs/                 ไฟล์บนเครื่องโฮสต์ Android และ USB OTG (HostBrowser)
  infra/
    config/                 CLI flag -> Config (config.go, size.go)
    system/                 Clock และ IDGenerator ตัวจริง
web/                        UI บนเบราว์เซอร์ ฝังด้วย embed.go (ดูรายการไฟล์ใน tech.md) web/test/ ห้ามฝัง
tools/genicons/             เครื่องมือสร้างไอคอน PWA และไอคอนแอป Mac (โลโก้ไฟล์กลางวงกลุ่ม)
desktop/LanShareMenu/       เปลือก menu bar บน Mac (Swift AppKit) เปิด/ปิดเซิร์ฟเวอร์ Go ไม่ใส่กฎห้อง
desktop/windows/            เปลือกถาดระบบ Windows เป็น Go เปิด/ปิด lanshare.exe แดชบอร์ดในเบราว์เซอร์ คลิกขวา Explorer เรียก share send (Phase I)
mobile/android/             เปลือกแอป Android เป็น Kotlin + WebView + foreground service ห่อ mobilehost (Phase I)
mobile/ios/                 เปลือกแอป iOS เป็น Swift + WKWebView ห่อ mobilehost (Phase I ใช้ได้ตอนแอปเปิดอยู่)
docs/project-scope.md       ขอบเขต การตัดสินใจ เฟส สถานะ ระบบ นักพัฒนา และ Mock UI (ภาษาไทย หัวข้อ 12–15)
dist/<เวอร์ชัน>/            ไฟล์ติดตั้งโฮสต์ที่แจก (Mac .dmg, Android .apk, Windows .zip) รุ่นเก่าเก็บแยกโฟลเดอร์
VERSION                     เลขเวอร์ชันปัจจุบัน ใช้ตอนแพ็ก `scripts/package-hosts.sh`
mock/                       ต้นแบบ UI แบบ HTML (ui-mock-v3.html คือตัวปัจจุบัน; v2 เก็บเทียบ; ui-mock-hosts.html คือโฮสต์ Phase I; ui-mock.html เลิกใช้แล้ว)
```

## กฎของแต่ละชั้น
- `domain` ห้าม import อะไรจากโปรเจกต์นี้ `usecase` import ได้เฉพาะ `domain` และ `usecase/port`
- `usecase` ห้าม import `adapter` หรือ `infra` และห้ามใช้ `net/http`, SQL หรือ filesystem โดยตรง
- Port ต้องประกาศใน `usecase/port` (ฝั่งผู้ใช้งาน) ไม่ใช่ข้างๆ ตัว implementation
- การแปลงจาก/ไปเป็น JSON/DTO ทำใน `adapter` เท่านั้น ให้ type ใน `domain` ปลอดจากเรื่อง JSON/SQL
- Port ที่เกี่ยวกับเนื้อหาไฟล์รับ `io.Reader` / `io.ReadSeeker` ห้ามใช้ `[]byte` กับทั้งไฟล์
- ทุกเมธอดของ repository/store ที่แตะไฟล์หรือข้อความต้องรับ `RoomID` เพื่อให้การค้นหาอยู่ในขอบเขตของห้อง
- พฤติกรรมภายนอกใหม่: เพิ่ม port ใน `usecase/port`, implement ใน `adapter` หรือ `infra`, เพิ่ม fake ใน `usecase/fake`, แล้วเชื่อมใน `internal/app`
- อย่าสร้าง interface ล่วงหน้าก่อนจะมีผู้ใช้งานจริง
- โฮสต์ Phase I (Windows / Android / iOS) ใช้แกน `internal/` ก้อนเดียวกับ Mac เปลือก Kotlin / Swift / ถาด Windows เป็นชั้นนอกสุดเทียบเท่า `cmd`: เปิด-ปิดเซิร์ฟเวอร์, การแจ้งเตือน, Hotspot, ที่เก็บเครื่อง, OTG ห้ามใส่กฎห้อง PIN สิทธิ์ หรือการโอนในแอปเนทีฟ ห้าม copy `domain`/`usecase` ออกจาก Go แดชบอร์ดยังเป็น `web/` บน `127.0.0.1`
- ความสามารถของ OS เครื่องใหม่ implement เป็น adapter ของ port ที่มีอยู่ (`NetworkProvider`, `FileStore`, `CaffeinateLauncher`, `ActivityTracker`, `HEICConverter`, `SystemPreviewer`) หรือ port ใหม่เมื่อมีผู้ใช้จริง เครื่องที่ไม่มีฟีเจอร์นั้นคืน `domain.ErrUnsupported`

## การตั้งชื่อ
- ชื่อ package เป็นตัวพิมพ์เล็กคำเดียว ตรงกับชื่อโฟลเดอร์
- ไฟล์ทดสอบอยู่ข้างโค้ดในรูป `*_test.go`
- ชื่อ event เป็นค่าคงที่ใน `port` (`EventFileAdded`, `EventTransfer`, ...) และเบราว์เซอร์ใช้สตริงชุดเดียวกันใน `web/app.js`

## การควบคุมเวอร์ชัน (Version control)
- เขียน commit message เป็นภาษาไทย บรรทัดหัวข้อไม่เกิน 72 ตัวอักษร ขึ้นต้นด้วยคำกริยา อนุญาตให้ใช้ prefix แบบ Conventional Commits ภาษาอังกฤษได้ (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`) ชื่อไฟล์ identifier และคำศัพท์เทคนิคให้คงเป็นภาษาอังกฤษ
