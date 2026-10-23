# เทคโนโลยีที่ใช้ (Tech Stack)

## หลัก
- ภาษา: Go (module `lanshare`, `go 1.27.0`)
- HTTP: standard library `net/http`
- WebSocket: `github.com/coder/websocket` (ซ่อนอยู่หลัง port `EventPublisher`)
- QR: `github.com/skip2/go-qrcode`
- รูปภาพ: `golang.org/x/image` (ถอดรหัสรูปเพื่อทำ thumbnail)
- Logging: `log/slog`
- การเก็บข้อมูล: ไฟล์ JSON `state.json` (`jsonstore`) อยู่หลัง repository port วางแผนจะใช้ SQLite (`modernc.org/sqlite`, pure Go, ไม่ใช้ cgo) มาแทนได้โดยไม่ต้องแก้ส่วนอื่น
- การอัปโหลดต่อเนื่อง (Phase C): สถานะเซสชันอัปโหลดเก็บเป็นไฟล์ JSON ต่อเซสชันใต้ `<data>/sessions/` (`sessionstore`) และเนื้อหาที่ยังไม่เสร็จเก็บใต้ `<data>/partial/<room>/` (`filestore`, นามสกุล `.part`)
- Adapter ใหม่ใน Phase C (`internal/adapter/`):
  - `caffeinate` กัน Mac หลับด้วยคำสั่ง `caffeinate` ระหว่างมีการโอน
  - `cmdrunner` รันคำสั่งภายนอกแบบมี context/timeout (ใช้โดย `caffeinate`)
  - `diskspace` อ่านพื้นที่ว่างของดิสก์ (statfs) สำหรับ `-min-free`
  - `sessionstore` เก็บเซสชันอัปโหลดที่ต่อได้ (`sessions/`)
  - `syspreview` วาดภาพพรีวิวของ HEIC, RAW ของกล้อง, PDF, Office, ข้อความ ฯลฯ ด้วย `sips` และ Quick Look ของ macOS (ผ่าน `cmdrunner`, ไม่เจอเครื่องมือ/ล้มเหลว = `ErrUnsupported` แล้ว UI แสดงไอคอน)
  - `peerlog` บันทึกเหตุการณ์การเชื่อมต่อของอุปกรณ์เพื่อใช้วินิจฉัย
- Use case ใหม่ใน Phase C (`internal/usecase/`): `power` (Power_Guard), `storage` (โควตา/พื้นที่ว่าง), `diagnostics` (หน้าตรวจปัญหาการเชื่อมต่อ)
- Frontend: HTML/CSS/JS ล้วนใน `web/` ฝังเข้า binary ด้วย `//go:embed` (`web/embed.go`) ไม่มี build step ไม่ใช้ npm ไม่มี `package.json` ไม่ใช้ framework ไฟล์ใน `web/`:
  - `index.html`, `app.js`, `style.css` เดิม
  - `logic.js` ฟังก์ชันล้วน (pure function) แยกจาก DOM เพื่อให้ทดสอบด้วย `node --test` ได้
  - `sha256.js`, `hash-core.js`, `hash-worker.js` คำนวณ SHA-256 ฝั่งเบราว์เซอร์ (แกนคำนวณอยู่ใน `hash-core.js` รันใน Web Worker ผ่าน `hash-worker.js`)
  - `sw.js` (service worker), `manifest.webmanifest` และ `icons/` สำหรับ PWA
  - `web/test/` เป็นไฟล์ทดสอบ/benchmark ห้ามฝังเข้า binary
- Dependency เฉพาะการทดสอบ: `pgregory.net/rapid` (property test ฝั่ง Go) ยังไม่อยู่ใน `go.mod` ถ้าเพิ่มต้องปักเวอร์ชันตายตัวและใช้ใน `_test.go` เท่านั้น ฝั่ง JS ไม่ใช้ dependency ภายนอก (ไม่มี npm, ไม่มี `fast-check`)
- แอป menu bar (Phase G): Swift AppKit ใน `desktop/LanShareMenu` เรียกเซิร์ฟเวอร์ Go แดชบอร์ดยังเป็น `web/` (ไม่ใช้ Wails)
- โฮสต์ Phase I (ล็อกแล้ว): แกน Go + `web/` เดิมทุกเครื่อง — Windows คอมไพล์ `GOOS=windows` ทั้งเซิร์ฟเวอร์และถาดระบบใน `desktop/windows`; Android เป็น Kotlin ใน `mobile/android` ห่อไลบรารี Go เปิด `localhost` ใน WebView; iOS เป็น Swift ใน `mobile/ios` ห่อ xcframework เปิด `localhost` ใน WKWebView ใช้ได้ตอนแอปเปิดอยู่ ห้ามเขียนเซิร์ฟเวอร์ซ้ำใน C# / Kotlin / Swift และห้ามใช้ Flutter

## คำสั่ง
```bash
go build ./...                      # compile ทั้งโปรเจกต์
go run ./cmd/lanshare               # รันเซิร์ฟเวอร์ (ค่าเริ่มต้น :8080, ข้อมูลอยู่ที่ ~/LANShare)
go run ./cmd/lanshare -port 9000 -data ./tmp -iface en0
go test ./...                       # รันเทสต์ทั้งหมด
go test ./internal/usecase/...      # เฉพาะเทสต์ของ use case
go test -race ./...                 # ตัวตรวจ race (โค้ดส่วนที่ใช้ concurrency หนัก)
go test -tags integration ./...     # เทสต์ integration (เช่น build ไบนารีแล้วรันจริง) ใช้ build tag `integration`
node --test web/test/               # เทสต์ฟังก์ชันล้วนของ JS (logic.js ฯลฯ) ด้วย Node ตัวเดียว ไม่ต้อง npm
go vet ./...
gofmt -l .                          # แสดงรายการไฟล์ที่ยังไม่ได้ format
sh scripts/package-hosts.sh         # แพ็กไฟล์ติดตั้งโฮสต์ Mac/Android/Windows เข้า dist/<เวอร์ชัน>/
```

Config flag (`internal/infra/config`): `-port`, `-data`, `-iface`, `-room-name`, `-max-pin-failures`, `-lockout`, `-min-free`, `-upload-ttl`, `-stall-timeout`, `-keep-awake`, `-system-preview`

flag ของ Phase C (ตรวจค่าตอน parse ถ้าค่าผิดช่วงจะล้มก่อนเปิดพอร์ตและก่อนสร้างโฟลเดอร์ข้อมูล):
- `-min-free` ขนาดพื้นที่ดิสก์ที่เก็บสำรองไว้ เขียนเป็น `<ตัวเลข><B|KiB|MiB|GiB>` เช่น `1GiB` ช่วง `0B` ถึง `9223372036854775807B` ค่าเริ่มต้น `1GiB` (`0B` = ไม่สำรอง)
- `-upload-ttl` เวลาที่เก็บอัปโหลดค้างไว้รอต่อ เป็น Go duration ขั้นต่ำ `1m` ค่าเริ่มต้น `24h`
- `-stall-timeout` ตัดคำขออัปโหลดที่ไม่ได้รับข้อมูลนานเกินกำหนด เป็น Go duration ขั้นต่ำ `5s` ค่าเริ่มต้น `30s`
- `-system-preview` เปิด/ปิดการพรีวิวด้วยเครื่องมือของ macOS รับเฉพาะ `true` หรือ `false` ค่าเริ่มต้น `true` เครื่องมือเหล่านี้อ่านไฟล์จากอุปกรณ์อื่น ถ้าไม่ไว้ใจผู้ใช้ให้ปิดด้วย `-system-preview=false`
- `-keep-awake` กัน Mac หลับระหว่างมีการโอน รับเฉพาะ `true` หรือ `false` ค่าเริ่มต้น `true` flag แบบ bool ต้องเขียนเป็น `-keep-awake=false` (ห้ามเขียน `-keep-awake false`)

```bash
go run ./cmd/lanshare -min-free 5GiB -upload-ttl 48h -stall-timeout 60s -keep-awake=false
```

ถ้าเพิ่มไฟล์ใหม่ใน `web/` (รวมถึงโฟลเดอร์ย่อยอย่าง `icons/`) ต้องเพิ่มลงในบรรทัด `//go:embed` ของ `web/embed.go` ด้วย ไม่เช่นนั้นไบนารีจะไม่มีไฟล์นั้นเมื่อรันนอกโฟลเดอร์โปรเจกต์ (ห้ามฝัง `web/test/`)

## ธรรมเนียมการเขียนโค้ด
- เขียนตามสไตล์ Go มาตรฐาน และรัน `gofmt` ห่อ error ด้วย `fmt.Errorf("...: %w", err)`
- ส่ง `context.Context` เป็นอาร์กิวเมนต์แรกของเมธอดใน port และ use case
- Domain error อยู่ใน `internal/domain/errors.go` (เช่น `ErrNotFound`, `ErrUnsupported`) ตรวจด้วย `errors.Is` และแปลงเป็น HTTP status ใน `httpapi` เท่านั้น
- Constructor ใช้รูป `NewService(...)` โดยรับ dependency แบบระบุชัดเจน ห้ามมี global และห้ามเชื่อมต่อผ่าน `init()`
- ฉีด port `Clock` และ `IDGenerator` เข้ามา อย่าเรียก `time.Now()` หรือสร้าง ID เองใน use case
- Doc comment ของ package และ type ที่ export ให้อธิบายเจตนาและ invariant (ไม่ใช่เล่าซ้ำสิ่งที่โค้ดทำ)
- Frontend: ข้อความที่ผู้ใช้ควบคุมได้ (ชื่อไฟล์ ชื่ออุปกรณ์ ข้อความ) ให้ใส่ด้วย `textContent`/text node ห้ามใช้ `innerHTML` สร้างลิงก์เฉพาะ URL แบบ http(s) คง `'use strict'` และ helper `$`/`$$` เดิมไว้
- ห้ามตั้ง `ReadTimeout`/`WriteTimeout` แบบรวมบน `http.Server` เพราะจะทำให้การโอนไฟล์ขนาดใหญ่ล้มเหลว

## การทดสอบ
- Use case: unit test โดยใช้ fake จาก `internal/usecase/fake` (port แบบ in-memory, `Clock` ที่ควบคุมเอง) ห้ามมี HTTP หรือ disk
- Adapter: ใช้ `httptest` และเทสต์แนว integration (`package httpapi_test`, filestore/jsonstore ตัวจริงใน `t.TempDir()`)
- การแยกขาดระหว่างห้อง (cross-room isolation) และ PIN lockout ต้องมีเทสต์ครอบคลุมต่อไป
- เพิ่มเทสต์เมื่อถูกขอเท่านั้น แต่ห้ามทำให้เทสต์เดิมพัง รัน `go test ./...` หลังแก้โค้ดทุกครั้ง

## ข้อควรระวังด้านความปลอดภัย
- ห้าม log หรือแสดง PIN, token และ Resume_Key ยกเว้นในแบนเนอร์ตอนเริ่มระบบ (เฉพาะ PIN/token; Resume_Key ห้ามออกใน log ทุกกรณี)
- ห้ามทำให้ PIN lockout, การตรวจ token หรือการจำกัดขอบเขตตามห้องอ่อนลง Auth middleware อยู่ที่ `internal/adapter/httpapi/middleware.go`
- โฟลเดอร์ข้อมูลสร้างด้วยสิทธิ์ `0o700` รวมถึง `sessions/` และ `partial/`
