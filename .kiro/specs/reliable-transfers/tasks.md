# Implementation Plan: reliable-transfers (Phase C "ความเสถียร")

## Overview

แผนนี้แปลง `design.md` เป็นงานเขียนโค้ดแบบค่อยเป็นค่อยไปบน Go (module `lanshare`, Clean Architecture) และ frontend แบบ vanilla ใน `web/` ไม่มี npm ไม่มี build step ลำดับงานเรียงตามความเสี่ยงก่อน

1. ความเสี่ยงสูงสุดก่อน: SHA-256 แบบ JS (ต้องวัดบนมือถือจริง) และผลลัพธ์ `socketfilterfw` ตอน Firewall เปิด (ยังไม่เคยเห็นข้อความจริง)
2. รากฐาน: config, domain, port, fake, adapter, `storage.Guard`
3. แกน resumable upload ใน `transfer.Service` (create, append, complete, cancel, janitor)
4. HTTP API และการเชื่อมใน `cmd/lanshare/main.go`
5. `power.Guard` และ `diagnostics.Service`
6. Frontend: ตรรกะล้วน, uploader, หน้า Owner, PWA, การแจ้งเตือน
7. เอกสารและ steering

กฎที่ทุกงานต้องรักษา

- ชั้น `domain` ไม่ import อะไรจากโปรเจกต์ `usecase` import ได้เฉพาะ `domain` และ `usecase/port` พฤติกรรมภายนอกใหม่ = port ใน `usecase/port` + adapter/infra + fake ใน `usecase/fake` + wiring ใน `main.go` ห้ามสร้าง interface ก่อนมีผู้ใช้จริง
- ห่อ error ด้วย `fmt.Errorf("...: %w", err)` ส่ง `context.Context` เป็นอาร์กิวเมนต์แรก ฉีด `Clock` และ `IDGenerator` ห้ามเรียก `time.Now()` ใน use case
- ห้ามตั้ง `ReadTimeout`/`WriteTimeout` รวมบน `http.Server` ห้าม log PIN, token, Resume_Key (รวม header และ query string) โฟลเดอร์ข้อมูลใหม่ (`sessions/`, `partial/`) สร้างด้วยสิทธิ์ `0o700` ไฟล์ session `0o600`
- ทุกเมธอดที่แตะ session/ไฟล์รับ `RoomID` การแยกห้องและ PIN lockout ต้องไม่อ่อนลง และเทสต์เดิมต้องผ่านต่อ
- Frontend: ใส่ข้อความที่ผู้ใช้ควบคุมด้วย `textContent`/text node เท่านั้น ห้าม `innerHTML` คง `'use strict'` และ helper `$`/`$$` ข้อความใหม่ที่ผู้ใช้เห็นเป็นภาษาไทย
- ทุกงานต้องจบด้วย `go build ./...` ผ่านและเทสต์เดิมไม่พัง
- sub-task ที่มี `*` เป็นเทสต์และเป็น optional ทั้งหมด (steering ให้เพิ่มเทสต์เมื่อถูกขอ แต่ spec นี้นิยาม Correctness Properties ไว้แล้ว) checkpoint ยังต้องรันเทสต์เดิมทุกครั้ง

## Tasks

- [ ] 1. จัดการความเสี่ยงสูงก่อน (SHA-256 ฝั่ง browser และ fixture ของ firewall)
  - [x] 1.1 สร้างไลบรารี SHA-256 ฝั่ง browser
    - สร้าง `web/sha256.js`: คลาส `Sha256` (`update(Uint8Array)`, `digestHex()`) ใช้ `Uint32Array` ไม่พึ่ง `crypto.subtle` ไม่มี dependency ภายนอก ผลเป็น hex ตัวพิมพ์เล็ก 64 ตัว
    - สร้าง `web/hash-core.js`: `hashBlob(blob, {start, end, sliceSize = 4 MiB, onProgress, isCancelled})` อ่านด้วย `blob.slice(a, b).arrayBuffer()` ทีละ slice, `compareChecksum(a, b)` ไม่สนตัวพิมพ์ใหญ่-เล็ก (ไฟล์ 0 ไบต์ต้องได้ `e3b0c442…b855`)
    - สร้าง `web/hash-worker.js`: classic worker, `importScripts('/sha256.js', '/hash-core.js')`, รับ `{id, file, start, end}` ส่ง `{id, done, total}` อย่างน้อยทุก 250 ms และ `{id, hex}` หรือ `{id, error}`
    - ทุกไฟล์ใช้รูปแบบ `if (typeof module !== 'undefined') module.exports = {...}` เพื่อให้ `node --test` โหลดได้ และคง `'use strict'`
    - _Requirements: 10.1, 10.2, 10.3, 10.13_

  - [x] 1.2 เพิ่มสคริปต์ hash ลงใน `//go:embed`
    - แก้บรรทัด `//go:embed` ใน `web/embed.go` เป็น `index.html app.js style.css sha256.js hash-core.js hash-worker.js`
    - ห้ามใส่โฟลเดอร์ `web/test/` ลงใน embed (ต้องไม่ถูกส่งให้ผู้ใช้)
    - ตรวจ `go build ./...`
    - _Requirements: 16.5_

  - [x] 1.3 สร้างเครื่องมือวัดความเร็ว SHA-256 บนมือถือ
    - สร้าง `web/test/bench-sha256.html` (สคริปต์ภายนอกเท่านั้น ไม่มี inline script) วัด MB/s ของ `hashBlob` ใน Web Worker กับไฟล์สร้างในหน่วยความจำขนาด 100 MB และ 1 GB (สร้างทีละ slice ไม่ถือทั้งไฟล์) แสดงผลบนหน้าและมีช่องคัดลอกผล
    - สร้าง `web/test/bench/main.go` (`//go:build ignore`, รันด้วย `go run web/test/bench/main.go`) เสิร์ฟโฟลเดอร์ `web/` ผ่าน HTTP เฉพาะระหว่างวัด ไม่มี auth จึงต้องพิมพ์คำเตือนว่าเสิร์ฟไฟล์ static ให้ทุกคนใน LAN และใช้ชั่วคราวเท่านั้น
    - เพิ่มหัวข้อ "ผลวัด SHA-256 บนมือถือ" (ตารางว่าง: อุปกรณ์, เบราว์เซอร์, MB/s ที่ 100 MB และ 1 GB) ลง `docs/project-scope.md` ให้ผู้ใช้กรอกหลังวัดบน iPhone และ Android จริง
    - ถ้าผลวัดช้ากว่า Wi-Fi มาก ให้ปรับค่าเริ่มต้นของการตรวจ (ดู Notes) ก่อนเริ่มงาน 19.1
    - _Requirements: 10.1, 10.3_

  - [x] 1.4 สร้างสคริปต์เก็บ fixture ผลลัพธ์คำสั่ง macOS สำหรับ diagnostics
    - สร้าง `internal/usecase/diagnostics/testdata/capture.sh` ที่ **อ่านอย่างเดียว** (ห้ามเปลี่ยนค่า Firewall หรือระบบ): `socketfilterfw --getglobalstate`, `--getblockall`, `--getappblocked <path ของไบนารี>`, `networksetup -listallhardwareports`, `system_profiler SPAirPortDataType -json`
    - สคริปต์ต้องตัดชื่อ Wi-Fi (SSID), ชื่อเครือข่ายอื่น, MAC และข้อมูลระบุตัวตนออกก่อนบันทึกไฟล์ใน `testdata/` เก็บเฉพาะข้อความที่ parser ต้องใช้ (เช่น สตริง channel)
    - รันบนเครื่องนี้เพื่อเก็บ fixture กรณี Firewall ปิด (ตรวจแล้วในการออกแบบ) และเขียน `testdata/README.md` ระบุขั้นตอนให้ผู้ใช้เปิด Firewall เองแล้วรันซ้ำเพื่อเก็บกรณี "Firewall เปิด + โปรแกรมถูกอนุญาต/ถูกบล็อก/ไม่อยู่ในรายการ" (ผู้ใช้เป็นผู้เปลี่ยนค่า ไม่ใช่เอเจนต์)
    - _Requirements: 14.4, 14.5, 14.6, 14.10, 14.11, 14.12_

  - [ ]* 1.5 เขียนเทสต์ SHA-256 ฝั่ง JS ด้วย `node --test`
    - **Property 32: SHA-256 ฝั่ง browser เท่ากับของอ้างอิง**
    - **Validates: Requirements 10.1, 10.2, 10.5, 10.13**
    - สร้าง `web/test/sha256.test.js` ใช้เฉพาะ `node:test`, `node:assert`, `node:crypto` ไม่มี package.json ไม่มี fast-check
    - เวกเตอร์ที่รู้ค่า (ว่าง, `abc`, ข้อความ 448 บิต, หนึ่งล้านตัว `a`) และลูปสุ่มเขียนเอง ≥ 100 รอบ (seed คงที่พิมพ์ออกเมื่อล้มเหลว): ความยาวสุ่ม 0 ถึงหลายพันไบต์ รวมความยาวรอบขอบบล็อก 55, 56, 63, 64, 65, แบ่ง `update` แบบสุ่ม, ขนาด slice สุ่มของ `hashBlob` เทียบ `crypto.createHash('sha256')`, ช่วง `[0, k)` เท่ากับ SHA-256 ของ prefix, `compareChecksum` ไม่สนตัวพิมพ์

  - [ ]* 1.6 เขียนเทสต์โปรโตคอลของ `hash-worker.js` ด้วย `vm`
    - จำลอง `self`, `importScripts`, `postMessage` ใน `web/test/hash-worker.test.js` ตรวจข้อความ progress, hex และ error เมื่ออ่านไฟล์ไม่ได้
    - _Requirements: 10.3, 10.8_

- [x] 2. ค่าตั้งของ Phase C (config)
  - [x] 2.1 สร้าง `ParseSize` และ `FormatSize`
    - สร้าง `internal/infra/config/size.go`: `ParseSize` รับ `^[0-9]{1,19}(B|KiB|MiB|GiB)$` (หน่วยตัวพิมพ์ตามนี้เป๊ะ ไม่มีช่องว่าง) แปลงผ่าน `uint64` พร้อมตรวจล้น ผลต้อง ≤ 2^63−1; `FormatSize` ใช้หน่วยใหญ่สุดที่หารลงตัว (0 เป็น `0B`)
    - ห่อ error ด้วย `%w` ข้อความระบุค่าที่ถูกปฏิเสธและช่วงที่ยอมรับ
    - _Requirements: 18.2, 18.3, 18.8_

  - [x] 2.2 เพิ่ม flag ใหม่และ usage ใน `config.Load`
    - แก้ `internal/infra/config/config.go`: เพิ่ม `MinFree int64` (`-min-free`, ค่าเริ่มต้น `1GiB`), `UploadTTL` (`-upload-ttl`, `24h`, ≥ `1m`), `StallTimeout` (`-stall-timeout`, `30s`, ≥ `5s`), `KeepAwake` (`-keep-awake`, `true`)
    - ใช้ `flag.Value` ที่ตรวจช่วงใน `Set` ข้อความ error มีชื่อ flag, ค่าที่ปฏิเสธ, ช่วงที่ยอมรับ; ตั้ง output ของ `FlagSet` เป็น `io.Discard` ตอน parse เพื่อให้ได้ error เดียว
    - `-h`/`-help` เขียน usage (ชื่อ ชนิด ค่าเริ่มต้น คำอธิบายหนึ่งบรรทัด ของ flag เดิมและใหม่ทั้งหมด) ลง stderr แล้ว exit 0; flag ที่ไม่รู้จักหรือไม่มีค่า → error + usage แล้ว exit 1 ทั้งหมดต้องเกิดก่อน bind พอร์ตและก่อนสร้าง Data_Dir (ลำดับใน `run()` เดิมเรียก `config.Load` ก่อนอยู่แล้ว ให้คงไว้)
    - flag เดิม (`-port`, `-data`, `-iface`, `-room-name`, `-max-pin-failures`, `-lockout`) ต้องทำงานเหมือนเดิม
    - _Requirements: 18.1, 18.2, 18.3, 18.4, 18.5, 18.6_

  - [ ]* 2.3 เขียน property test ของ size round trip
    - **Property 25: Size round trip**
    - **Validates: Requirements 18.8, 18.9**
    - `internal/infra/config/size_test.go` ใช้ `rapid` (หลังงาน 3.5) ค่า n ตั้งแต่ 0 ถึง 9223372036854775807 และตรวจว่าหน่วยที่เลือกเป็นหน่วยใหญ่สุดที่หารลงตัว

  - [ ]* 2.4 เขียน property test และตัวอย่างของการตรวจค่า config
    - **Property 26: การตรวจค่า config**
    - **Validates: Requirements 18.2, 18.3, 18.4**
    - `internal/infra/config/config_test.go`: สตริงที่ผิดไวยากรณ์ (ไม่มีหน่วย, หน่วยผิดตัวพิมพ์, ทศนิยม, ลบ, ช่องว่าง, > 19 หลัก, เกิน 2^63−1), duration ต่ำกว่าขั้นต่ำ ต้อง error ที่มีชื่อ flag/ค่า/ช่วง; ค่าที่ถูกต้องต้องผ่าน
    - ตัวอย่าง: `-h` (exit 0, usage ครบทุก flag), flag ไม่รู้จัก (exit 1), `-keep-awake=false`, และ Data_Dir ไม่ถูกสร้างเมื่อ config ผิด
    - _Requirements: 18.5, 18.6_

  - [x] 2.5 log ค่า config ที่ใช้จริงตอนเริ่มระบบ
    - แก้ `cmd/lanshare/main.go`: หลัง `config.Load` สำเร็จและก่อนเริ่มรับ connection เขียน `slog.Info` หนึ่งรายการมี `min-free` (รูป `FormatSize`), `upload-ttl`, `stall-timeout` (Go duration string), `keep-awake`
    - ห้าม log PIN หรือ token
    - _Requirements: 18.7_

- [x] 3. Domain, ports, fakes และ dependency สำหรับเทสต์
  - [x] 3.1 เพิ่มชนิดข้อมูลและ error ใหม่ใน `internal/domain`
    - สร้าง `upload.go`: `SessionID`, `MaxDeclaredSize = 1<<53 - 1`, `UploadSession` (ฟิลด์ตามหัวข้อ Data Models: `ID, RoomID, CreatorID, CreatorName, Name, Dir, Declared, Conflict, Offset, HashState, ResumeKeyHash, CreatedAt, LastWriteAt`), `Valid()` (0 ≤ Offset ≤ Declared, ชื่อ/Dir sanitize ซ้ำแล้วไม่เปลี่ยน), `ExpiresAt(ttl)`, `HashResumeKey`, `CheckResumeKey` (constant time)
    - สร้าง `integrity.go`: `Integrity` (`verified|unverified|unknown`), `NormalizeChecksum` (hex 64 ตัว → lowercase, อย่างอื่น `ErrInvalidInput`), `EmptySHA256`
    - สร้าง `check.go`: `CheckResult`, `CheckStatus`, `PowerStatus` (`active|idle|disabled|unavailable`)
    - แก้ `errors.go`: typed errors ตามตารางในการออกแบบ (`OffsetMismatchError`, `ErrUploadBusy`, `IncompleteError`, `SizeExceededError`, `ErrStallTimeout`, `ErrTooManyUploads`, `InsufficientStorageError`, `ChecksumMismatchError`, `PersistFailedError`, `ErrChecksumBusy`, `ErrChecksumFailed`, `ErrDiskFull`, `ErrInterrupted`) ที่เหมาะให้มี `Unwrap`
    - แก้ `file.go`: เพิ่ม `SHA256 string`, `Integrity Integrity` และ `IntegrityOrUnknown()`; แก้ `network.go`: เพิ่ม `NetInterface.PrefixLen int`
    - ห้าม import HTTP/JSON/SQL/disk; ฟิลด์ที่เพิ่มต้องไม่ทำให้โค้ดและเทสต์เดิมคอมไพล์ไม่ผ่าน (ใช้ named fields)
    - _Requirements: 1.1, 1.9, 2.9, 5.7, 7.2, 8.4, 9.1, 9.3, 14.3_

  - [x] 3.2 ประกาศ port ใหม่ใน `internal/usecase/port/port.go`
    - เพิ่ม `UploadSessionRepository`, `PartialRef`, `PartialWriter`, `PartialStore`, `DiskSpace`, `SecretGenerator`, `ActivityTracker`, `CaffeinateProcess`, `CaffeinateLauncher`, `CommandRunner`, `PeerLog` (+ `PeerSeen`), `LocalAddrProvider` ตาม signature ในการออกแบบ (ทุกเมธอดรับ `RoomID` ที่เกี่ยวข้อง และ `context.Context` เป็นตัวแรก)
    - ยังไม่แก้ `FileRepository` (ทำใน 4.1 พร้อม implementation เพื่อให้ build ผ่านทุกก้าว)
    - ทุก port มีผู้ใช้จริงในงานถัดไป: sessions/partials/disk/secrets/activity → `transfer` และ `storage`, launcher → `power`, runner/peers/locals → `diagnostics`
    - _Requirements: 4.4, 7.1, 13.1, 14.16, 5.7_

  - [x] 3.3 เพิ่ม fake ใน `internal/usecase/fake/fake.go`
    - เพิ่ม `Sessions` (ล้มเหลวที่การเรียกครั้งที่ k, จำลอง record เสีย), `Partials` (ตัด/ต่อไบต์จริง, ล้มด้วย `ErrDiskFull` ที่ไบต์ที่ n, นับไบต์ที่ถูกอ่านซ้ำ, สร้างภาพดิสก์สำหรับทดสอบ Recover), `Disk` (ตั้งพื้นที่ว่างได้, จำลองอ่านไม่ได้), `Secrets` (ค่ากำหนดได้ ≥ 32 ตัวอักษร), `Activity`, `Launcher` (เริ่มสำเร็จ/ล้มเหลว/จบเอง), `Runner` (ตอบตามสคริปต์/หน่วงเวลา/error ต่อคำสั่ง), `PeerLog`, `LocalAddrs`
    - ไม่ใส่ fake ของ `AppendBody` ที่นี่ (อินเทอร์เฟซอยู่ในแพ็กเกจ `transfer` ถ้า import จะเกิด cycle) ให้ทำใน test harness ของ `transfer`
    - _Requirements: 2.3, 4.6, 4.7, 7.5, 13.9, 14.20_

  - [ ]* 3.4 เขียนเทสต์ unit ของ domain ใหม่
    - สร้าง `internal/domain/upload_test.go`: `NormalizeChecksum` (ความยาว, อักขระ, ตัวพิมพ์), `UploadSession.Valid`, `CheckResumeKey` (key ว่าง/ผิด/ถูก), `ExpiresAt`, typed errors กับ `errors.Is/As`
    - _Requirements: 9.4, 5.2, 4.2_

  - [ ]* 3.5 เพิ่ม dependency `pgregory.net/rapid` สำหรับ property test ฝั่ง Go
    - รัน `go get pgregory.net/rapid@v1.3.0` (เวอร์ชันล่าสุดที่ `go list -m -versions` แสดงเมื่อเขียนแผน; ปักเวอร์ชันตายตัว) แล้วตรวจ `go.mod`/`go.sum`
    - ถ้าดึงไม่ได้ ให้ใช้ `testing/quick` สำหรับ property ที่ไม่ต้องใช้ state machine และตารางเคส seed คงที่สำหรับที่เหลือ แล้วบันทึกข้อจำกัดนี้ลง Notes
    - ใช้ `-rapid.checks=100` เป็นอย่างน้อย
    - _Requirements: (Testing Strategy ของ design)_

- [x] 4. เก็บ checksum ใน metadata ของไฟล์
  - [x] 4.1 เพิ่ม `FileRepository.SetChecksum` พร้อม implementation
    - แก้ `port.go`: เพิ่ม `SetChecksum(ctx, room, id, sha256hex) error` ใน `FileRepository` (คืน `ErrNotFound` ถ้าไฟล์ถูกลบหรือถูกแทนที่แล้ว)
    - แก้ `fake.Files` และ `internal/adapter/jsonstore/store.go` ในงานเดียวกัน: `fileDTO` เพิ่ม `SHA256`/`Integrity` (`omitempty`), `SetChecksum` ทำใต้ `mu` เดียวกับ `Save` และสแกนด้วย `RoomID`; `state.json` เก่าที่ไม่มีฟิลด์ต้องโหลดได้และได้ `Integrity` ว่าง (= `unknown`)
    - _Requirements: 8.4, 8.6, 8.10, 8.13_

  - [x] 4.2 เพิ่ม `sha256` และ `integrity` ใน DTO ของไฟล์
    - แก้ `fileDTO` ใน `internal/adapter/httpapi/handlers.go`: `sha256` (ว่างถ้า `unknown`) และ `integrity` (`IntegrityOrUnknown`) ฟิลด์เดิมทั้งหมดต้องคงชื่อ ชนิด ค่าเหมือนเดิม
    - _Requirements: 6.6, 8.4_

  - [ ]* 4.3 เขียน property test ของ metadata round trip
    - **Property 21: Metadata round trip และความเข้ากันได้กับ state.json เก่า**
    - **Validates: Requirements 6.6, 8.13**
    - `internal/adapter/jsonstore/store_test.go` ใช้ jsonstore จริงใน `t.TempDir()`: save/load `File` สุ่ม (มี/ไม่มี `SHA256`) และโหลด `state.json` สไตล์ก่อน Phase C ต้องไม่ทำให้ไฟล์หาย

- [x] 5. Adapter ใหม่สำหรับ session, partial file, พื้นที่ดิสก์ และ secret
  - [x] 5.1 สร้าง `internal/adapter/sessionstore`
    - implement `port.UploadSessionRepository`: ไฟล์ละ session ที่ `<data>/sessions/<id>.json` (0600, โฟลเดอร์ 0700); `Save` = เขียน `.tmp` → `Sync` → `rename` → `Sync` โฟลเดอร์
    - ตรวจชื่อ/ID ด้วย `^[0-9a-f]{32}$` ก่อนสร้างพาธทุกครั้ง; `LoadAll` คืน `ok` และ `corrupt` (parse ไม่ได้, เวอร์ชันไม่รู้จัก, `Valid()` ไม่ผ่าน); record มี `version: 1`
    - _Requirements: 4.4, 4.8, 4.11, 5.7, 8.2_

  - [ ]* 5.2 เขียน property test ของ session record
    - **Property 7: Upload_Session record round trip**
    - **Validates: Requirements 4.8, 8.2**
    - `sessionstore_test.go`: save→load ได้ทุกฟิลด์เท่าเดิมรวม `ResumeKeyHash` และ `HashState`; unmarshal `HashState` แล้วเขียนไบต์ที่เหลือได้ SHA-256 ของไบต์ทั้งหมด; ตัวอย่าง: ไฟล์ `.tmp` ค้างไม่ถูกนับเป็น record, record เสียไปอยู่ใน `corrupt`, ID ที่มี `../` ถูกปฏิเสธ

  - [x] 5.3 เพิ่ม `PartialStore` และการแปลง disk-full ใน `filestore`
    - แก้ `internal/adapter/filestore/filestore.go`: implement `Create`, `OpenAppend` (ตัดไฟล์ให้เหลือ offset แล้วเขียนต่อ), `Size`, `Truncate`, `Delete`, `Promote` (rename อะตอมมิกจาก `partial/<room>/<id>.part` ไป `rooms/<room>/<fileID>`), `Demote`, `List`, `SweepLegacyTemp`
    - partial อยู่ไดรฟ์เดียวกับ `rooms/`; ทุกเมธอดตรวจ `safeName` ของ room และ id; แปลง `ENOSPC`/`EDQUOT` เป็น `domain.ErrDiskFull` ด้วย `errors.Is` ทั้งใน `Put` และ `OpenAppend`
    - เมธอดเดิมของ `FileStore`/`ThumbStore` ต้องทำงานเหมือนเดิมและ `filestore_test.go` เดิมต้องผ่าน
    - _Requirements: 2.1, 2.3, 3.1, 4.5, 4.6, 7.5_

  - [ ]* 5.4 เขียนเทสต์ของ `PartialStore`
    - เพิ่มใน `filestore_test.go` หรือไฟล์ใหม่ `partial_test.go` (ดิสก์จริงใน `t.TempDir()`): `Promote`/`Demote` ย้อนกลับได้, `OpenAppend` ตัดส่วนเกิน, `SweepLegacyTemp` ลบเฉพาะไฟล์ที่เก่ากว่ากำหนด, ห้องคนละห้องไม่ปนกัน, พาธอันตรายถูกปฏิเสธ, ฟังก์ชันแปลง error ของดิสก์เต็มให้ `ErrDiskFull`
    - _Requirements: 3.5, 4.5, 4.6, 7.5_

  - [x] 5.5 สร้าง `internal/adapter/diskspace`
    - implement `port.DiskSpace` ด้วย `syscall.Statfs`: `uint64(st.Bavail) * uint64(st.Bsize)` (แปลง `Bsize` เป็น `uint64` ก่อนคูณ); build tag `darwin || linux` และไฟล์ stub ระบบอื่นคืน error
    - _Requirements: 7.1, 7.6_

  - [x] 5.6 เพิ่ม `SecretGenerator` จริงใน `internal/infra/system`
    - 128 บิตจาก `crypto/rand` เป็น hex 32 ตัว (ใช้สร้าง Session ID และ Resume_Key แยกกัน) คืน error ที่ห่อด้วย `%w` ถ้าอ่านแหล่งสุ่มไม่ได้
    - _Requirements: 1.9, 5.7_

- [x] 6. `storage.Guard` (ตรวจและจองพื้นที่ดิสก์)
  - [x] 6.1 สร้าง `internal/usecase/storage/guard.go`
    - `Guard` รับ `port.DiskSpace`, `minFree uint64`, logger; `Reserve(ctx, key, declared)` ล็อก `mu` ตลอดการตัดสินใจ: `avail = max(0, free − Σ ledger อื่น)`, รับเมื่อ `avail ≥ declared + minFree` (เท่ากันต้องผ่าน, ใช้การบวกแบบ saturating), ใส่ `ledger[key]` ในล็อกเดียวกัน; ไม่รับ → `*InsufficientStorageError{Required, Available}`
    - `Reservation.Update(remaining)`, `Release()`, `CheckFree`, `Snapshot` (`free`, `minFree`, `freeKnown`); อ่านพื้นที่ว่างไม่ได้ → รับคำขอ, `slog.Warn` หนึ่งครั้งต่อความล้มเหลว, `freeKnown=false`
    - _Requirements: 7.1, 7.2, 7.3, 7.6, 7.9_

  - [ ]* 6.2 เขียน property test ของ Storage_Guard
    - **Property 17: Storage_Guard ไม่จองเกินพื้นที่**
    - **Validates: Requirements 7.1, 7.2, 7.8, 7.9**
    - `guard_test.go` ใช้ `fake.Disk`: ลำดับ Reserve/Update/Release สุ่มและส่งพร้อมกัน (`-race`), ตรวจกรณีเท่ากันผ่านและ `requiredBytes`/`availableBytes` ถูกต้อง

- [x] 7. Checkpoint A - รากฐานพร้อม
  - รัน `gofmt -l .` (ต้องไม่มีไฟล์ค้าง), `go vet ./...`, `go test -race ./...` และ `node --test web/test/`
  - Ensure all tests pass, ask the user if questions arise.

- [x] 8. `transfer.Service`: สร้าง ถาม และต่อไบต์ใน Upload_Session
  - [x] 8.1 เตรียมโครงสร้างของ `transfer.Service` สำหรับ session
    - แก้ `internal/usecase/transfer/transfer.go`: `Progress.State` รองรับ `"interrupted"`; `tracker` เพิ่ม `mark(StateInterrupted)` (ส่ง event ทันที `Speed=0` ไม่ปิด tracker), `add(n)` ในสถานะ `interrupted` กลับเป็น `running` และ `bytes` ไม่ลดลง, `finish` ส่ง event สถานะปลายหนึ่งครั้ง; เพิ่มฟิลด์ที่ต้องใช้ทั้งหมดของ session (registry + `regMu`, `roomLock`, completion records, semaphore สำหรับ lazy checksum, ฯลฯ) โดย `NewService` เดิมไม่เปลี่ยน signature
    - สร้าง `session.go`: `UploadDeps`, `UploadConfig` (`TTL`, `StallTimeout`, `CompletionKeep` 10 นาที), `ConfigureUploads`, `AppendBody` (`io.Reader` + `Interrupt()`), `sessionRT`, `appendRun`, ฟังก์ชัน lookup ที่คืน `ErrNotFound` เหมือนกันทุกกรณี (ไม่พบ, ห้องอื่น, key ว่าง/ผิด, dead, หมดอายุ) และเทียบ hash ศูนย์เมื่อไม่พบ session
    - ถ้ายังไม่เรียก `ConfigureUploads` เทสต์และ `POST /api/files` เดิมต้องทำงานเหมือนเดิม (`Activity` nil = no-op)
    - _Requirements: 2.6, 2.11, 5.1, 5.2, 12.1, 12.2, 12.3_

  - [x] 8.2 implement `CreateSession` และ `QuerySession`
    - เพิ่มใน `session.go`: ลำดับตรวจตามการออกแบบ (CanUpload → ตรวจ input → sanitize → สิทธิ์ overwrite → นับ 16/64 → `Guard.Reserve` → สร้าง `.part` ว่าง + `Sessions.Save` + registry; ล้มขั้นใดต้องคืนทุกอย่างที่สร้างไปแล้ว)
    - ชื่อ 1-200 rune หลัง trim (เกินหรือว่าง → `ErrInvalidInput`), `Declared` 0..`MaxDeclaredSize`, conflict `""|rename|overwrite`, ชื่อที่ sanitize แล้วว่างใช้ `file`, `Declared=0` complete ได้โดยไม่ต้อง append
    - ผลสร้างคืน `id, resumeKey, offset 0, size, expiresAt` (คืน key ครั้งเดียวเท่านั้น, สุ่มแยกจาก ID); `QuerySession` คืน `offset/size/expiresAt/sha256` จาก record ที่ persist แล้ว (offset 0 → `EmptySHA256`)
    - ตรวจ `CanUpload` ก่อน lookup ทุกครั้ง
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.7, 1.8, 1.9, 2.4, 5.6, 5.7, 6.4, 7.1, 7.2_

  - [x] 8.3 implement `Append`
    - สร้าง `append.go` ตามอัลกอริทึมในการออกแบบ: สิทธิ์ → lookup → ตรวจ offset (400) → แทนที่ request เก่าด้วย `interrupt()` รอ ≤ 5 วินาที (`ErrUploadBusy`) → ตรวจ offset ตรง (`OffsetMismatchError`) และ Content-Length เกิน (413 ก่อนอ่าน body) → `OpenAppend` → วนอ่านทีละ ≤ 1 MiB จาก `sync.Pool` → เขียนไฟล์ก่อนแล้ว hash เฉพาะไบต์ที่เขียนได้จริง
    - watchdog `time.AfterFunc(StallTimeout)` เรียก `Interrupt()` ได้ `ErrStallTimeout` (408) เก็บไบต์ที่เขียนแล้ว; เกิน `Declared` ระหว่างอ่าน → rollback ทั้ง request (`Truncate` + `HashState` เดิม) และ 413; `ErrDiskFull` → หยุด finalize และคืน `InsufficientStorageError` (`requiredBytes = remaining + minFree`)
    - sync และ persist ทุก 64 MiB และตอนจบ request ด้วย `context.WithoutCancel`; ลำดับบังคับ: เขียน partial → `Sync` → `Sessions.Save` → จึงอัปเดต `rt.rec`/ledger; `Save` ล้มเหลว → `Truncate` กลับ offset ที่ persist และ `*PersistFailedError`
    - เรียก `Activity.Begin`/`Hold(id, 60s)`/`Release` และส่ง event `running` (ห่าง ≥ 250 ms) / `interrupted` (≤ 1 วินาที) ผ่าน tracker ต่อ session
    - ใช้หน่วยความจำไม่เกิน 1 MiB ของเนื้อไฟล์ต่อ request และไม่อ่าน partial ซ้ำ
    - _Requirements: 2.1, 2.2, 2.3, 2.5, 2.6, 2.7, 2.9, 2.10, 2.12, 4.3, 4.4, 4.12, 7.5, 8.1, 8.2, 8.3, 12.1, 12.2, 12.3_

  - [ ]* 8.4 สร้าง test harness ของ `transfer` สำหรับ property test
    - สร้าง `internal/usecase/transfer/harness_test.go`: ฟังก์ชันประกอบ `Service` ด้วย fake ทั้งหมด, `Body` ที่ส่งไบต์ตามสคริปต์แล้วขาด/บล็อก/รับ `Interrupt()`, generator ของ `rapid` (ไบต์, จุดขัดจังหวะ, ชื่อ/Dir, ขนาด), helper สร้างอุปกรณ์หลายบทบาทและหลายห้อง, ตัวจับ event
    - _Requirements: (รองรับ property test ของ Requirements 1-9, 12)_

  - [ ]* 8.5 เขียน property test ของการตรวจ input ตอนสร้าง
    - **Property 6: การตรวจ input และ sanitize ตอนสร้าง session**
    - **Validates: Requirements 1.1, 1.3, 1.4, 1.5, 6.4**
    - `session_create_test.go`

  - [ ]* 8.6 เขียน property test ของ append ที่ถูกปฏิเสธ
    - **Property 3: คำขอ append ที่ถูกปฏิเสธไม่เปลี่ยนสถานะ**
    - **Validates: Requirements 2.2, 2.5, 2.12**
    - `append_reject_test.go`: offset ไม่ตรง/ผิดรูป/เกิน, body ที่ทำให้เกิน `Declared` (รวมส่วนต้นที่ถูกต้อง) ต้อง rollback ทั้งหมด

  - [ ]* 8.7 เขียน property test ของ offset ที่ตอบกับสิ่งที่ persist
    - **Property 15: Offset ที่ตอบ client ไม่เกินที่ persist**
    - **Validates: Requirements 4.4, 4.12**
    - `append_persist_test.go` ใช้ `fake.Sessions` ที่ล้มเหลวตามจุดสุ่ม

- [x] 9. `transfer.Service`: ปิดงาน เผยแพร่ไฟล์ และยกเลิก
  - [x] 9.1 implement `CompleteSession` และ `publish.go`
    - สร้าง `publish.go`: `roomLock(room)`, การเลือกชื่อ (`rename` ใช้ `domain.UniqueName` เทียบ Dir และชื่อแบบ case-insensitive; `overwrite` หาไฟล์เดิม), การเผยแพร่ (`Promote` → `files.Save` → ลบของเดิมสำหรับ overwrite → `file.deleted` แล้ว `file.added`)
    - สร้าง `complete.go` ตามลำดับในการออกแบบ: CanUpload → Completion_Record (ซ้ำภายใน 10 นาที → 200 ไฟล์เดิมโดยไม่ publish ซ้ำ, รวมคำขอพร้อมกัน) → lookup → `NormalizeChecksum` (ผิดรูป 400 โดยไม่แตะ session) → `Offset < Declared` → `IncompleteError` → ตั้ง `completing` และตัด append ที่ค้าง → เทียบ checksum ก่อนย้ายไฟล์ (ไม่ตรง: ลบ partial/session/ledger, tracker `failed`, `*ChecksumMismatchError`) → `roomLock` → ตรวจสิทธิ์ overwrite ซ้ำด้วยอุปกรณ์ที่ส่ง complete → `Promote` + `Save`
    - `Save` ล้มเหลว → `Demote` กลับ (ล้มเหลวอีกจึงลบ) และ 500 โดยไม่ publish, ของเดิมที่ overwrite ไม่ถูกแตะ; สำเร็จ → ลบ record/registry/ledger ก่อนตอบ, เก็บ Completion_Record 10 นาที, `UploaderID` = อุปกรณ์ที่ส่ง complete, `Integrity=verified` ก็ต่อเมื่อมี checksum ที่เท่ากับที่คำนวณ ไม่เช่นนั้น `unverified`, ส่ง `file.added` หนึ่งครั้งและ `transfer done` หนึ่งครั้ง
    - _Requirements: 3.1, 3.2, 3.3, 3.5, 3.6, 3.7, 3.8, 5.5, 6.2, 6.3, 6.9, 8.4, 9.1, 9.2, 9.3, 9.4, 9.5, 9.8, 12.4, 12.5_

  - [x] 9.2 implement `CancelSession`, `CancelByOwner`, `ListSessions`, `PartialBytes`
    - สร้าง `cancel.go`: Cancel โดยผู้ถือ key หรือ Owner ของห้อง (Owner ข้าม key ได้เฉพาะห้องตนเอง), ตั้ง `dead`, ตัด append ที่ค้างและรอ ≤ 5 วินาที, ลบ partial/record, `Guard.Release`, `Activity.Release`, ส่ง `transfer stopped` หนึ่งครั้ง, 204; ผิดห้อง/ไม่พบ/ไม่มีสิทธิ์ → `ErrNotFound` เดียวกัน
    - `ListSessions`/`CancelByOwner` ใช้ได้เฉพาะ Owner (มิฉะนั้น `ErrForbidden`) คืนเฉพาะ session ของห้องตนเอง มี `Declared`, `Offset`, เวลา activity ล่าสุด และไม่มี Resume_Key และไม่มีชื่อไฟล์; `PartialBytes()` = ผลรวม `Offset`
    - _Requirements: 4.1, 4.10, 5.1, 5.2, 5.6, 14.2, 14.19, 14.21_

  - [ ]* 9.3 เขียน property test ของการปฏิเสธที่แยกไม่ออกและการแยกห้อง
    - **Property 4: การปฏิเสธที่แยกไม่ออกและแยกห้อง**
    - **Validates: Requirements 2.11, 4.10, 5.1, 5.2, 5.8, 8.11**
    - `isolation_test.go`: สองห้อง, Owner ของห้องหนึ่งใช้ key ที่ถูกของอีกห้อง, id ไม่มี, key ว่าง/ผิด ต้องได้ผลเหมือนกันทุกประการและไม่มีสิ่งใดเปลี่ยน (`-race`)

  - [ ]* 9.4 เขียน property test ของบทบาทที่ไม่มีสิทธิ์
    - **Property 5: บทบาทที่ไม่มีสิทธิ์อัปโหลดถูกปฏิเสธก่อนค้นหา session**
    - **Validates: Requirements 1.2, 5.6**
    - `role_test.go`: ทุก operation × ทุกบทบาทที่ `CanUpload()` เท็จ × key ถูก/ผิด

  - [ ]* 9.5 เขียน property test ของ Partial_File ที่ไม่ปรากฏและลำดับการ publish
    - **Property 8: Partial_File ไม่ปรากฏและการ publish ถูกลำดับ**
    - **Validates: Requirements 3.2, 3.3, 3.4, 12.4**
    - `visibility_test.go`: `List/Open/Thumbnail/PlanArchive` ไม่เห็น partial, complete ก่อนครบได้ 409 `incomplete`, `file.added` หนึ่งครั้งหลัง `Save`

  - [ ]* 9.6 เขียน property test ของ complete ซ้ำ
    - **Property 9: Complete ซ้ำให้ผลเดิม และบันทึกผู้ complete เป็นผู้อัปโหลด**
    - **Validates: Requirements 3.6, 3.7, 5.5**
    - `complete_repeat_test.go` ใช้ `fake.Clock` (ภายใน 10 นาที = 200 ไฟล์เดิม, พ้นแล้ว 404), คำขอพร้อมกัน (`-race`)

  - [ ]* 9.7 เขียน property test ของ overwrite
    - **Property 11: สิทธิ์และความอะตอมมิกของ overwrite**
    - **Validates: Requirements 1.6, 3.8, 6.3, 6.7**
    - `overwrite_test.go`: สิทธิ์ตอนสร้างและตอน complete, ลำดับ `file.deleted` → `file.added`, `Save` ล้มเหลวแล้วของเดิมยังอยู่

  - [ ]* 9.8 เขียน property test ของ cancel
    - **Property 12: ความหมายของ cancel**
    - **Validates: Requirements 4.1, 14.21**
    - `cancel_test.go`: cancel ขณะว่าง/ขณะ append, ledger คืนพื้นที่, event `stopped` หนึ่งครั้ง, Owner ยกเลิกของห้องอื่นไม่ได้

  - [ ]* 9.9 เขียน property test ของสตรีม event ความคืบหน้า
    - **Property 23: สตรีม event ความคืบหน้าของ session**
    - **Validates: Requirements 12.1, 12.2, 12.3, 12.5**
    - `progress_test.go` ใช้ `fake.Clock`: `running` ห่าง ≥ 250 ms, `bytes` ไม่ลดลง, `interrupted` ≤ 1 วินาที `speed=0`, สถานะปลายหนึ่งครั้งแล้วไม่มี event อีก

- [x] 10. `transfer.Service`: `POST /api/files` เดิม และ checksum ของไฟล์เก่า
  - [x] 10.1 ปรับ `Upload` เดิมให้ใช้ `publish.go`, Storage_Guard และ SHA-256
    - แก้ `transfer.go` โดย **ไม่เปลี่ยน signature เดิม** เพิ่มเมธอด/ออปชันใหม่ (เช่น `UploadVerified`) สำหรับ `sha256` เพื่อไม่ต้องแก้ caller เดิม
    - ลำดับ: CanUpload → ตรวจ overwrite เบื้องต้น (403 ก่อนอ่าน body) → ถ้ารู้ Content-Length ให้ `Guard.Reserve` ก่อนอ่านไบต์แรก → ห่อ body ด้วย tee เข้า sha256 (ไม่อ่านไฟล์ซ้ำ) → ถ้าไม่รู้ขนาด ตรวจ `Disk.Free` ทุก 64 MiB และหยุดเมื่อ `free < minFree` พร้อมลบไฟล์ชั่วคราว → ตรวจ `sha256` ก่อน publish (ไม่ตรง → ลบเนื้อหา, 422) → เลือกชื่อและ publish ภายใน `roomLock` หลัง `Put` → `Integrity` `verified|unverified`
    - เพิ่ม `Activity.Begin` ให้การอัปโหลด, `Open` (ดาวน์โหลด) และ zip (end ตอน `Close`)
    - `transfer_test.go` เดิมต้องผ่านต่อ
    - _Requirements: 6.1, 6.2, 6.3, 6.5, 6.7, 6.8, 6.9, 7.3, 7.4, 8.5, 9.6, 9.7, 13.1_

  - [x] 10.2 implement `FileChecksum` (คำนวณ checksum ของไฟล์ที่เก็บไว้ก่อน Phase C)
    - สร้าง `integrity.go`: `CanDownload` (403) → `files.Get(actor.RoomID, id)` (ห้องอื่น = `ErrNotFound`) → มี `SHA256` คืนเลย → ไม่มีก็เข้าคิว semaphore ขนาด 1 รอ ≤ 60 วินาที (`ErrChecksumBusy`) → `Open` แล้วคัดลอกผ่าน buffer 1 MiB เข้า sha256 → `files.SetChecksum`
    - ไฟล์หายหรือถูกแทนที่ระหว่างคำนวณ → `ErrNotFound` โดยไม่เก็บอะไร; ข้อผิดพลาดอ่าน → `ErrChecksumFailed`; `Integrity` ยังเป็น `unknown`
    - _Requirements: 8.6, 8.7, 8.8, 8.9, 8.10, 8.11_

  - [ ]* 10.3 เขียน property test ของ lazy checksum
    - **Property 22: Lazy checksum ถูกต้องและทำทีละงาน**
    - **Validates: Requirements 8.6, 8.8**
    - `integrity_test.go` (`-race`): ค่าเท่ากับ SHA-256 ของเนื้อหา, ไม่เกิน 1 งานพร้อมกัน, ตัวอย่าง 8.7, 8.9, 8.10, 8.11

  - [ ]* 10.4 เขียน property test ของชื่อไม่ซ้ำ
    - **Property 10: ชื่อไฟล์ที่ publish ไม่ซ้ำกัน**
    - **Validates: Requirements 3.1, 6.2, 6.5, 6.9**
    - `names_test.go`: ผสม Upload_Session และ `POST /api/files` พร้อมกัน (`-race`), ชื่อ `.env`, ตัวพิมพ์เล็ก-ใหญ่

  - [ ]* 10.5 เขียน property test ของการตรวจพบการเสียหายหนึ่งบิต
    - **Property 18: ตรวจพบการเสียหายหนึ่งบิตเสมอ**
    - **Validates: Requirements 9.3, 9.6, 9.8, 9.9, 9.11**
    - `corruption_test.go`: ทั้ง complete และ `POST /api/files?sha256=` รวมไฟล์ว่าง ตรวจว่าไม่มี metadata/เนื้อหา/thumbnail/`file.added` และ session ถัดไป 404

  - [ ]* 10.6 เขียน property test ของสถานะ Integrity
    - **Property 19: สถานะ Integrity สอดคล้องกับ checksum จากต้นทาง**
    - **Validates: Requirements 8.4, 8.5, 9.1, 9.2, 9.5, 9.10**
    - `integrity_status_test.go`: ตัวพิมพ์ใหญ่-เล็ก, ไม่ส่ง checksum = `unverified`

  - [ ]* 10.7 เขียน property test ของ checksum ผิดรูป
    - **Property 20: Checksum ที่รูปแบบผิดถูกปฏิเสธโดยไม่แตะข้อมูล**
    - **Validates: Requirements 9.4, 9.7**
    - `checksum_format_test.go`

  - [ ]* 10.8 เขียนเทสต์ตัวอย่างของ `POST /api/files` ที่ปรับแล้ว
    - `legacy_upload_test.go`: Content-Length รู้ขนาด → 507 ก่อนอ่าน body, ไม่รู้ขนาดแล้วดิสก์ลดลง → หยุดและลบไฟล์ชั่วคราว, สิทธิ์ overwrite
    - _Requirements: 6.7, 7.3, 7.4_

- [x] 11. `transfer.Service`: Recover, Sweep และ shutdown
  - [x] 11.1 implement `Recover`, `RunJanitor` และ `BeginShutdown`
    - สร้าง `janitor.go`: `Recover` ตามลำดับในการออกแบบ (ลบ `corrupt` + `slog.Warn`, ตัดสินแต่ละ record: `Valid()` ไม่ผ่าน/partial หาย/สั้นกว่า offset → ลบ, ยาวกว่า → `Truncate`, ลบ partial กำพร้า, `SweepLegacyTemp(now − 1h)`, สร้าง registry/ledger/นับต่ออุปกรณ์ใหม่) เนื้อหาไฟล์ปลายทางที่ไม่มี metadata ให้ log `Warn` เท่านั้น ไม่ลบ
    - `RunJanitor` ทุก 1 นาทีด้วย `Clock`: ลบ session ที่ `now ≥ ExpiresAt(ttl)` และไม่มี append (ตั้ง `dead` ใต้ `rt.mu`), ส่ง `stopped` ถ้าเคยส่ง `running/interrupted`, ลบ Completion_Record ที่เกิน 10 นาที
    - `BeginShutdown(ctx)` เรียก `Interrupt()` ทุก append แล้วรอ persist ≤ 3 วินาที
    - _Requirements: 2.3, 4.2, 4.5, 4.6, 4.7, 4.9, 4.11, 12.8_

  - [ ]* 11.2 เขียน property test ของ resume round trip
    - **Property 1: Resume round trip**
    - **Validates: Requirements 1.8, 2.1, 2.3, 2.4, 2.8, 3.9, 8.3, 8.12**
    - `resume_roundtrip_test.go`: ไบต์สุ่ม, จุดขัดจังหวะหลายจุด, ขนาดก้อนสุ่ม, `Recover` แทรกกลาง; ตรวจเนื้อหา, ความยาว, sha256 ที่เก็บ, checksum ที่ query ได้ทุก offset, และ `fake.Partials` ไม่ถูกอ่านซ้ำส่วนที่ยืนยันแล้ว

  - [ ]* 11.3 เขียน property test ของ invariant Confirmed_Offset
    - **Property 2: Confirmed_Offset invariant**
    - **Validates: Requirements 2.9, 4.6, 4.7, 8.2**
    - `offset_invariant_test.go` ใช้ `rapid` stateful (create, append ขาด, query, restart/Recover, cancel, sweep)

  - [ ]* 11.4 เขียน property test ของการหมดอายุ
    - **Property 13: การหมดอายุของ session**
    - **Validates: Requirements 4.2, 4.3, 12.8**
    - `expiry_test.go` ใช้ `fake.Clock`

  - [ ]* 11.5 เขียน property test ของ Recover
    - **Property 14: Recover ทำให้ดิสก์สอดคล้องกับ record**
    - **Validates: Requirements 4.5, 4.6, 4.7, 4.9, 4.11**
    - `recover_test.go`: ภาพดิสก์สุ่มจาก `fake.Partials` (record เสีย, partial ยาว/สั้น/หาย/กำพร้า, `.upload-*` เก่า/ใหม่)

- [x] 12. Checkpoint B - use case ของ resumable upload พร้อม
  - รัน `gofmt -l .`, `go vet ./...`, `go test -race ./...` ยืนยันว่า `transfer_test.go`, `auth_test.go`, `chat_test.go`, `httpapi_test.go` เดิมผ่าน
  - Ensure all tests pass, ask the user if questions arise.

- [x] 13. HTTP API ของ resumable upload และการเชื่อมใน `main.go`
  - [x] 13.1 สร้าง body adapter และแมป error ใหม่
    - สร้าง `internal/adapter/httpapi/appendbody.go`: `appendBody` ห่อ `r.Body` + `http.ResponseController` มี `Interrupt()` ที่ตั้ง read deadline ย้อนหลัง (`time.Unix(1, 0)`) และ `Read` แปลง `os.ErrDeadlineExceeded` ที่เกิดจาก Interrupt เป็น `domain.ErrInterrupted`
    - แก้ `Server.fail` ใน `internal/adapter/httpapi/middleware.go`: เพิ่ม case ตามตารางใน Components (`offset_mismatch` 409 พร้อม offset, `upload_busy`, `incomplete`, `size_exceeded` 413, `stall_timeout` 408, `too_many_uploads` 429, `insufficient_storage` 507 พร้อม `requiredBytes/availableBytes`, `checksum_mismatch` 422 พร้อมค่าทั้งสอง, `persist_failed` 500 พร้อม offset, `checksum_busy` 503, `checksum_failed` 500); 408 และ `persist_failed` ตั้ง `Connection: close`
    - ห้ามเพิ่ม `r.Header` หรือ `r.URL.RawQuery` ลงใน log ใดๆ (รวม `recoverPanic` และ `fail`)
    - _Requirements: 2.2, 2.5, 2.6, 2.7, 4.12, 5.10, 7.2, 8.9, 9.3_

  - [x] 13.2 เพิ่ม route `/api/uploads...`
    - สร้าง `internal/adapter/httpapi/uploads.go` และลงทะเบียนใน `server.go` ทุก route ผ่าน `s.authed` (401 `unauthorized` เมื่อไม่มี token): `POST /api/uploads` (body JSON ≤ 32 KiB, `size` ถอดรหัสด้วย `json.Number` ปฏิเสธทศนิยม/เลขยกกำลัง/ติดลบ), `GET /api/uploads/{id}`, `PATCH /api/uploads/{id}?offset=N`, `POST /api/uploads/{id}/complete` (body `{sha256?}` ว่างได้, ค่าว่างที่ส่งมา = 400), `DELETE /api/uploads/{id}`
    - อ่าน Resume_Key จาก header `X-Resume-Key` เท่านั้น (ค่าใน path/query/body ไม่ถูกอ่าน; ค่าที่ไม่ใช่ hex 32 ตัวถือเป็นไม่มี key); `{id}` ที่ไม่ผ่าน `^[0-9a-f]{32}$` ให้ 404 เดียวกับกรณีไม่พบ; ทุก 404 ของ session ใช้ response เดียวกัน; สร้าง 201 / complete 201 (ซ้ำ 200) / cancel 204
    - ต้องผ่าน `sameOrigin` middleware เดิมสำหรับ PATCH/DELETE (client ส่ง header ตามที่ middleware ต้องการ)
    - _Requirements: 1.1, 1.9, 2.1, 2.4, 3.1, 3.6, 4.1, 5.3, 5.9, 5.11, 6.1_

  - [x] 13.3 ปรับ `POST /api/files` และเพิ่ม `GET /api/files/{id}/sha256`
    - แก้ handler `upload` ใน `handlers.go`: อ่าน query `sha256` (ผิดรูป/ว่าง → 400 ก่อนอ่าน body), ส่ง Content-Length ให้ use case เพื่อตรวจพื้นที่ล่วงหน้า, เรียกเมธอดใหม่จากงาน 10.1; ผลลัพธ์ DTO มี `sha256` และ `integrity`
    - เพิ่ม `GET /api/files/{id}/sha256` (`200 {sha256, integrity}`) ลง `server.go`
    - _Requirements: 6.1, 6.7, 6.8, 7.3, 8.6, 8.7, 9.6, 9.7_

  - [x] 13.4 เชื่อมทุกอย่างใน `cmd/lanshare/main.go`
    - สร้าง `sessionstore`, `diskspace`, `system.SecretGenerator`, `storage.Guard` (`cfg.MinFree`), เรียก `transferSvc.ConfigureUploads(...)` ด้วย `cfg.UploadTTL` และ `cfg.StallTimeout`; ลำดับตอนเริ่ม: `config.Load` → `MkdirAll(DataDir, 0o700)` → สร้าง adapter/use case → log ค่า config → `Recover` → เริ่ม `RunJanitor` → `net.Listen` → `Serve`
    - ลำดับตอนปิด (SIGINT/SIGTERM): `BeginShutdown` (≤ 3 วินาที) → `srv.Shutdown` (5 วินาทีเดิม แล้ว `Close`) ไม่เพิ่ม `ReadTimeout`/`WriteTimeout`
    - ส่ง dependency ใหม่ผ่าน `httpapi.Deps` (ฟิลด์ใหม่ต้องไม่ทำให้ `httpapi_test.go` เดิมพัง)
    - _Requirements: 4.5, 18.7, 2.3_

  - [ ]* 13.5 เขียน integration test ของ route ใหม่
    - `internal/adapter/httpapi/uploads_test.go` (`package httpapi_test`, filestore/jsonstore/sessionstore จริงใน `t.TempDir()`): status และ error code ตามตาราง, 401 ทุก route, 403 บทบาทไม่มีสิทธิ์, header-only key, ตัดการเชื่อมต่อกลางทางแล้ว query/append ต่อ (ใช้ `Interrupt` จริงผ่าน `ResponseController`), 408 stall (ตั้ง `StallTimeout` สั้นในเทสต์), เส้นทาง checksum ผิด/ถูก, ไฟล์ขนาด 0, ไฟล์ list มี `sha256/integrity`
    - _Requirements: 1.1, 2.1, 2.7, 3.1, 5.9, 6.6, 9.3_

  - [ ]* 13.6 เขียน property test ของความลับ Resume_Key
    - **Property 16: ความลับของ Resume_Key**
    - **Validates: Requirements 1.9, 5.3, 5.10, 5.11, 5.7**
    - `uploads_secret_test.go`: ใช้ `slog` handler จับ log ทั้งหมดและสแกน window ยาว 8 ตัวของ key ในทุก response, event, log รวมคำขอที่ล้มเหลว; key ใน path/query/body → 404; ID กับ key ไม่เท่ากันและไม่เป็น prefix ของกัน

  - [ ]* 13.7 เขียนเทสต์การแยกห้องและ lockout ระดับ HTTP
    - `uploads_isolation_test.go`: Owner ห้อง A ใช้ id/key ของห้อง B ได้ 404 เดียวกับ id ที่ไม่มี; ไฟล์ checksum ข้ามห้อง 404; ยืนยันว่าเทสต์ PIN lockout และ cross-room เดิมใน `httpapi_test.go` ยังผ่านและไม่ถูกแก้ให้อ่อนลง
    - _Requirements: 5.1, 5.8, 8.11_

- [x] 14. Checkpoint C - ฝั่งเซิร์ฟเวอร์ของ resumable upload ใช้งานได้ครบ
  - รัน `gofmt -l .`, `go vet ./...`, `go test -race ./...`
  - Ensure all tests pass, ask the user if questions arise.

- [x] 15. `power.Guard` (กัน Mac หลับ)
  - [x] 15.1 สร้าง `internal/usecase/power/guard.go`
    - `Guard` รับ `port.Clock`, `port.CaffeinateLauncher`, `pid`, `grace` (30 วินาที), `enabled` (`-keep-awake`): `Begin() (end func())`, `Hold(key, ttl)`, `Release(key)`, `Step(now)`, `Status()`, `Run(ctx)` (ticker 500 ms), `Stop(ctx)`; implement `port.ActivityTracker`
    - กฎ: ถือ assertion ก็ต่อเมื่อ `enabled && (n > 0 || (everActive && now − lastEnd < grace))` (strict) ถือ process ไม่เกินหนึ่งตัว, Hold หมดอายุใช้เวลาหมดอายุเป็น `lastEnd`; Start ล้มเหลวหรือ process จบเอง → `slog.Warn` หนึ่งครั้งต่อความล้มเหลว, สถานะ `unavailable`, การถ่ายโอนไม่ถูกขัดจังหวะ, ไม่ retry จนกว่า 0→1 ครั้งถัดไป; `disabled` ไม่เริ่ม process เลย
    - _Requirements: 13.1, 13.2, 13.3, 13.4, 13.7, 13.8, 13.11_

  - [ ]* 15.2 เขียน property test ของ Power_Guard
    - **Property 24: Power_Guard ถือ assertion ตามจำนวน Active_Transfer**
    - **Validates: Requirements 13.1, 13.2, 13.3, 13.4, 13.8, 13.9**
    - `guard_test.go` ใช้ `fake.Clock` และ `fake.Launcher`: ลำดับ Begin/End/Hold/Release/เดินนาฬิกา; ตรวจจำนวน process ≤ 1 และไม่เริ่มใหม่ขณะยังถือ; ตัวอย่าง Start ล้มเหลว/จบเอง → `unavailable` แล้วกลับ `active`

  - [x] 15.3 สร้าง adapter `internal/adapter/caffeinate`
    - implement `port.CaffeinateLauncher`: `exec.Command("/usr/bin/caffeinate", "-i", "-w", strconv.Itoa(pid))`, goroutine `Wait` ปิด `Exited()`, `Stop(ctx)` ส่งสัญญาณและรอ exit ตามเวลาของ ctx
    - _Requirements: 13.1, 13.5, 13.6_

  - [x] 15.4 เชื่อม `power.Guard` และ endpoint สถานะ
    - แก้ `main.go`: สร้าง `power.Guard` (`cfg.KeepAwake`, `os.Getpid()`), ส่งเป็น `Activity` ให้ `ConfigureUploads`, `go power.Run(ctx)`, ตอนปิดเรียก `power.Stop` (ctx 2 วินาที) **หลัง** `srv.Shutdown`
    - เพิ่ม `GET /api/admin/power` (`200 {status}`; Owner เท่านั้น มิฉะนั้น 403) ใน `internal/adapter/httpapi/admin.go` และ `server.go` โดยตรวจ Owner ใน use case/handler ตามแบบ endpoint admin เดิม และเพิ่ม `Deps` ที่จำเป็น
    - _Requirements: 13.1, 13.5, 13.10_

  - [ ]* 15.5 เขียน integration test ของ `caffeinate` จริง
    - `internal/adapter/caffeinate/caffeinate_integration_test.go` (`//go:build integration && darwin`): เริ่ม/หยุดภายใน 2 วินาที และตรวจ argv มี `-i -w <pid>` การทดสอบ `kill -9` ที่ปล่อย assertion ภายใน 5 วินาทีเป็นรายการตรวจด้วยมือ (ดู Notes)
    - _Requirements: 13.5, 13.6_

- [x] 16. `diagnostics.Service` (ตรวจปัญหาการเชื่อมต่อสำหรับ Owner)
  - [x] 16.1 เพิ่ม `PrefixLen` และ `LocalAddrs` ใน `netinfo`
    - แก้ `internal/adapter/netinfo/netinfo.go`: เติม `PrefixLen` จาก `ipnet.Mask.Size()`, implement `port.LocalAddrProvider` (IPv4 และ IPv6 ทุก address ของทุก interface)
    - เทสต์/โค้ดเดิมที่ใช้ `NetInterface` ต้องคอมไพล์ผ่านต่อ
    - _Requirements: 14.3, 14.7_

  - [x] 16.2 สร้าง adapter `cmdrunner` และ `peerlog`
    - `internal/adapter/cmdrunner`: implement `port.CommandRunner` ด้วย `exec.CommandContext` (argv ตายตัว ไม่ผ่าน shell), kill เมื่อ ctx หมดเวลา, จำกัด stdout 1 MiB, แยก error เป็น `exec.ErrNotFound` / `*exec.ExitError` / `context.DeadlineExceeded` ให้ใช้ `errors.Is/As`
    - `internal/adapter/peerlog`: implement `port.PeerLog` เป็น `map[netip.Addr]time.Time` สูงสุด 256 รายการ ตัดข้อมูลเก่ากว่า 10 นาทีทุกครั้งที่ `Record/Since`
    - _Requirements: 14.7, 14.16, 14.20_

  - [x] 16.3 เขียน parser และตัวจัดประเภทแบบฟังก์ชันล้วน
    - สร้าง `internal/usecase/diagnostics/parse.go` และ `reach.go`: parse `socketfilterfw` (state, appblocked; ข้อความที่ไม่รู้จัก → `unknown` พร้อม reason `parse_failed` ไม่เดา), parse `networksetup -listallhardwareports`, หา channel ของ interface ใน JSON ของ `system_profiler` แล้วแยก band จากสตริง `<n> (<band>GHz, <width>MHz)` (ห้ามอ่านส่วน "Other Local Wi-Fi Networks"), `ClassifyReach(peers, locals, active)`
    - ใช้ fixture จากงาน 1.4 ในเทสต์ของ parser และถ้าผู้ใช้ยังไม่ได้เก็บ fixture กรณี Firewall เปิด ให้คงพฤติกรรม `unknown` ไว้และบันทึกใน Notes
    - _Requirements: 14.4, 14.5, 14.6, 14.7, 14.8, 14.9, 14.10, 14.11, 14.12_

  - [x] 16.4 implement `diagnostics.Service`
    - สร้าง `service.go` และ `checks_*.go`: 7 checks (`interface`, `firewall`, `reachability`, `wifi_band`, `disk`, `power`, `secure_context`) รันพร้อมกันเป็น goroutine, timeout ต่อคำสั่ง 3 วินาที (`system_profiler` 10 วินาที), เพดานรวม 12 วินาที (ที่ยังไม่เสร็จ → `unknown` reason `timeout`, ใช้ channel แบบ buffered กัน goroutine รั่ว)
    - Owner เท่านั้น (`ErrForbidden` ก่อนรันสิ่งใด); cache 30 วินาที (`Fresh` ข้าม cache) พร้อม single-flight; `secure_context` ไม่เข้า cache; ความล้มเหลวของ check หนึ่งเป็น `unknown` เฉพาะ check นั้น; `detail/hint` สร้างจากค่าที่ parse แล้วเท่านั้น ห้ามใส่ stdout ดิบ, SSID, PIN, token, Resume_Key, ชื่อไฟล์; ใช้ `Clock` ที่ฉีด
    - ใช้ `storage.Guard.Snapshot`, `transfer.PartialBytes`, `power.Status`, `network.Service`
    - _Requirements: 14.1, 14.2, 14.3, 14.13, 14.14, 14.15, 14.16, 14.17, 14.18, 14.20, 14.22_

  - [ ]* 16.5 เขียน property test ของ check พื้นที่ดิสก์และ interface
    - **Property 27: สถานะ check พื้นที่ดิสก์**
    - **Validates: Requirements 14.13**
    - **Property 28: สถานะ check interface**
    - **Validates: Requirements 14.3**
    - `checks_test.go`

  - [ ]* 16.6 เขียน property test ของ reachability
    - **Property 29: การจัดประเภท reachability**
    - **Validates: Requirements 14.7, 14.8, 14.9**
    - `reach_test.go`

  - [ ]* 16.7 เขียน property test ของการแยก band
    - **Property 30: การแยก band จากสตริง channel**
    - **Validates: Requirements 14.10, 14.11, 14.12**
    - `parse_test.go` รวม fixture จริงจากงาน 1.4 และสตริงสุ่มที่ต้องไม่ทำให้ panic

  - [ ]* 16.8 เขียน property test ของรายงานและการแยกความล้มเหลว
    - **Property 31: รูปร่างและการแยกความล้มเหลวของรายงาน diagnostics**
    - **Validates: Requirements 14.1, 14.18, 14.19, 14.20**
    - `service_test.go` ใช้ `fake.Runner`: ชุดย่อยของคำสั่งที่ล้มเหลว/ค้าง/ผลลัพธ์อันตราย, 7 รายการ id ไม่ซ้ำ, `warn|fail` มี hint, ไม่มีข้อความลับที่ฝังไว้

  - [x] 16.9 เปิด endpoint admin และ middleware `recordPeer`
    - แก้ `middleware.go`: `recordPeer` เป็นชั้นนอกสุดของ `chain` เรียก `PeerLog.Record(clientIP, now)` ทุก request (รวมไฟล์ static และ `/ws`); แก้ `admin.go`/`server.go`: `GET /api/admin/diagnostics[?fresh=1]`, `GET /api/admin/uploads`, `DELETE /api/admin/uploads/{id}` ตอบตามตาราง HTTP API (Owner เท่านั้น, 403 `forbidden` สำหรับบทบาทอื่นโดยไม่รันคำสั่งหรือยกเลิกอะไร)
    - ตรวจ `GET/POST /api/admin/network` เดิมว่า non-Owner ได้ 403 และการเลือก interface ไม่เปลี่ยน (แก้เฉพาะถ้าจำเป็น); เพิ่ม `Deps` และ wiring ใน `main.go` (`cmdrunner`, `peerlog`, `diagnostics.Service`, `os.Executable()`)
    - _Requirements: 14.2, 14.7, 14.19, 14.21, 14.22, 15.3_

  - [ ]* 16.10 เขียน integration test ของ endpoint admin
    - `internal/adapter/httpapi/admin_test.go`: non-Owner ทุก endpoint → 403 โดย `fake.Runner` ไม่ถูกเรียก, Owner ห้อง A ไม่เห็น/ยกเลิก session ของห้อง B, รายการไม่มี Resume_Key และชื่อไฟล์, cache 30 วินาที/`fresh=1`
    - _Requirements: 14.2, 14.19, 14.21, 14.22, 15.3_

  - [ ]* 16.11 เขียน integration test ของคำสั่ง macOS จริง
    - `internal/usecase/diagnostics/macos_integration_test.go` (`//go:build integration && darwin`): รันคำสั่งจริงและตรวจว่าได้ผลที่ parse ได้หรือ `unknown` พร้อม reason โดยไม่ panic
    - _Requirements: 14.4, 14.10, 14.20_

- [x] 17. Checkpoint D - backend ครบ
  - รัน `gofmt -l .`, `go vet ./...`, `go test -race ./...`; ตรวจว่า `go build ./...` ไม่มีโค้ดเชื่อมไม่ครบ (ทุก port ใหม่มี adapter, fake และ wiring)
  - Ensure all tests pass, ask the user if questions arise.

- [x] 18. Frontend: ตรรกะล้วนที่ทดสอบได้
  - [x] 18.1 สร้าง `web/logic.js` และเชื่อมเข้าหน้าเว็บ
    - ฟังก์ชันล้วน (ไม่แตะ DOM, export ด้วย `module.exports` เมื่อรันใน Node): ตัวจัดตาราง retry (1, 2, 4, 8, 15, 15… วินาที, รีเซ็ตเป็น 1 เมื่อ offset เพิ่ม), ตัวแบ่งก้อน `Blob.slice` (ก้อน ≤ 16 MiB, ไม่เกินเพดาน 32 MiB, หนึ่ง request ต่อ session), `classifyResult(status, code | networkError | noProgress45s)` ตามตารางการตอบสนอง, ตัวนับ 409 ติดกัน 3 ครั้ง, ตัวจับคู่ไฟล์ที่ค้าง (เทียบ `name` และ `size` ไม่เทียบ `lastModified`), `formatBytes` (ไบนารี ทศนิยม 1 ตำแหน่ง), ความคืบหน้าที่ไม่ลดลง (`max(offset, bytes จาก event)`), `groupNotifications` (หน้าต่าง 5 วินาที, `"{N} ไฟล์ใหม่"`), `titleCount` (`99+`), `isNotifiable`, `buildDiagnosticRows` / `buildReport` (status ไม่รู้จัก → "ไม่ทราบ", ฟิลด์ว่างถูกละ, รายงานไม่มีข้อมูลลับ)
    - เพิ่ม `<script src="/logic.js"></script>` ก่อน `app.js` ใน `index.html` (ไม่มี inline script) และ **เพิ่ม `logic.js` ลงบรรทัด `//go:embed` ใน `web/embed.go`**
    - _Requirements: 7.7, 10.11, 11.1, 11.2, 11.5, 11.9, 11.11, 11.12, 11.13, 15.5, 15.8, 15.9, 15.13, 17.1, 17.3, 17.7, 17.8_

  - [ ]* 18.2 เขียนเทสต์ตัวจัดตาราง retry, ตัวแบ่งก้อน, ความคืบหน้า และ `formatBytes`
    - **Property 33: ตัวจัดตาราง retry ความคืบหน้า และการแบ่งก้อน (JS)**
    - **Validates: Requirements 7.7, 11.2, 11.7, 11.11**
    - `web/test/logic-upload.test.js` ด้วย `node --test` และลูปสุ่ม ≥ 100 รอบเขียนเอง (ไม่มี fast-check)

  - [ ]* 18.3 เขียนเทสต์ตัวจัดประเภทผลลัพธ์การอัปโหลด
    - **Property 34: การจัดประเภทผลลัพธ์ของการอัปโหลด (JS)**
    - **Validates: Requirements 11.1, 11.5, 11.6, 11.12, 11.13**
    - `web/test/logic-classify.test.js`

  - [ ]* 18.4 เขียนเทสต์ตัวจับคู่ไฟล์ที่ค้าง
    - **Property 35: การจับคู่ไฟล์กับรายการอัปโหลดที่ค้าง (JS)**
    - **Validates: Requirements 11.9**
    - `web/test/logic-match.test.js`

  - [ ]* 18.5 เขียนเทสต์ตรรกะการแจ้งเตือนไฟล์ใหม่
    - **Property 37: ตรรกะการแจ้งเตือนไฟล์ใหม่ (JS)**
    - **Validates: Requirements 17.1, 17.3, 17.7, 17.8, 17.12**
    - `web/test/logic-notify.test.js`

  - [ ]* 18.6 เขียนเทสต์ตัวสร้างแถวและรายงาน diagnostics
    - **Property 38: การแสดงผลและรายงาน diagnostics (JS)**
    - **Validates: Requirements 15.5, 15.8, 15.9, 15.13, 15.19, 15.20**
    - `web/test/logic-diagnostics.test.js`

- [x] 19. Frontend: uploader ที่ต่อได้ และหน้า Owner
  - [x] 19.1 สร้าง uploader engine ใน `web/app.js`
    - แทน `startUpload` เดิม: `POST /api/uploads` → ส่งทีละก้อน `Blob.slice` ≤ 16 MiB ด้วย `XMLHttpRequest` `PATCH` (header `X-Resume-Key`, header ที่ `sameOrigin` ต้องการ) หนึ่ง request ต่อ session; เก็บ `{id, key, name, size, lastModified, dir, conflict, roomId, createdAt}` ใน `localStorage` (`lanshare.uploads.v1`) **ก่อน** PATCH แรก; ลบเมื่อ complete/cancel/หมดอายุ; ห้ามแสดง key บนหน้าหรือใส่ใน URL
    - คำนวณ SHA-256 ใน `hash-worker.js` ขนานกับการส่ง แล้วเรียก complete พร้อม `{sha256}` เมื่อทั้งสองเงื่อนไขครบ (ส่งครบและแฮชครบ); แสดง "กำลังคำนวณ SHA-256" และเปอร์เซ็นต์ไม่ต่ำกว่าทุก 500 ms; ความคืบหน้าที่แสดงมาจาก offset ที่เซิร์ฟเวอร์ยืนยันและ event `transfer` ของ id ตัวเอง (แก้ `onRemoteTransfer` ให้ไม่ข้าม event ของตัวเองที่ id ตรงกับ session ที่กำลังส่ง)
    - ตั้งค่า "ตรวจ SHA-256" (`localStorage` `lanshare.verify`, ค่าเริ่มต้นตามผลงาน 1.3) มีผลกับไฟล์ที่เข้าคิวหลังเปลี่ยน; สร้าง Worker ไม่ได้/อ่านไฟล์ไม่ได้ → ส่ง complete โดยไม่มี sha256 พร้อมข้อความว่าข้ามการตรวจ
    - ข้อความผู้ใช้ทุกจุดใช้ `textContent`; 422 `checksum_mismatch` → สถานะ "ไฟล์ไม่ตรงกัน" พร้อมปุ่ม "ส่งใหม่" (แฮชใหม่จาก 0 และสร้าง session ใหม่ ไม่ resend อัตโนมัติ)
    - แก้ `index.html`/`style.css` เท่าที่จำเป็น
    - _Requirements: 10.1, 10.2, 10.3, 10.4, 10.8, 10.9, 10.10, 10.14, 10.15, 11.7, 11.8, 11.11_

  - [x] 19.2 เพิ่มการต่ออัปโหลดอัตโนมัติและตารางตอบสนองต่อ error
    - ใช้ตัวจัดประเภทจาก `logic.js`: network error / ไม่มี progress 45 วินาที / 408 / 5xx ที่ไม่ใช่ 507 → สถานะ "กำลังเชื่อมต่อใหม่", `GET /api/uploads/{id}` แล้วส่งต่อจาก offset ที่ยืนยัน, รอตามตาราง; event `online` ยกเลิกการรอและลองใหม่ ≤ 1 วินาที (ยกเว้น item ที่ `paused`); 10 นาทีโดย offset ไม่เพิ่ม → `paused` พร้อมปุ่ม "ต่อ"
    - 409 `offset_mismatch`/`upload_busy` → ส่งต่อทันที (3 ครั้งติดโดย offset ไม่เพิ่ม → จัดเป็น retry); 404 → ลบรายการ แจ้งว่าหมดอายุ สร้าง session ใหม่จาก 0 (3 ครั้งติดโดยไม่มีไบต์ → `paused`); 401 → `paused` คง key และเมื่อเข้าห้องใหม่ query แล้วส่งต่อโดยไม่ต้องเลือกไฟล์ซ้ำถ้ายังถือ `File`; 507 → หยุด แสดง "พื้นที่ดิสก์ไม่พอ: ต้องการ {requiredBytes} มี {availableBytes}" คงรายการไว้ให้กด "ต่อ"; 422 และ 4xx อื่น → หยุดไม่ retry
    - ถ้า response ของ complete หาย ให้ส่ง complete ซ้ำด้วย key เดิม (ไม่ query) และเมื่อได้ 404 หลัง complete ให้รีเฟรชรายการไฟล์
    - _Requirements: 7.7, 11.1, 11.2, 11.3, 11.4, 11.5, 11.6, 11.12, 11.13, 11.14_

  - [x] 19.3 เพิ่มรายการ "อัปโหลดที่ค้างอยู่", การเลือกไฟล์เดิม และการยกเลิก
    - เมื่อโหลดหน้า แสดงรายการจาก `localStorage`; รับไฟล์ที่เลือกเมื่อ `name` และ `size` ตรง (ไม่ตรง → ข้อความ "ไฟล์ไม่ตรง" และคงรายการ); query แล้วถ้า offset > 0 และเปิดการตรวจ ให้แฮช `[0, offset)` ในเครื่องเทียบกับ `sha256` จากเซิร์ฟเวอร์ (ไม่สนตัวพิมพ์): ตรง → ส่งต่อ; ไม่ตรงหรือขนาดไฟล์ต่างจาก `size` → `DELETE` (ไม่ว่าผลเป็นอย่างไร รวม 404) แล้วเริ่ม session ใหม่จาก 0 พร้อมข้อความว่าไฟล์ที่เลือกไม่ตรงกับไฟล์ที่ส่งไปบางส่วน; query 404 → ลบรายการโดยไม่ส่ง cancel และเริ่มใหม่
    - ปุ่มยกเลิก: abort request, หยุด retry, ส่ง `DELETE` หนึ่งครั้ง, ลบรายการแม้ cancel ล้มเหลว
    - _Requirements: 10.5, 10.6, 10.7, 11.9, 11.10_

  - [x] 19.4 แสดงสถานะ "หยุดชั่วคราว" จาก event ของอุปกรณ์อื่น
    - แก้การจัดการ event `transfer` ใน `app.js`: `interrupted` ของ `deviceId` อื่น แสดง "หยุดชั่วคราว" พร้อม `bytes` จาก `total` และคงไว้จนมี event ใหม่ของ `id` เดียวกัน; event `running|done|failed|stopped` ของ `id` เดียวกันแทนข้อความนั้นบนแถวเดิมโดยไม่สร้างแถวซ้ำ (แก้ตัวจัดการเดิมที่ลบแถวที่ไม่ใช่ `running` ใน 4-8 วินาที เฉพาะกรณี `interrupted`)
    - ชื่อเหตุการณ์ใช้สตริงชุดเดียวกับ `port`
    - _Requirements: 12.6, 12.7_

  - [x] 19.5 เพิ่มป้ายความถูกต้อง รายละเอียด checksum และการเทียบไฟล์ในเครื่อง
    - ใน `renderFiles` แสดงป้ายไอคอนพร้อมข้อความ: "ตรวจแล้ว" (`verified`), "ยังไม่ได้ตรวจกับต้นทาง" (`unverified`), ไม่มีป้ายสำหรับ `unknown`/ไม่มีค่า
    - หน้ารายละเอียดไฟล์แสดง checksum เต็ม 64 ตัวพร้อมปุ่มคัดลอก (ยืนยัน 2 วินาที; ถ้า clipboard ใช้ไม่ได้ให้เลือกข้อความและแจ้งให้คัดลอกเอง), ปุ่ม "คำนวณ" สำหรับไฟล์ `unknown` เรียก `GET /api/files/{id}/sha256`, และปุ่มเลือกไฟล์ในเครื่องมาเทียบด้วยวิธีแฮชแบบเดียวกัน (Worker สร้างไม่ได้ → แจ้งว่าเทียบไม่ได้)
    - _Requirements: 10.11, 10.12, 10.13, 10.15, 8.6_

  - [x] 19.6 สร้างหน้า "ตรวจปัญหาการเชื่อมต่อ" และแผงสถานะสำหรับ Owner
    - ใน `setupOwner()` สร้างเมนู/pane ด้วย `el()` เฉพาะเมื่อ `role() === 'owner'` (บทบาทอื่นไม่มี DOM node และไม่ส่ง request ใดไปยัง endpoint นี้); เปิดหน้าส่ง `GET /api/admin/diagnostics` หนึ่งครั้ง; "ตรวจใหม่" ใช้ `?fresh=1` ปิดปุ่มระหว่างโหลด คงผลเดิมพร้อมป้าย "เก่า" จนได้ผลใหม่; timeout 30 วินาทีด้วย `AbortController` จัดหมวด (เครือข่าย/หมดเวลา/เซิร์ฟเวอร์)
    - แถวสถานะใช้ไอคอนรูปร่างต่างกัน (`aria-hidden="true"`) กับข้อความ "ปกติ/ควรตรวจสอบ/ผิดปกติ/ไม่ทราบ" ในแถว, live region `role="status"` ประกาศโดยไม่ย้าย focus, ได้ 403 → ปิดหน้า ลบเมนู ล้างผล แจ้งว่าเป็นฟังก์ชันเฉพาะ Owner; "คัดลอกรายงาน" ใช้ `buildReport` (ข้อความ "คัดลอกแล้ว" ≥ 3 วินาที, ล้มเหลว → กล่องข้อความที่เลือกได้); รายการ interface ใช้ `GET/POST /api/admin/network` เดิมแล้วขอผลใหม่
    - poll `GET /api/admin/power` ทุก 3 วินาทีขณะแผง Owner เปิดอยู่ แสดง "กำลังทำงาน/ว่าง/ปิดอยู่/ใช้ไม่ได้"; แสดงรายการอัปโหลดค้าง (`/api/admin/uploads`) พร้อมปุ่มยกเลิก (ไม่แสดงชื่อไฟล์)
    - _Requirements: 13.10, 14.19, 14.21, 15.1, 15.2, 15.4, 15.5, 15.6, 15.7, 15.10, 15.11, 15.12, 15.13, 15.14, 15.15, 15.16, 15.17, 15.18_

- [x] 20. PWA และการแจ้งเตือนไฟล์ใหม่
  - [x] 20.1 สร้างไอคอน manifest และ Service Worker
    - สร้าง `tools/genicons/main.go` (`//go:build ignore`, `go run tools/genicons/main.go`) ด้วย `image/png` สร้าง `web/icons/icon-192.png` (192x192), `icon-512.png` (512x512), `icon-maskable-512.png` (512x512), `apple-touch-icon.png` (180x180) แล้ว commit ไฟล์ PNG
    - สร้าง `web/manifest.webmanifest` ตามฟิลด์ใน Data Models (`short_name` ≤ 12 ตัว, `theme_color`/`background_color` ตรงกับสีหลักใน `style.css`)
    - สร้าง `web/sw.js`: `ALLOW` ตามการออกแบบ (รวม `/sha256.js`, `/hash-core.js`, `/hash-worker.js`, `/logic.js`), `isCacheable(url, method, origin)` (GET + origin เดียวกัน + `pathname` ตรง allow-list), network-first, `put` เฉพาะ `status === 200 && !response.redirected`, ใช้ cache เมื่อ network error เท่านั้น, ชื่อ cache `lanshare-shell-__SHELL_VERSION__`, ตอน `activate` ลบ cache `lanshare-shell-*` ที่ไม่ใช่ของตน, `/api/*`, `/ws`, `/sw.js` ไม่แตะ cache เลย; export `isCacheable` ด้วย `module.exports` ให้ Node ทดสอบ
    - _Requirements: 16.2, 16.3, 16.9, 16.10, 16.11, 16.12_

  - [x] 20.2 เพิ่มไฟล์ PWA ลงใน `//go:embed`
    - แก้ `web/embed.go` ให้รวม `manifest.webmanifest sw.js icons` (โฟลเดอร์ `icons` ทั้งโฟลเดอร์) ต่อจากรายการเดิม (`index.html app.js style.css sha256.js hash-core.js hash-worker.js logic.js`) ห้ามรวม `web/test/`
    - ตรวจ `go build ./...`
    - _Requirements: 16.5_

  - [x] 20.3 เสิร์ฟ manifest, `sw.js` และไอคอนด้วย header ที่ถูกต้อง
    - สร้าง `internal/adapter/httpapi/static.go` และลงทะเบียนใน `server.go`: `/manifest.webmanifest` → `application/manifest+json`, `/sw.js` → `text/javascript` (แทน `__SHELL_VERSION__` ด้วย 12 ตัวแรกของ SHA-256 ของเนื้อหาไฟล์ใน allow-list เรียงชื่อ คำนวณครั้งเดียวตอนเริ่ม), `/icons/*.png` → `image/png`; ทุกตัว `Cache-Control: no-cache` กำหนด Content-Type เองไม่พึ่ง mime database; ไม่เปลี่ยน CSP เดิม
    - _Requirements: 16.1, 16.3, 16.5, 16.6, 16.12_

  - [x] 20.4 เพิ่มแท็กใน `index.html`, ลงทะเบียน Service Worker เฉพาะ Secure_Context และหน้าเชื่อมต่อไม่ได้
    - `index.html` `<head>`: `<link rel="manifest" href="/manifest.webmanifest">`, `<meta name="theme-color">` (ค่าตรง manifest), `<link rel="apple-touch-icon" href="/icons/apple-touch-icon.png">` อย่างละหนึ่ง ไม่มี inline script/style/handler
    - `app.js`: หลัง `load` เรียก `navigator.serviceWorker.register('/sw.js', {scope: '/'})` เฉพาะเมื่อ `window.isSecureContext && 'serviceWorker' in navigator` และ `.catch(() => {})`; นอกเหนือจากนี้ไม่แสดง error ไม่โยน exception และฟีเจอร์หลักทำงานต่อ; ไม่ใช่ Secure_Context → แสดงในตั้งค่า "การติดตั้งแอปและการแจ้งเตือนของระบบต้องใช้ HTTPS" โดยไม่มีปุ่มหรือข้อความติดตั้ง
    - `boot()`: API แรกล้มเหลวด้วย network error หรือเงียบเกิน 10 วินาที → แสดง "เชื่อมต่อเซิร์ฟเวอร์ไม่ได้" และปุ่ม "ลองใหม่" โดยไม่เรนเดอร์ข้อมูลใดๆ; ปุ่มลองใหม่ทำซ้ำคำขอและแทนที่ด้วยหน้าปกติเมื่อสำเร็จ
    - _Requirements: 16.4, 16.7, 16.8, 16.13, 16.14, 16.15_

  - [x] 20.5 เพิ่มการแจ้งเตือนไฟล์ใหม่
    - `handleEvent('file.added')` เก็บ id ใน `pendingAdded` แล้วเรียก `refreshFiles()` เดิม; ไฟล์ใน `pendingAdded` ที่ปรากฏในรายการและไม่ใช่ของตนเองเป็น notifiable (ไม่พบ → ข้ามเงียบ; เพิ่มระหว่าง WebSocket ขาด → ไม่แจ้ง); บทบาท `uploader` ไม่ขอ metadata ไม่แจ้งไม่นับ
    - หน้า visible → แถบแจ้งในหน้า (ปิดเองใน 5 วินาทีหรือเมื่อผู้ใช้ปิด); hidden → เพิ่มตัวนับ `document.title` (`"(N) "`, `99+`) และรีเซ็ตเมื่อกลับมา visible; รวมกลุ่มภายใน 5 วินาทีเป็น `"{N} ไฟล์ใหม่"`
    - การแจ้งเตือนระบบ: แสดงตัวควบคุมเฉพาะ `isSecureContext && 'Notification' in window` และไม่ใช่ `uploader`; ขอสิทธิ์เฉพาะตอนผู้ใช้กดเปิด (ไม่ได้รับสิทธิ์ → คงปิดพร้อมข้อความไทย), เก็บ `lanshare.notify` (ค่าเริ่มต้นปิด), คลิกแล้ว focus หน้าและเปิดรายการไฟล์; ตั้งค่าแสดง "การแจ้งเตือนทำงานเฉพาะขณะเปิดหน้านี้ไว้เท่านั้น" เสมอ
    - _Requirements: 17.1, 17.2, 17.3, 17.4, 17.5, 17.6, 17.7, 17.8, 17.9, 17.10, 17.11, 17.12, 17.13_

  - [ ]* 20.6 เขียนเทสต์นโยบาย cache ของ Service Worker
    - **Property 36: นโยบาย cache ของ Service Worker**
    - **Validates: Requirements 16.9, 16.10, 16.12, 16.16**
    - `web/test/sw.test.js` ด้วย `node --test`: `isCacheable` กับ URL/method/origin สุ่ม (รวม `/api/..`, `/ws`, `/sw.js`, พาธนอก allow-list), ค่า `<version>` เปลี่ยนเมื่อไฟล์ใน allow-list เปลี่ยนและคงเดิมเมื่อไม่เปลี่ยน

  - [ ]* 20.7 เขียนเทสต์ฝั่ง Go ของไฟล์ static และ PWA
    - `internal/adapter/httpapi/static_test.go`: status/Content-Type/`Cache-Control` ของ `/manifest.webmanifest`, `/sw.js`, `/icons/*.png`; manifest มีฟิลด์ครบและ `short_name` ≤ 12 ตัว; decode PNG ตรวจขนาดพิกเซลทั้ง 4 ไฟล์; `index.html` มีแท็กตามที่กำหนดอย่างละหนึ่ง และไม่มี inline `<script>`/`<style>`/`on*=`; header CSP เท่าเดิม
    - _Requirements: 16.1, 16.2, 16.3, 16.4, 16.6_

  - [ ]* 20.8 เขียนเทสต์ว่าไบนารีเสิร์ฟไฟล์ web โดยไม่มีโฟลเดอร์ `web/` บนดิสก์
    - `web/embed_test.go`: ตรวจว่า `Files` มีทุกไฟล์ที่คาดไว้ (`index.html app.js style.css sha256.js hash-core.js hash-worker.js logic.js manifest.webmanifest sw.js icons/*.png`) และไม่มีไฟล์ใต้ `test/`
    - `cmd/lanshare/embed_integration_test.go` (`//go:build integration`): `go build` ไบนารีไปยัง temp dir, รันจากไดเรกทอรีว่างที่ไม่มี `web/` ด้วย `-data` ใน temp และพอร์ตว่าง, ขอ `/`, `/sw.js`, `/manifest.webmanifest`, `/icons/icon-192.png`, `/sha256.js` ต้องได้ 200
    - _Requirements: 16.5_

- [x] 21. Checkpoint E - frontend และ PWA
  - รัน `gofmt -l .`, `go vet ./...`, `go test -race ./...` และ `node --test web/test/`; `go test -tags integration ./...` เมื่อรันบน Mac นี้ได้
  - Ensure all tests pass, ask the user if questions arise.

- [x] 22. เอกสารและ steering ให้ตรงกับสิ่งที่สร้างจริง
  - [x] 22.1 อัปเดต `docs/project-scope.md`
    - หัวข้อ 11 ตารางสถานะ: Phase C = เสร็จ (หรือเสร็จบางส่วนถ้า 20.x เลื่อน) และเพิ่ม "รายละเอียด Phase C ที่ทำจริง" (resumable upload, SHA-256 สองฝั่ง, Power_Guard, diagnostics, PWA/การแจ้งเตือน, ข้อจำกัดบน HTTP), flag ใหม่ `-min-free`, `-upload-ttl`, `-stall-timeout`, `-keep-awake` พร้อมค่าเริ่มต้นและช่วง, endpoint ใหม่ และผลวัด SHA-256 บนมือถือ (จากงาน 1.3)
    - เขียนเป็นภาษาไทย คงสอดคล้องกับรูปแบบเดิมของไฟล์
    - _Requirements: 18.1, 18.2, 18.3_

  - [x] 22.2 อัปเดต `.kiro/steering/tech.md`
    - บรรทัด Config flag เพิ่ม `-min-free`, `-upload-ttl`, `-stall-timeout`, `-keep-awake`; เพิ่ม `pgregory.net/rapid` (เวอร์ชันที่ปักไว้) เป็น dependency เฉพาะการทดสอบ; เพิ่มคำสั่ง `node --test web/test/` และ `go test -tags integration ./...`; ปรับรายการไฟล์ใน `//go:embed` ให้ตรงของจริง; บันทึกไฟล์ข้อมูลใหม่ `sessions/`, `partial/` ใต้โฟลเดอร์ข้อมูล
    - _Requirements: 18.1_

  - [x] 22.3 อัปเดต `.kiro/steering/product.md`
    - บรรทัดสถานะ: Phase C เสร็จ (ตามที่สร้างจริง) และปรับรายการ "ยังไม่เริ่ม" เป็น D, E, F, G; เพิ่มฟีเจอร์ resumable upload, การตรวจ SHA-256, กัน Mac หลับ, หน้าตรวจปัญหาการเชื่อมต่อ ในรายการฟีเจอร์; คงข้อห้ามเรื่องผู้ใช้ที่ไม่น่าไว้ใจจนกว่า Phase D เสร็จ
    - _Requirements: 18.1_

- [x] 23. Final checkpoint - Ensure all tests pass
  - รัน `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test -race ./...` และ `node --test web/test/` ตรวจว่าไม่มี `fast-check`, `package.json` หรือไฟล์ทดสอบใต้ `web/test/` ปรากฏใน `//go:embed`
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- งานที่มี `*` เป็นเทสต์และ optional ทั้งหมด ข้ามได้เพื่อทำ MVP เร็วขึ้น แต่ checkpoint ต้องรันเทสต์เดิมทุกครั้ง และห้ามทำให้เทสต์เดิมพัง
- **ส่วนที่ต่างจาก `design.md` โดยตั้งใจ (ตามกฎ steering ของโปรเจกต์):** ไม่ใช้ `fast-check` และไม่มี `web/test/package.json` เทสต์ JS รันด้วย `node --test` และลูปสุ่มที่เขียนเอง (Node บนเครื่องนี้คือ v26.9.0) ฟังก์ชันล้วนที่ design ให้อยู่ใน `app.js` ถูกแยกไปไว้ใน `web/logic.js` เพื่อให้ Node โหลดได้โดยไม่ต้องมี DOM (ต้องอยู่ในบรรทัด `//go:embed`) ส่วน `web/test/` อยู่นอก `//go:embed`
- ค่าอื่นๆ ที่เป็นการตัดสินใจเปิดใน design คงตามค่าเริ่มต้น: Resume_Key เก็บเป็นแฮช (Property 7 เทียบ `ResumeKeyHash`), `Promote/Demote` rollback ตอน `Save` ล้มเหลว, ชื่อไฟล์เกิน 200 rune → 400, query คืนค่าที่ persist แล้ว, เนื้อหากำพร้าไม่ถูกลบอัตโนมัติ
- `pgregory.net/rapid` ปักที่ `v1.3.0` (ตรวจด้วย `go list -m -versions` เมื่อเขียนแผน ยังไม่ได้ `go get`) ถ้าดึงไม่ได้ให้ใช้ `testing/quick` กับตารางเคส seed คงที่ และแก้ Notes นี้
- **สิ่งที่แผนนี้ทำแทนผู้ใช้ไม่ได้ (ไม่ใช่ task เขียนโค้ด):** (1) รันหน้าวัดในงาน 1.3 บน iPhone/Android จริงแล้วกรอกผลใน `docs/project-scope.md` ถ้า SHA-256 JS ช้ากว่า Wi-Fi มาก ให้ตั้งค่าเริ่มต้น "ตรวจ SHA-256" เป็นปิดสำหรับไฟล์ใหญ่ก่อนทำงาน 19.1 (WebAssembly ใช้ไม่ได้เพราะ CSP ต้องไม่ผ่อน) (2) เปิด Firewall ของ macOS เองแล้วรัน `capture.sh` ซ้ำเพื่อเก็บ fixture กรณีเปิด/บล็อก จนกว่าจะได้ parser คืน `unknown` เมื่อเจอข้อความที่ไม่รู้จัก (3) ทดสอบบนอุปกรณ์จริง: Wi-Fi หลุดแล้วกลับ, iOS Safari พับหน้าจอระหว่างส่ง, ปิดแท็บแล้วเปิดต่อ, ไฟล์ > 1 GB ดู RSS ของเซิร์ฟเวอร์ไม่โตตามขนาดไฟล์, `kill -9` เซิร์ฟเวอร์แล้วดู `pmset -g assertions` ว่า `caffeinate` ปล่อยภายใน 5 วินาที, Web Worker/Service Worker/Notification บนเบราว์เซอร์จริง
- `web/test/bench/main.go` เสิร์ฟไฟล์ static โดยไม่มี auth ใช้ชั่วคราวระหว่างวัดเท่านั้น
- `go run` สร้างไบนารีชั่วคราว Firewall ของ macOS อาจถามอนุญาตใหม่ทุกครั้ง ผล check `firewall` ตอนพัฒนาจึงอาจต่างจากไบนารีที่ติดตั้งจริง
- Event `transfer` ของ Hub ยังส่งให้ทุก connection ในห้องรวมบทบาท `uploader` (ปัญหาเดิมก่อน Phase C ยังไม่แก้ใน spec นี้ แนะนำแก้ใน Phase D)
- ถ้าเวลาไม่พอ งานในข้อ 20 (PWA และการแจ้งเตือน) เป็นส่วนที่เลื่อนไป Phase D ได้กระทบน้อยที่สุดตามที่ requirements ระบุ
- Wave ในกราฟด้านล่างเรียงให้ไฟล์เดียวกันไม่ถูกแก้พร้อมกัน (`main.go`: 2.5, 13.4, 15.4, 16.9; `server.go`: 13.2, 13.3, 15.4, 16.9, 20.3; `app.js`: 19.1-19.6, 20.4, 20.5; `web/embed.go`: 1.2, 18.1, 20.2) และทุกงานหลัง checkpoint ถูกวางไว้หลังงานก่อน checkpoint นั้น

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "1.4", "2.1", "3.1", "3.5"] },
    { "id": 1, "tasks": ["1.2", "1.3", "1.5", "1.6", "2.2", "2.3", "3.2", "3.4"] },
    { "id": 2, "tasks": ["2.4", "2.5", "3.3", "5.1", "5.3", "5.5", "5.6", "6.1"] },
    { "id": 3, "tasks": ["4.1", "5.2", "5.4", "6.2"] },
    { "id": 4, "tasks": ["4.2", "4.3"] },
    { "id": 5, "tasks": ["8.1"] },
    { "id": 6, "tasks": ["8.2"] },
    { "id": 7, "tasks": ["8.3"] },
    { "id": 8, "tasks": ["8.4", "9.1", "9.2"] },
    { "id": 9, "tasks": ["8.5", "8.6", "8.7", "9.3", "9.4", "9.5", "9.6", "9.7", "9.8", "9.9", "10.1", "10.2"] },
    { "id": 10, "tasks": ["10.3", "10.4", "10.5", "10.6", "10.7", "10.8", "11.1"] },
    { "id": 11, "tasks": ["11.2", "11.3", "11.4", "11.5"] },
    { "id": 12, "tasks": ["13.1"] },
    { "id": 13, "tasks": ["13.2"] },
    { "id": 14, "tasks": ["13.3"] },
    { "id": 15, "tasks": ["13.4", "13.5", "13.6", "13.7"] },
    { "id": 16, "tasks": ["15.1", "15.3", "16.1", "16.2", "16.3"] },
    { "id": 17, "tasks": ["15.2", "15.4", "15.5", "16.4", "16.6", "16.7"] },
    { "id": 18, "tasks": ["16.5", "16.8", "16.9", "16.11"] },
    { "id": 19, "tasks": ["16.10"] },
    { "id": 20, "tasks": ["18.1", "20.1"] },
    { "id": 21, "tasks": ["18.2", "18.3", "18.4", "18.5", "18.6", "19.1", "20.2", "20.6"] },
    { "id": 22, "tasks": ["19.2", "20.3"] },
    { "id": 23, "tasks": ["19.3", "20.8"] },
    { "id": 24, "tasks": ["19.4"] },
    { "id": 25, "tasks": ["19.5"] },
    { "id": 26, "tasks": ["19.6"] },
    { "id": 27, "tasks": ["20.4"] },
    { "id": 28, "tasks": ["20.5", "20.7"] },
    { "id": 29, "tasks": ["22.1", "22.2", "22.3"] }
  ]
}
```
