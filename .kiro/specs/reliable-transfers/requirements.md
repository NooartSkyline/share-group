# Requirements Document

## Introduction

เอกสารนี้กำหนดความต้องการของ Phase C "ความเสถียร" (ชื่อ spec: `reliable-transfers`) ของ LAN Share ตาม `docs/project-scope.md` หัวข้อ 4 โดยมี 6 ส่วนหลัก

1. Resumable upload: ต่อการอัปโหลดจากไบต์ที่เซิร์ฟเวอร์ยืนยันแล้วเมื่อ Wi-Fi หลุด
2. ตรวจความถูกต้องของไฟล์ด้วย SHA-256 พร้อมป้ายสถานะต่อไฟล์
3. กัน Mac หลับระหว่างมีการถ่ายโอน
4. หน้าตรวจปัญหาการเชื่อมต่อ (เฉพาะ Owner)
5. PWA และการแจ้งเตือนไฟล์ใหม่
6. ตรวจพื้นที่ดิสก์ก่อนรับไฟล์

### ข้อเท็จจริงจากโค้ดปัจจุบัน (Phase A และ B) ที่มีผลต่อความต้องการ

- `POST /api/files` สตรีม request body ลงไฟล์ชั่วคราว `.upload-*` แล้ว rename เมื่อครบ ถ้าเซิร์ฟเวอร์ถูกฆ่ากลางทาง ไฟล์ชั่วคราวจะค้างอยู่ และไม่มีกลไกลบ
- ถ้าการเชื่อมต่อขาด ข้อมูลที่ส่งไปแล้วถูกทิ้งทั้งหมด และเบราว์เซอร์ต้องส่งใหม่ตั้งแต่ต้น
- เซิร์ฟเวอร์ไม่ตั้ง Read/Write timeout เพื่อไม่ตัดการส่งไฟล์ใหญ่ จึงยังไม่มีการตัดการเชื่อมต่อที่ค้างนิ่ง
- Token และ Device เก็บในหน่วยความจำ เมื่อรีสตาร์ตเซิร์ฟเวอร์ ทุกอุปกรณ์ต้องกรอก PIN ใหม่และได้ Device ID ใหม่ การต่ออัปโหลดข้ามการรีสตาร์ตจึงผูกกับ Device ID ไม่ได้ ต้องใช้ Resume_Key แทน
- Metadata ของไฟล์เก็บใน `state.json` (jsonstore) ยังไม่มีฟิลด์ checksum
- Hub ส่งทุก event ของห้องให้ทุกการเชื่อมต่อในห้อง รวมถึงบทบาท Uploader ที่ไม่มีสิทธิ์ดาวน์โหลด
- เบราว์เซอร์ที่เปิดผ่าน `http://192.168.x.x:8080` ไม่ใช่ Secure Context ดังนั้น `crypto.subtle`, Service Worker และ Notification API ใช้ไม่ได้ในเครื่องอื่น ส่วน `http://localhost:8080` บน Mac ของเจ้าของเป็น Secure Context
- Content-Security-Policy ปัจจุบันเป็น `default-src 'self'` จึงรองรับ Web Worker และ Service Worker จากต้นทางเดียวกันโดยไม่ต้องผ่อนนโยบาย

### ข้อจำกัดที่ต้องยอมรับ

- บน HTTP ธรรมดา Service Worker, การติดตั้ง PWA แบบเต็มรูปแบบ และการแจ้งเตือนของระบบจะไม่ทำงานในอุปกรณ์อื่น ผลที่ได้บน HTTP คือ manifest, ไอคอน, การเพิ่มลงหน้าจอหลักแบบ shortcut ตามที่เบราว์เซอร์รองรับ และการแจ้งเตือนในหน้าเว็บ ส่วนที่เหลือรอ HTTPS ใน Phase D (ยังไม่ยืนยันว่า self-signed certificate จะทำให้ Service Worker ทำงานได้ในทุกเบราว์เซอร์ ต้องทดสอบใน Phase D)
- การแจ้งเตือนทำงานเฉพาะขณะหน้าเว็บเปิดและเชื่อมต่อ WebSocket อยู่ Web Push ต้องพึ่งบริการ push บนอินเทอร์เน็ต จึงอยู่นอกขอบเขต
- `caffeinate` ป้องกันการหลับเมื่อไม่มีการใช้งาน แต่ไม่รับประกันว่า Mac จะไม่หลับเมื่อปิดฝาเครื่องขณะใช้แบตเตอรี่ การต่ออัปโหลดอัตโนมัติจึงเป็นแนวป้องกันชั้นที่สอง
- ระบบไม่สามารถตรวจพบ AP Isolation ได้แน่ชัดจากฝั่งเซิร์ฟเวอร์ การตรวจทำได้เพียงใช้หลักฐานทางอ้อม (ยังไม่มีอุปกรณ์อื่นเชื่อมต่อเข้ามา) แล้วแสดงคำแนะนำ
- ความเร็วของ SHA-256 แบบ JavaScript บนมือถือจริงยังไม่ได้วัด ต้องวัดในขั้น design และ tasks

### นอกขอบเขตของ spec นี้

- HTTPS และการอนุมัติอุปกรณ์ (Phase D)
- ตรวจ SHA-256 ของไฟล์ที่เก็บอยู่ซ้ำเป็นระยะ (bit rot) และ checksum ภายใน zip
- โควตาไฟล์ต่อห้องและไฟล์หมดอายุ (Phase D)
- Web Push
- การอัปโหลดแบบหลายการเชื่อมต่อขนานต่อไฟล์เดียว
- การรองรับโฮสต์ที่ไม่ใช่ macOS

### ลำดับที่แนะนำสำหรับขั้นวางงาน

Phase C ประเมินไว้ 3.5-4.5 วัน แต่ขอบเขตที่เขียนไว้ในเอกสารนี้ครอบคลุมงานจำนวนมาก ลำดับที่แนะนำคือ Requirement 7, 1-6 และ 11-12, 8-10, 13, 14-15, 16-17 หากเวลาไม่พอ Requirement 16 และ 17 เป็นส่วนที่ตัดหรือเลื่อนไป Phase D ได้โดยกระทบน้อยที่สุด

## Glossary

- **LanShare_Server**: โปรแกรมเซิร์ฟเวอร์ Go ที่รันบน Mac ของเจ้าของ ประกอบด้วยทุกชั้นของ Clean Architecture
- **Transfer_Service**: use case ฝั่งเซิร์ฟเวอร์ที่จัดการอัปโหลด ดาวน์โหลด และ Upload_Session
- **Web_Server**: ชั้น `adapter/httpapi` ที่เปิด HTTP API และเสิร์ฟหน้าเว็บ
- **Web_Client**: หน้าเว็บ JavaScript ใน `web/` ที่ทำงานบนเบราว์เซอร์ของทุกอุปกรณ์
- **Storage_Guard**: ส่วนที่ตรวจพื้นที่ว่างของดิสก์และจองพื้นที่ให้ Upload_Session
- **Power_Guard**: ส่วนที่ป้องกันไม่ให้ Mac หลับขณะมี Active_Transfer
- **Diagnostics_Service**: use case ที่รวบรวมผลตรวจสภาพแวดล้อมของเครือข่ายและเครื่อง
- **Upload_Janitor**: งานเบื้องหลังที่ลบ Upload_Session ที่หมดอายุและไฟล์ชั่วคราวที่ตกค้าง
- **Config_Loader**: ส่วนที่อ่านและตรวจค่า command line flag
- **Room**: พื้นที่แยกของไฟล์ อุปกรณ์ และ PIN
- **Device**: อุปกรณ์ที่เข้าห้องแล้วและมี Role
- **Owner**: Device บทบาท `owner` (`Role.CanManage()` เป็นจริง)
- **Upload_Session**: สถานะการอัปโหลดที่ต่อได้ ประกอบด้วย session ID, Room, ชื่อไฟล์ที่ผ่านการ sanitize, Dir, Declared_Size, Conflict_Policy, Confirmed_Offset, สถานะ SHA-256 ระหว่างทาง, เวลาใช้งานล่าสุด และ Resume_Key
- **Resume_Key**: ความลับสุ่ม 128 บิตที่ออกให้ผู้สร้าง Upload_Session ใช้พิสูจน์สิทธิ์ต่ออัปโหลด รวมถึงหลังรีสตาร์ตเซิร์ฟเวอร์
- **Declared_Size**: ขนาดไฟล์เป็นไบต์ที่ผู้ส่งแจ้งตอนสร้าง Upload_Session
- **Confirmed_Offset**: จำนวนไบต์แรกของไฟล์ที่ LanShare_Server เขียนลง Partial_File แล้ว และตรงกับสถานะ SHA-256 ที่บันทึกไว้
- **Partial_File**: ไฟล์ชั่วคราวที่เก็บไบต์ของ Upload_Session ที่ยังไม่ครบ ไม่ถือเป็นไฟล์ของห้อง
- **Conflict_Policy**: นโยบายชื่อซ้ำ `rename` (ค่าเริ่มต้น) หรือ `overwrite` ตามที่ Phase B กำหนด
- **Checksum**: ค่า SHA-256 แสดงเป็นตัวอักษร hex พิมพ์เล็ก 64 ตัว
- **Integrity_Status**: สถานะความถูกต้องของไฟล์ มีค่า `verified` (Checksum จากต้นทางตรงกับที่เซิร์ฟเวอร์คำนวณ), `unverified` (มีเฉพาะ Checksum ที่เซิร์ฟเวอร์คำนวณ) หรือ `unknown` (ไฟล์เก่าจาก Phase A/B ที่ยังไม่มี Checksum)
- **Free_Space_Reserve**: พื้นที่ว่างขั้นต่ำที่ต้องเหลือบนดิสก์หลังรับไฟล์ ค่าเริ่มต้น 1 GiB
- **Data_Dir**: โฟลเดอร์ข้อมูลของ LanShare_Server (flag `-data`)
- **Active_Transfer**: การอัปโหลด ดาวน์โหลด หรือดาวน์โหลด zip ที่ request ยังทำงานอยู่ หรือ Upload_Session ที่ยังไม่เสร็จและมี request สิ้นสุดลงภายใน 60 วินาทีที่ผ่านมา
- **Idle_Grace**: ช่วงเวลา 30 วินาทีหลังไม่มี Active_Transfer ก่อนที่ Power_Guard จะปล่อยการกันหลับ
- **Stall_Timeout**: เวลาสูงสุดที่ยอมให้ request อัปโหลดไม่ได้รับไบต์ใหม่ ค่าเริ่มต้น 30 วินาที
- **Secure_Context**: บริบทที่เบราว์เซอร์ถือว่าปลอดภัย (`window.isSecureContext` เป็นจริง) เช่น HTTPS หรือ `localhost`
- **Check_Result**: ผลตรวจหนึ่งรายการของ Diagnostics_Service มี `id`, `status` (`ok`, `warn`, `fail`, `unknown`), หัวข้อ, รายละเอียด และคำแนะนำ
- **App_Shell**: ไฟล์คงที่ของหน้าเว็บ ได้แก่ `index.html`, `style.css`, `app.js`, worker script, manifest และไอคอน
- **Dir**: โฟลเดอร์ย่อยของไฟล์ที่ผ่านการ sanitize แล้ว คั่นด้วย `/` ไม่เกิน 12 ระดับ ค่าว่างหมายถึงไม่มีโฟลเดอร์
- **Member**: Device บทบาท `member` ในเรื่องราวผู้ใช้ใช้เรียกผู้ใช้ทั่วไปที่ส่งและรับไฟล์ได้ (Owner ใช้ความสามารถเหล่านี้ได้เช่นกัน)
- **Active_Interface**: การ์ดเครือข่ายที่ IPv4 ของมันถูกแสดงใน URL และ QR สำหรับเชิญอุปกรณ์ (เรียกอีกอย่างว่า active network interface)
- **Completion_Record**: ข้อมูลที่เก็บไว้ 10 นาทีหลังอัปโหลดสำเร็จ เพื่อตอบคำขอ complete ซ้ำที่มี Resume_Key ถูกต้อง
- **Power_Guard_Status**: สถานะของ Power_Guard มี 4 ค่า คือ `active` (กำลังกันหลับ), `idle` (ไม่มีงานถ่ายโอน), `disabled` (ปิดด้วย `-keep-awake=false`) และ `unavailable` (เริ่ม `caffeinate` ไม่ได้)

## Requirements

### Requirement 1: สร้าง Upload_Session สำหรับการอัปโหลดที่ต่อได้

**User Story:** As a Device ที่มีสิทธิ์อัปโหลด, I want เริ่มการอัปโหลดแบบที่ต่อได้, so that ไฟล์ขนาดหลาย GB ไม่ต้องเริ่มใหม่เมื่อการเชื่อมต่อหลุด

#### Acceptance Criteria

1. WHEN a Device whose Role satisfies `CanUpload` submits a name of 1 to 200 characters after trimming, a Dir, an integer Declared_Size from 0 to 9007199254740991 bytes inclusive, and a Conflict_Policy of `rename` or `overwrite`, THE Transfer_Service SHALL create an Upload_Session bound to the Room of that Device with Confirmed_Offset 0 and return HTTP 201 with the session ID, the Resume_Key, the expiry time and the Confirmed_Offset
2. IF the Role of the requesting Device does not satisfy `CanUpload`, THEN THE Transfer_Service SHALL reject the request with HTTP 403 and error code `forbidden`, create neither an Upload_Session nor a Partial_File, and leave the Upload_Session count of the Room unchanged
3. IF the name is empty or contains only whitespace, the Declared_Size is missing, negative, not an integer, or greater than 9007199254740991, or the Conflict_Policy is present but is not `rename` or `overwrite`, THEN THE Transfer_Service SHALL reject the request with HTTP 400 and error code `bad_request` and create neither an Upload_Session nor a Partial_File
4. WHEN an Upload_Session is created, THE Transfer_Service SHALL apply the same file name and Dir sanitization as `POST /api/files` (`SanitizeFileName` and `SanitizeDir`) so that the stored name is at most 200 characters and never contains a directory part, a control character or a `..` segment, and the stored Dir has at most 12 segments
5. IF the name is non-empty but the sanitized name is empty, THEN THE Transfer_Service SHALL store the fallback name `file` (the same result `SanitizeFileName` gives for `POST /api/files`) and still create the Upload_Session
6. IF Conflict_Policy is `overwrite`, an existing file with the same Dir and name exists in the Room, and the requesting Device is neither the Owner nor the uploader of that file, THEN THE Transfer_Service SHALL reject the request with HTTP 403 and error code `forbidden` before any byte is received, and create neither an Upload_Session nor a Partial_File
7. IF the requesting Device already has 16 unfinished Upload_Sessions or the Room already has 64, THEN THE Transfer_Service SHALL reject the request with HTTP 429 and error code `too_many_uploads`, and create neither an Upload_Session nor a Partial_File
8. WHEN the Declared_Size is 0, THE Transfer_Service SHALL allow the Upload_Session to complete without any append request
9. WHEN the Transfer_Service creates an Upload_Session, THE Transfer_Service SHALL generate a Resume_Key of 128 random bits, and return it only in the creation response for that Upload_Session

### Requirement 2: ต่อไบต์และรายงาน Confirmed_Offset

**User Story:** As a Web_Client, I want ส่งไบต์ต่อจากจุดที่เซิร์ฟเวอร์ยืนยัน, so that ไม่ต้องส่งซ้ำข้อมูลที่ถึงแล้ว

#### Acceptance Criteria

1. WHEN an append request for an unfinished Upload_Session carries a valid Resume_Key and an offset equal to the Confirmed_Offset, THE Transfer_Service SHALL write the received bytes to the Partial_File after the existing bytes, increase the Confirmed_Offset by the number of bytes written, and return the new Confirmed_Offset
2. IF an append request carries an offset that is a valid non-negative integer but differs from the Confirmed_Offset, THEN THE Transfer_Service SHALL reject the request with HTTP 409 and error code `offset_mismatch`, include the current Confirmed_Offset in the response, and write no byte
3. WHEN an append request ends before its body is complete because of a client disconnect, a Stall_Timeout or a server shutdown, THE Transfer_Service SHALL keep every byte written to the Partial_File before the end and set the Confirmed_Offset to the exact length of the Partial_File in bytes, with the SHA-256 state matching those bytes
4. WHEN a client with a valid Resume_Key queries an Upload_Session, THE Transfer_Service SHALL return the Confirmed_Offset, the Declared_Size, the expiry time and the Checksum of the first Confirmed_Offset bytes, where a Confirmed_Offset of 0 yields the Checksum of empty input
5. IF an append request would make the Partial_File longer than the Declared_Size, THEN THE Transfer_Service SHALL reject the request with HTTP 413 and error code `size_exceeded`, discard any byte written by that request, and leave the Partial_File length and the Confirmed_Offset equal to their values before the request
6. WHEN an append request arrives for an Upload_Session that still has another append request in progress, THE Transfer_Service SHALL terminate the earlier request, wait up to 5 seconds for its bytes to be recorded in the Confirmed_Offset, and then process the new request against the updated Confirmed_Offset; IF the earlier request has not finished recording its bytes after 5 seconds, THEN THE Transfer_Service SHALL reject the new request with HTTP 409 and error code `upload_busy`, write no byte and leave the Confirmed_Offset unchanged
7. IF an append request receives no new byte for the Stall_Timeout (30 seconds by default, measured from the last received byte or from the start of the request if no byte has arrived), THEN THE Transfer_Service SHALL end that request with HTTP 408 and error code `stall_timeout`, keep the bytes already written, and set the Confirmed_Offset to the length of the Partial_File
8. FOR ALL byte sequences of length 0 to Declared_Size and ALL interruption points from 0 to the sequence length inclusive, appending a prefix, being interrupted, querying the Confirmed_Offset and appending the remaining bytes from that offset SHALL produce a Partial_File byte-identical to the original sequence, with a Checksum equal to the SHA-256 of the original sequence (property)
9. FOR ALL Upload_Sessions at any time, including after a server restart, THE Transfer_Service SHALL keep the Confirmed_Offset between 0 and the Declared_Size inclusive and equal to the length of the Partial_File in bytes (invariant)
10. WHILE an append request is writing, THE Transfer_Service SHALL stream the body to the Partial_File without holding more than 1 MiB of file content in memory per request, regardless of the body size
11. IF an append or query request names an Upload_Session that does not exist, has expired, has already completed or belongs to another Room than the Room of the authenticated Device, or carries a missing, empty or non-matching Resume_Key, THEN THE Transfer_Service SHALL reject the request with HTTP 404 and error code `not_found`, with the same status, error code and response body in every one of these cases (see Requirement 5), write no byte and leave every Upload_Session unchanged
12. IF an append request carries an offset that is missing, negative, non-numeric or greater than the Declared_Size, THEN THE Transfer_Service SHALL reject the request with HTTP 400 and error code `bad_request`, write no byte, and leave the Confirmed_Offset unchanged

### Requirement 3: ปิดงานอัปโหลดและเผยแพร่ไฟล์แบบอะตอมมิก

**User Story:** As a Device ที่อัปโหลด, I want ไฟล์ปรากฏในห้องเมื่อครบและถูกต้องเท่านั้น, so that คนอื่นไม่เห็นหรือดาวน์โหลดไฟล์ที่ยังส่งไม่เสร็จ

#### Acceptance Criteria

1. WHEN a complete request arrives for an Upload_Session whose Confirmed_Offset equals the Declared_Size, THE Transfer_Service SHALL resolve the file name against the current files of the Room with the same Dir using the Conflict_Policy (`rename` yields a name not used by any existing file in that Dir, `overwrite` replaces the existing file of the same name, and both keep the sanitized name when no file of that name exists), move the Partial_File into final storage, save the file metadata and respond with HTTP 201 and the stored file
2. WHEN the file metadata of a completed Upload_Session has been saved, THE Transfer_Service SHALL publish exactly one `file.added` event for that file, and SHALL publish no `file.added` event before the metadata is saved
3. IF a complete request arrives while the Confirmed_Offset is less than the Declared_Size, THEN THE Transfer_Service SHALL reject the request with HTTP 409, include the Confirmed_Offset in the response, publish no `file.added` event, and leave the Upload_Session and its Partial_File unchanged
4. WHILE an Upload_Session is not completed, THE Transfer_Service SHALL exclude its Partial_File from file listing, download, zip download, thumbnail generation and `file.added` events, and SHALL answer any download or thumbnail request that targets it with HTTP 404 and error code `not_found` (invariant)
5. IF saving the file metadata fails during completion, THEN THE Transfer_Service SHALL delete the final content, respond with HTTP 500, publish no `file.added` event, and leave no content without metadata in the Room storage; WHERE Conflict_Policy is `overwrite`, the previously stored file with the same Dir and name SHALL remain unchanged
6. WHEN a complete request carrying the correct Resume_Key is repeated for an Upload_Session completed within the last 10 minutes, including requests that arrive concurrently with the first complete request, THE Transfer_Service SHALL respond with HTTP 200 and the same stored file (same file ID, name and size), store no second file, and publish no second `file.added` event; once more than 10 minutes have passed since completion, THE Transfer_Service SHALL answer the repeated request with HTTP 404 and error code `not_found`
7. WHEN completion succeeds, THE Transfer_Service SHALL delete the Upload_Session, its persisted state and the Partial_File before the success response is sent, so that the Upload_Session cannot be queried or appended to afterwards, and the only record retained is the Completion_Record needed by criterion 6 for 10 minutes
8. IF Conflict_Policy is `overwrite` at completion, a file with the same Dir and name exists, and the requesting Device is neither the Owner nor the uploader of that file, THEN THE Transfer_Service SHALL check this again at completion time, reject the request with HTTP 403 and error code `forbidden`, publish no `file.added` event, and leave the existing file and the Upload_Session unchanged
9. FOR ALL Upload_Sessions that completed successfully, including those resumed any number of times, THE stored file content SHALL be byte-identical to the bytes of the original file, with a length equal to the Declared_Size and a SHA-256 equal to that of the original file (property)

### Requirement 4: ยกเลิก หมดอายุ และล้างข้อมูลที่ตกค้าง

**User Story:** As an Owner, I want ให้ไฟล์ที่ส่งไม่จบถูกล้างอัตโนมัติ, so that ดิสก์ของ Mac ไม่เต็มด้วยไฟล์ชั่วคราว

#### Acceptance Criteria

1. WHEN the holder of the Resume_Key of an Upload_Session, or the Owner of the Room of that Upload_Session, cancels it, THE Transfer_Service SHALL terminate any append request in progress for it, delete the Partial_File and the persisted state within 5 seconds, publish exactly one transfer event with state `stopped`, and respond with HTTP 204
2. WHEN an Upload_Session has had no append request that wrote at least one byte for 24 hours, measured from its creation if no such request ever occurred, THE Upload_Janitor SHALL delete its Partial_File and persisted state no later than 5 minutes after the 24 hours elapsed, running at least once every 5 minutes, and SHALL NOT delete an Upload_Session that has an append request in progress
3. WHEN an append request succeeds in writing at least one byte, THE Transfer_Service SHALL restart the 24 hour expiry period of that Upload_Session, and an append request that writes zero bytes or fails before writing any byte SHALL NOT restart it
4. WHEN the Confirmed_Offset of an Upload_Session changes, THE Transfer_Service SHALL persist the Upload_Session to disk before it acknowledges the new Confirmed_Offset to the client, so that a failure while writing leaves the previously persisted complete record intact and unfinished sessions survive a restart of the LanShare_Server
5. WHEN the LanShare_Server starts, THE Upload_Janitor SHALL, before the Web_Server accepts its first request, delete every Partial_File that has no persisted Upload_Session and every legacy `.upload-*` temporary file in the Data_Dir whose last modification is more than 1 hour old
6. WHEN the LanShare_Server starts and a Partial_File is longer than the Confirmed_Offset persisted for its Upload_Session, THE Upload_Janitor SHALL truncate the Partial_File to exactly that Confirmed_Offset and keep the Upload_Session
7. WHEN the LanShare_Server starts and the Partial_File of a persisted Upload_Session is shorter than its persisted Confirmed_Offset or missing, THE Upload_Janitor SHALL delete that persisted Upload_Session and any remaining Partial_File, and later requests naming that Upload_Session SHALL receive `not_found`
8. FOR ALL valid Upload_Session records, saving the record to disk then loading it SHALL produce a record in which every field, including the Resume_Key and the SHA-256 state, equals the original (round-trip property)
9. FOR ALL points in time after the Upload_Janitor finished a run, except while a create, cancel or complete operation is in progress, the number of Partial_Files on disk SHALL equal the number of persisted unfinished Upload_Sessions, and each Partial_File SHALL belong to exactly one of those Upload_Sessions (invariant)
10. IF a cancel request names an Upload_Session that does not exist, belongs to another Room, or is cancelled by a Device that holds neither its Resume_Key nor the Owner role of its Room, THEN THE Transfer_Service SHALL respond HTTP 404 with error code `not_found`, and SHALL delete no Partial_File or persisted state and publish no transfer event
11. IF a persisted Upload_Session record cannot be parsed when the LanShare_Server starts, THEN THE Upload_Janitor SHALL delete that record and its Partial_File, write a warning to the log, and continue starting without failing
12. IF persisting an Upload_Session fails while handling an append request, THEN THE Transfer_Service SHALL respond HTTP 500 with error code `persist_failed`, report to the client the last persisted Confirmed_Offset, and keep the Upload_Session

### Requirement 5: สิทธิ์ต่ออัปโหลดและการแยกห้อง

**User Story:** As an Owner, I want ให้การต่ออัปโหลดไม่ข้ามห้องและไม่ข้ามสิทธิ์, so that ทีมหนึ่งไม่แตะไฟล์ชั่วคราวของอีกทีม

#### Acceptance Criteria

1. WHEN an append, query, complete or cancel request names an Upload_Session of another Room than the Room of the authenticated Device, including when the Device is the Owner and when the request carries the correct Resume_Key of that session, THE Transfer_Service SHALL respond HTTP 404 with error code `not_found`, with the same status, error code and response body as for a session ID that does not exist, and SHALL change no Upload_Session, Partial_File or file
2. IF an append, query or complete request, or a cancel request from a Device that is not the Owner, names an Upload_Session of the Room of the authenticated Device and carries no Resume_Key, an empty Resume_Key or a Resume_Key that differs from the Resume_Key of that session, THEN THE Transfer_Service SHALL respond HTTP 404 with error code `not_found`, with the same status, error code and response body as for a session ID that does not exist, and SHALL change no Upload_Session, Partial_File or file
3. THE Web_Server SHALL read the Resume_Key only from a request header, and SHALL treat a Resume_Key supplied in the URL path, the query string or the request body as if the request carried no Resume_Key
4. WHEN a Device whose Role satisfies `CanUpload` has joined the Room again after the LanShare_Server restarted and presents the correct Resume_Key of an Upload_Session of that Room, THE Transfer_Service SHALL allow that Device to query, append to, complete and cancel that Upload_Session under the same session ID, with the Confirmed_Offset that was persisted before the restart
5. WHEN a complete request succeeds, THE Transfer_Service SHALL record the ID of the Device that sent that complete request as the uploader of the stored file, also when that Device differs from the Device that created the Upload_Session
6. IF the Role of the authenticated Device does not satisfy `CanUpload`, THEN THE Transfer_Service SHALL reject any create, append, query, complete or cancel request with HTTP 403 and error code `forbidden`, even when the request carries a correct Resume_Key, before it looks up any Upload_Session, and SHALL change no Upload_Session, Partial_File or file
7. THE Transfer_Service SHALL generate the session ID and the Resume_Key independently of each other from a cryptographically secure random source, with at least 128 bits each
8. FOR ALL pairs of Rooms A and B where A differs from B, no operation by any Device of Room A, including the Owner of Room A, with any session ID or any Resume_Key, including the correct Resume_Key of an Upload_Session of Room B, SHALL read, change or delete an Upload_Session, Partial_File or file of Room B, and every such request that names an Upload_Session SHALL receive the response defined in criterion 1 (property)
9. IF an append, query, complete or cancel request carries no valid device token, THEN THE Transfer_Service SHALL respond HTTP 401 with error code `unauthorized` and SHALL change no Upload_Session, Partial_File or file
10. THE Web_Server SHALL NOT write the Resume_Key to the access log or the application log, in raw form, in its transmitted encoding, or as any prefix or substring of 8 or more characters, including for requests that fail
11. THE Transfer_Service SHALL return the Resume_Key only in the response to the request that creates the Upload_Session, SHALL NOT include it in the response to any other request or in any event published to the Room, and SHALL NOT include in any Upload_Session response the metadata of a file other than the file of that Upload_Session, so that a Device with the `uploader` Role receives no file listing data

### Requirement 6: ความเข้ากันได้กับ endpoint เดิม ชื่อซ้ำ และโฟลเดอร์

**User Story:** As a Member, I want ให้ไฟล์ที่ต่ออัปโหลดได้ทำงานกับนโยบายชื่อซ้ำและโฟลเดอร์เหมือนเดิม, so that พฤติกรรมของ Phase B ไม่เปลี่ยน

#### Acceptance Criteria

1. WHEN an authenticated Device whose Role satisfies `CanUpload` calls `POST /api/files` with the query parameters `name` (required), `dir` (optional) and `conflict` (optional, `rename` when omitted), THE Web_Server SHALL stream the raw request body into the Room of that Device without buffering the whole body in memory and respond HTTP 201 with the stored file using the same JSON field names, types and value formats as in Phase B, except for the additional responses defined in Requirements 7 and 9
2. WHEN an Upload_Session with Conflict_Policy `rename` completes, THE Transfer_Service SHALL choose the final name at completion time, not at creation time, so that the final name differs, compared case-insensitively, from the name of every file already in the same Dir of the Room, where Dir is also compared case-insensitively; IF the sanitized name is free THEN it SHALL be kept unchanged, and otherwise THE Transfer_Service SHALL use the first free name of the form `<base> (N)<ext>` with N starting at 2 and increasing by 1, where `<ext>` is the part of the name from its last `.` and is empty for a name that consists only of an extension such as `.env`
3. WHEN an Upload_Session with Conflict_Policy `overwrite` completes, THE Transfer_Service SHALL save the content and metadata of the new file under the sanitized name first, and only after the save succeeded SHALL it delete the old file's content, thumbnail and metadata, publish `file.deleted` for the old file and then publish `file.added` for the new file; IF the save fails, THEN the old file SHALL remain unchanged; IF no file with the same Dir and name (compared case-insensitively) exists at completion time, THEN THE Transfer_Service SHALL save the new file and delete nothing
4. WHEN an Upload_Session is created with a Dir, THE Transfer_Service SHALL sanitize the Dir with the same rules as `POST /api/files` (backslash converted to `/`, segments that are empty, `.` or `..` dropped, control characters removed, leading and trailing spaces and dots of each segment trimmed, each segment limited to 200 characters, at most 12 segments kept, empty string when nothing remains), keep the sanitized Dir unchanged in the persisted Upload_Session across resume and server restart, store it in the metadata of the stored file, and SHALL place that file in a zip download at the path `<Dir>/<name>`
5. FOR ALL sets of Upload_Sessions and `POST /api/files` uploads with Conflict_Policy `rename` into the same Room and Dir (compared case-insensitively) that complete in any order, including concurrently, and FOR ALL sets of files already present in that Dir, the final names of the stored files SHALL be pairwise distinct under case-insensitive comparison (property)
6. WHEN a file stored through `POST /api/files` is listed, THE Web_Server SHALL include every field that Phase B included, with unchanged names, types and values, plus the `sha256` and `integrity` fields defined in Requirement 8
7. IF a `POST /api/files` request is not from an authenticated Device, THEN THE Web_Server SHALL respond HTTP 401 with error code `unauthorized`; IF the Role of the Device does not satisfy `CanUpload`, or Conflict_Policy is `overwrite` and the Device is neither the Owner nor the uploader of the existing file with the same Dir and name, THEN THE Web_Server SHALL respond HTTP 403 with error code `forbidden`; in every such case THE Transfer_Service SHALL store no file and leave every existing file unchanged
8. IF the `name` query parameter of `POST /api/files` is empty or missing, or `conflict` is present and is neither `rename` nor `overwrite`, THEN THE Web_Server SHALL respond HTTP 400 with error code `bad_request` and store no file
9. WHILE two or more completions (Upload_Sessions or `POST /api/files`) in the same Room are resolving names, THE Transfer_Service SHALL process them one at a time per Room, so that each completion sees every file published by the completions before it

### Requirement 7: ตรวจพื้นที่ดิสก์ก่อนและระหว่างรับไฟล์

**User Story:** As a Member, I want ให้ระบบปฏิเสธไฟล์ที่ดิสก์รับไม่ไหวตั้งแต่ต้น, so that ไม่เสียเวลาส่งไฟล์ใหญ่แล้วล้มเหลวตอนท้าย

#### Acceptance Criteria

1. WHEN an Upload_Session is created, THE Storage_Guard SHALL compute required bytes as the Declared_Size plus the Free_Space_Reserve and available bytes as the free space of the volume of the Data_Dir minus the sum of the remaining bytes (Declared_Size minus Confirmed_Offset) of all other unfinished Upload_Sessions, where an unfinished Upload_Session is one that has not been completed, cancelled or expired, and available bytes below 0 SHALL be treated as 0
2. IF the available bytes are smaller than the required bytes, THEN THE Transfer_Service SHALL reject the request with HTTP 507 and error code `insufficient_storage`, include `requiredBytes` and `availableBytes` as non-negative integers in bytes in the response, and create no Upload_Session and no Partial_File (available bytes equal to the required bytes SHALL be accepted)
3. WHEN `POST /api/files` is called with a Content-Length header, THE Storage_Guard SHALL treat the Content-Length as the Declared_Size, apply the check in criteria 1 and 2 before reading any byte of the request body, and on rejection respond with HTTP 507 and error code `insufficient_storage` and leave no Partial_File
4. WHILE an upload without a known length is being received, IF the free space read after each further 64 MiB (67,108,864 bytes) written is smaller than the Free_Space_Reserve, THEN THE Transfer_Service SHALL stop reading the request body, delete the Partial_File and all other temporary data of that upload, and respond with HTTP 507, error code `insufficient_storage`, `requiredBytes` equal to the Free_Space_Reserve and `availableBytes` equal to the free space read
5. IF a write to the Partial_File fails because the disk is full, THEN THE Transfer_Service SHALL keep the Upload_Session and the Partial_File content up to the last consistent Confirmed_Offset (matching the saved SHA-256 state) and respond with HTTP 507 and error code `insufficient_storage`, with `requiredBytes` equal to the remaining bytes of that Upload_Session plus the Free_Space_Reserve and `availableBytes` equal to the free space read after the failure
6. IF the free space of the volume of the Data_Dir cannot be read, THEN THE Storage_Guard SHALL accept the upload without rejecting it, write one warning entry to the log for that failed read, and cause the Diagnostics_Service to report the disk-space Check_Result with status `unknown`
7. WHEN the Web_Client receives HTTP 507 with error code `insufficient_storage`, THE Web_Client SHALL show the message "พื้นที่ดิสก์ไม่พอ: ต้องการ {requiredBytes} มี {availableBytes}" with both sizes formatted in binary units (B, KiB, MiB or GiB) to one decimal place, mark that transfer as failed, and send no further request for that transfer without a user action
8. FOR ALL sequences of Upload_Session creation requests against a fake disk whose free space F is constant, the sum of the remaining bytes of all accepted unfinished Upload_Sessions plus the Free_Space_Reserve SHALL NOT exceed F (property)
9. WHEN two or more Upload_Session creation requests (including `POST /api/files` requests with a known Content-Length) arrive concurrently, THE Storage_Guard SHALL evaluate the check in criterion 1 and register each accepted Upload_Session one request at a time, so that every accepted request counts the remaining bytes of all previously accepted unfinished Upload_Sessions

### Requirement 8: คำนวณ SHA-256 ฝั่งเซิร์ฟเวอร์

**User Story:** As a Member, I want ให้เซิร์ฟเวอร์คำนวณ SHA-256 ของไฟล์ที่ได้รับ, so that มีค่าอ้างอิงสำหรับตรวจว่าไฟล์ไม่เสียหาย

#### Acceptance Criteria

1. WHILE bytes of an upload are being written, THE Transfer_Service SHALL update a SHA-256 computation over exactly the bytes written, in the order written, without reading the stored file again and without holding more than 1 MiB of file content in memory at any time for this computation
2. WHEN an Upload_Session is persisted, THE Transfer_Service SHALL persist the SHA-256 computation state together with the Confirmed_Offset in one atomic update, so that the persisted state always corresponds to exactly the first Confirmed_Offset bytes of the Partial_File
3. WHEN an Upload_Session resumes after an interruption or a server restart, THE Transfer_Service SHALL continue the SHA-256 computation from the persisted state without reading any byte of the Partial_File that lies before the Confirmed_Offset
4. WHEN an upload completes, THE Transfer_Service SHALL store the Checksum in the file metadata and SHALL include `sha256` (the Checksum, or an empty string when the Integrity_Status is `unknown`) and `integrity` (an Integrity_Status) for that file in every file listing response
5. WHEN an upload through `POST /api/files` completes, THE Transfer_Service SHALL store the Checksum computed by the LanShare_Server while receiving the body, without a second pass over the stored file, and set the Integrity_Status to `unverified`
6. WHEN a file stored before this phase has no Checksum and a Device whose Role satisfies `CanDownload` requests its Checksum, THE Transfer_Service SHALL compute it by streaming the file with at most 1 MiB of file content in memory, store the Checksum in the metadata, keep the Integrity_Status `unknown`, and return the Checksum
7. IF a Device whose Role does not satisfy `CanDownload` requests a Checksum, THEN THE Transfer_Service SHALL reject the request with HTTP 403 and error code `forbidden`, and start no computation
8. IF a Checksum computation for a file stored before this phase is requested while another such computation is running, THEN THE Transfer_Service SHALL queue the request and start it only after the running computation ends, so that at most 1 such computation runs at any time
9. IF a queued Checksum request has not started within 60 seconds, THEN THE Transfer_Service SHALL reject it with HTTP 503 and error code `checksum_busy`, and store no Checksum for that request
10. IF a file is deleted or replaced, or a read error occurs, while a Checksum computation for a file stored before this phase is running, THEN THE Transfer_Service SHALL store no Checksum, leave the metadata of every file unchanged, and respond with HTTP 404 and error code `not_found` when the file no longer exists, or with HTTP 500 and error code `checksum_failed` for a read error
11. THE Transfer_Service SHALL scope every Checksum lookup to the Room of the requesting Device and SHALL respond with HTTP 404 and error code `not_found`, with a response identical to that for a file that does not exist, for files of other Rooms
12. FOR ALL byte sequences and ALL interruption points, the Checksum stored after resume SHALL equal the SHA-256 of the original byte sequence (property)
13. FOR ALL valid file metadata including a Checksum, saving to `state.json` then loading SHALL produce equivalent metadata, and loading a `state.json` written before this phase SHALL succeed, lose no file, and give every file without a Checksum the Integrity_Status `unknown` (round-trip property)

### Requirement 9: ตรวจ Checksum จากต้นทางและปฏิเสธไฟล์ที่ไม่ตรง

**User Story:** As a Member, I want ให้ระบบเทียบ SHA-256 จากอุปกรณ์ต้นทางกับที่เซิร์ฟเวอร์ได้รับ, so that ไฟล์ที่เสียระหว่างทางไม่ถูกนับว่าส่งสำเร็จ

#### Acceptance Criteria

1. WHEN a complete request for an Upload_Session whose Confirmed_Offset equals the Declared_Size carries a Checksum of exactly 64 hexadecimal characters, THE Transfer_Service SHALL compare it, ignoring letter case, with the Checksum computed over the received bytes before moving the Partial_File into final storage, saving metadata or publishing `file.added`
2. WHEN the two Checksums are equal, THE Transfer_Service SHALL complete the upload as defined in Requirement 3, store the computed Checksum in the file metadata and return the stored file with Integrity_Status `verified`, including for a file whose Declared_Size is 0
3. IF the two Checksums differ, THEN THE Transfer_Service SHALL reject the completion with HTTP 422 and error code `checksum_mismatch`, include the supplied Checksum and the computed Checksum in the response, delete the Partial_File and the Upload_Session with its persisted state, and create no file metadata, no stored file content, no thumbnail and no `file.added` event
4. IF the Checksum in a complete request is not exactly 64 hexadecimal characters (`0-9`, `a-f`, `A-F`), including an empty value, THEN THE Transfer_Service SHALL reject the request with HTTP 400 and error code `bad_request` before comparing, and SHALL keep the Upload_Session, the Partial_File and the Confirmed_Offset unchanged
5. WHEN a complete request for an Upload_Session whose Confirmed_Offset equals the Declared_Size carries no Checksum, THE Transfer_Service SHALL complete the upload as defined in Requirement 3, store the computed Checksum in the file metadata and return the stored file with Integrity_Status `unverified`
6. WHEN `POST /api/files` is called with a `sha256` query parameter of exactly 64 hexadecimal characters, THE Transfer_Service SHALL apply the same case-insensitive comparison and the same outcomes as criteria 2 and 3, and SHALL store no file when the Checksums differ
7. IF the `sha256` query parameter of `POST /api/files` is present but is not exactly 64 hexadecimal characters, including an empty value, THEN THE Transfer_Service SHALL reject the request with HTTP 400 and error code `bad_request` and store no file
8. IF an append, query or complete request names an Upload_Session that was deleted because of `checksum_mismatch`, THEN THE Transfer_Service SHALL respond `not_found` and SHALL NOT recreate the Upload_Session or the file
9. FOR ALL byte sequences (including the empty sequence), ALL bit positions and ALL single-bit corruptions applied between the Web_Client hash and the stored bytes, THE Transfer_Service SHALL reject the completion with HTTP 422 and `checksum_mismatch` (property)
10. FOR ALL uncorrupted uploads that carry the correct Checksum in lowercase or uppercase hexadecimal, THE Transfer_Service SHALL publish the file with Integrity_Status `verified` (property)
11. THE Transfer_Service SHALL NOT publish any file whose supplied Checksum differs from the computed Checksum, and SHALL NOT assign Integrity_Status `verified` to any file whose supplied Checksum was not equal to the computed Checksum (invariant)

### Requirement 10: การคำนวณ Checksum และป้ายสถานะบน Web_Client

**User Story:** As a Member, I want เห็นว่าไฟล์แต่ละไฟล์ตรวจแล้วหรือไม่ และส่งใหม่ได้เมื่อไม่ตรง, so that ฉันมั่นใจว่าไฟล์ไม่เสียหาย

#### Acceptance Criteria

1. WHEN verification is enabled (default enabled) and a file is queued for upload, THE Web_Client SHALL compute the Checksum of that file incrementally in a Web Worker, reading the file in slices of at most 4 MiB (4,194,304 bytes), so that the Web_Client holds at most one slice of file data for hashing at any time regardless of file size, and SHALL produce the Checksum `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` for a file of 0 bytes
2. THE Web_Client SHALL compute SHA-256 with a script bundled in `web/` and served from the same origin, SHALL NOT depend on `crypto.subtle` or on any third-party host, because plain HTTP on the LAN is not a Secure_Context, and SHALL express every Checksum it produces as 64 lowercase hex characters
3. WHILE the Web_Client hashes a file, THE Web_Client SHALL keep the user interface responsive, with no main-thread task longer than 50 ms caused by hashing, and SHALL display the state "กำลังคำนวณ SHA-256" with progress as a whole-number percentage (0 to 100) of the file bytes hashed, updated at least once every 500 ms
4. WHEN the LanShare_Server has confirmed the last byte of the file (Confirmed_Offset equals Declared_Size) and the Web_Client has finished hashing all Declared_Size bytes, THE Web_Client SHALL send the complete request carrying the Checksum and the Resume_Key in a request header, and SHALL NOT send the complete request before both conditions hold
5. WHERE verification is enabled, WHEN the Web_Client continues an Upload_Session after a page reload with the file re-selected and no hash state in memory, THE Web_Client SHALL hash the first Confirmed_Offset bytes of the re-selected file locally, compare the result case-insensitively with the digest returned by the Upload_Session query (sent with the Resume_Key in a request header), and, WHEN the two are equal, continue the upload from Confirmed_Offset; WHEN Confirmed_Offset is 0, THE Web_Client SHALL skip the comparison and upload from offset 0
6. IF the comparison in criterion 5 shows the digests are not equal, or the size of the re-selected file differs from Declared_Size, THEN THE Web_Client SHALL discard the local hash state, send a cancel request for the Upload_Session, start a new Upload_Session from offset 0 whether the cancel request succeeds or fails (including HTTP 404 `not_found`), and show a message stating that the selected file differs from the file that was partly sent
7. IF the Upload_Session query for a continuation answers HTTP 404 `not_found`, THEN THE Web_Client SHALL discard the stored session ID and Resume_Key, SHALL NOT send a cancel request, start a new Upload_Session from offset 0 with the same name, Dir and Conflict_Policy, and show a message stating that the earlier Upload_Session is no longer available
8. IF reading the file or the Web Worker fails after hashing has started (file unreadable, removed or modified so that a slice cannot be read, or the worker reports an error), THEN THE Web_Client SHALL stop hashing, SHALL NOT send a Checksum for that file, show that transfer as failed with a message stating that the file could not be read, and keep the Upload_Session so that the user can continue it later
9. IF the LanShare_Server answers HTTP 422 `checksum_mismatch` to the complete request, THEN THE Web_Client SHALL show that transfer as "ไฟล์ไม่ตรงกัน" (not as completed) with a "ส่งใหม่" button, and SHALL NOT resend automatically
10. WHEN the user presses the "ส่งใหม่" button, THE Web_Client SHALL hash the file again from byte 0 and start a new Upload_Session from offset 0 with the same name, Dir and Conflict_Policy, replacing the stored session ID and Resume_Key of the failed transfer
11. WHEN the file listing contains a file, THE Web_Client SHALL show a badge that uses both an icon and Thai text: "ตรวจแล้ว" for Integrity_Status `verified`, "ยังไม่ได้ตรวจกับต้นทาง" for `unverified`, and no badge (no icon and no text) for `unknown` or an absent Integrity_Status
12. WHEN the user opens the details of a file that has a Checksum, THE Web_Client SHALL show the full 64-character Checksum and a button that copies it to the clipboard, SHALL show a confirmation for 2 seconds after the copy succeeds, and IF the clipboard is unavailable (plain HTTP is not a Secure_Context), THEN THE Web_Client SHALL select the Checksum text and ask the user to copy it manually
13. WHEN the user selects a local file to compare with a listed file that has a Checksum, THE Web_Client SHALL hash the local file with the same incremental method (criteria 1 to 3) and show a result stating whether its Checksum equals the stored Checksum, comparing the two case-insensitively, covering the case of a downloaded copy
14. WHERE the user turns verification off in the settings, THE Web_Client SHALL apply the setting to files queued after the change, SHALL NOT start hashing for them, and SHALL send the complete request without a Checksum, so that the file listing shows Integrity_Status `unverified` for each such file after completion
15. IF the Web Worker cannot be created, THEN THE Web_Client SHALL upload without a Checksum, tell the user that the check was skipped (the resulting Integrity_Status is `unverified`), and, for a local-file comparison (criterion 13), tell the user that the comparison cannot be performed

### Requirement 11: ต่ออัปโหลดอัตโนมัติบน Web_Client

**User Story:** As a Member, I want ให้เบราว์เซอร์ต่ออัปโหลดเองเมื่อ Wi-Fi กลับมา, so that ฉันไม่ต้องคอยกดส่งใหม่

#### Acceptance Criteria

1. WHEN an append request fails because of a network error, because no response or upload progress arrived for 45 seconds, because of HTTP 408 `stall_timeout`, or because of an HTTP 5xx response other than 507, THE Web_Client SHALL query the Upload_Session and continue sending from the returned Confirmed_Offset, and IF the query fails for one of the same reasons, THEN THE Web_Client SHALL repeat the query under the wait schedule of criterion 2
2. WHILE the Web_Client retries, THE Web_Client SHALL wait 1 second before the first retry, double the wait after each failed retry (1, 2, 4, 8, then 15 seconds, and 15 seconds for every later retry), reset the wait to 1 second after any increase of the Confirmed_Offset, and show the state "กำลังเชื่อมต่อใหม่" from the first failure until the next increase of the Confirmed_Offset
3. WHEN the browser fires the `online` event while a transfer is waiting to retry, THE Web_Client SHALL cancel the remaining wait, reset the wait to 1 second and send the next retry within 1 second, and WHEN the transfer is paused under criterion 4, 6 or 12, THE Web_Client SHALL ignore the `online` event for that transfer
4. WHILE the Web_Client retries, IF the Confirmed_Offset has not increased for 10 minutes (measured from the last increase, or from the first failure if no byte was confirmed yet), THEN THE Web_Client SHALL pause the transfer, stop automatic retries, show a "ต่อ" button and keep the Upload_Session entry in `localStorage`
5. IF an Upload_Session query or append request answers HTTP 404 `not_found`, THEN THE Web_Client SHALL remove the stored entry of that session, show a message that the session expired, and start a new Upload_Session for the same file with the same Dir and Conflict_Policy from offset 0, and IF this happens 3 times in a row without any byte confirmed, THEN THE Web_Client SHALL pause the transfer, show an error message and show a "ต่อ" button
6. IF a request answers HTTP 401, THEN THE Web_Client SHALL pause the transfer, stop automatic retries, keep the stored Upload_Session entry including its Resume_Key, and show a message that the user must enter the room again
7. THE Web_Client SHALL send the remaining bytes in append requests of at most 33,554,432 bytes (32 MiB) each, built with `Blob.slice`, with at most one append request in flight per Upload_Session, so that memory use on mobile browsers does not grow with the file size
8. THE Web_Client SHALL store the session ID, Resume_Key, name, size, last modified time, Dir and Conflict_Policy of every unfinished Upload_Session in `localStorage` before sending the first append request, and SHALL remove the entry when the session completes, is cancelled or expires, and SHALL send the Resume_Key only in a request header and never show it on the page or place it in a URL
9. WHEN the page loads and unfinished Upload_Sessions are stored, THE Web_Client SHALL list them as "อัปโหลดที่ค้างอยู่" and let the user select the original file to continue, and the Web_Client SHALL accept the selected file only if its name and size equal the stored name and size (the last modified time is not compared), otherwise it SHALL show an error message indicating a file mismatch and keep the entry unchanged
10. WHEN the user cancels a transfer, THE Web_Client SHALL abort the running request, stop all pending retries, send one request to cancel the Upload_Session on the LanShare_Server, and remove the stored entry even if the cancel request fails
11. FOR ALL interruption points during an upload, THE progress shown by the Web_Client SHALL be derived only from Confirmed_Offset values returned by the LanShare_Server, SHALL equal the returned Confirmed_Offset after resuming, and SHALL NOT decrease during a continuous transfer of one Upload_Session, except for the reset to 0 caused by criterion 5 (property)
12. IF an append request answers HTTP 507 `insufficient_storage`, HTTP 422 `checksum_mismatch`, or any other 4xx response not covered by criteria 1, 5, 6 and 13, THEN THE Web_Client SHALL stop the transfer without automatic retry, show an error message indicating the cause, and keep the stored entry only for 507 so that the user can press "ต่อ" after freeing space
13. WHEN an append request answers HTTP 409 `offset_mismatch` or `upload_busy`, THE Web_Client SHALL continue sending immediately from the current Confirmed_Offset given in the response (after querying the Upload_Session when the response carries none) without waiting, and IF 3 consecutive such 409 responses occur without an increase of the Confirmed_Offset, THEN THE Web_Client SHALL treat the next failure as a retriable failure under criteria 1 and 2
14. WHEN the user has entered the room again after a pause under criterion 6, THE Web_Client SHALL query the Upload_Session with the stored Resume_Key and continue sending from the returned Confirmed_Offset without asking the user to select the file again, provided the file object is still held in the page

### Requirement 12: รายงานความคืบหน้าของ Upload_Session

**User Story:** As a Device in the Room, I want เห็นสถานะการส่งของอุปกรณ์อื่นรวมถึงตอนที่หยุดชั่วคราว, so that รู้ว่าการส่งยังมีชีวิตอยู่

#### Acceptance Criteria

1. WHILE an Upload_Session receives bytes, THE Transfer_Service SHALL broadcast to every connection of the Room a `transfer` event with state `running`, `id` equal to the session ID, `bytes` equal to the Confirmed_Offset, and `total` equal to the Declared_Size, with at least 250 ms between consecutive `running` events of the same session
2. WHEN an append request ends before the Confirmed_Offset reaches the Declared_Size (client disconnect, Stall_Timeout expiry, or read error), THE Transfer_Service SHALL broadcast, within 1 second of the request ending and without waiting for the 250 ms interval, a `transfer` event with state `interrupted`, `id` equal to the session ID, `bytes` equal to the Confirmed_Offset at that moment, `total` equal to the Declared_Size, and `speed` equal to 0
3. WHEN an append request is accepted for an Upload_Session whose last broadcast state is `interrupted`, THE Transfer_Service SHALL broadcast all subsequent `transfer` events of that session with the same `id` and with `bytes` greater than or equal to the `bytes` of the `interrupted` event
4. WHEN an Upload_Session completes (Confirmed_Offset equals the Declared_Size and any Checksum supplied by the sender matches), THE Transfer_Service SHALL broadcast exactly one `transfer` event with state `done`, `bytes` equal to `total` equal to the Declared_Size, after the last `running` event of that session and after the file becomes part of the Room
5. IF completion fails with HTTP 422 `checksum_mismatch`, THEN THE Transfer_Service SHALL broadcast exactly one `transfer` event with state `failed`, the same `id`, and `bytes` equal to the Confirmed_Offset, and SHALL NOT broadcast any further event for that session
6. WHEN the Web_Client receives a `transfer` event with state `interrupted` whose `deviceId` differs from its own Device, THE Web_Client SHALL show on that transfer's entry the text "หยุดชั่วคราว" together with `bytes` of `total` confirmed so far, and SHALL keep showing it until another event with the same `id` arrives
7. WHEN the Web_Client receives a `transfer` event with state `running`, `done`, `failed`, or `stopped` for an `id` currently shown as "หยุดชั่วคราว", THE Web_Client SHALL remove the text "หยุดชั่วคราว" from that entry and show the state and `bytes` of the new event on the same entry, without creating a second entry for that `id`
8. WHEN the Upload_Janitor removes an unfinished Upload_Session whose last broadcast state is `interrupted`, THE Transfer_Service SHALL broadcast one `transfer` event with state `stopped`, the same `id`, and `bytes` equal to the last Confirmed_Offset

### Requirement 13: กัน Mac หลับระหว่างถ่ายโอน

**User Story:** As an Owner, I want ให้ Mac ไม่หลับขณะมีการส่งไฟล์, so that การถ่ายโอนไม่ขาดกลางทาง

#### Acceptance Criteria

1. WHILE the `-keep-awake` setting is enabled, WHEN the number of Active_Transfers changes from 0 to 1, THE Power_Guard SHALL start exactly one sleep prevention assertion by running one `caffeinate` child process within 1 second and SHALL set its status to `active`
2. WHILE the `-keep-awake` setting is enabled and at least one Active_Transfer exists, THE Power_Guard SHALL keep the `caffeinate` process of the assertion running without restarting it
3. WHEN the Idle_Grace of 30 seconds elapses since the number of Active_Transfers changed to 0 and no new Active_Transfer has started, THE Power_Guard SHALL terminate the `caffeinate` process within 1 second, confirm that it has exited and set its status to `idle`
4. WHEN a new Active_Transfer starts during the Idle_Grace, THE Power_Guard SHALL cancel the Idle_Grace countdown, keep the existing `caffeinate` process and SHALL NOT start a second `caffeinate` process
5. WHEN the LanShare_Server receives SIGINT or SIGTERM, THE Power_Guard SHALL terminate the `caffeinate` process and confirm that it has exited within 2 seconds, regardless of the number of Active_Transfers or any remaining Idle_Grace, before the LanShare_Server process exits
6. THE Power_Guard SHALL bind the `caffeinate` process to the process ID of the LanShare_Server so that the `caffeinate` process exits within 5 seconds after the LanShare_Server terminates abnormally (for example by SIGKILL or a crash)
7. IF `caffeinate` cannot be started, or the `caffeinate` process exits when the Power_Guard did not request it to stop, THEN THE Power_Guard SHALL write exactly one warning log entry per failure stating the cause, SHALL let every Active_Transfer continue without abort or interruption, SHALL set its status to `unavailable` so that the Diagnostics_Service reports it as a warning, and SHALL NOT retry starting `caffeinate` until the next start attempt defined in criterion 1
8. WHILE the `-keep-awake` setting is disabled, THE Power_Guard SHALL start no `caffeinate` process and SHALL set its status to `disabled`, regardless of the number of Active_Transfers
9. FOR ALL sequences of transfer start and finish events with a fake clock and a fake `caffeinate` launcher that always starts successfully, WHILE the `-keep-awake` setting is enabled, THE Power_Guard SHALL, after each event and each clock advance, hold the assertion if and only if at least one Active_Transfer exists or at least one Active_Transfer has ended and the time since the most recent end is strictly below the Idle_Grace of 30 seconds, and SHALL hold at most one assertion at a time (property)
10. WHEN the Owner opens the Web_Client, THE Web_Client SHALL show the Power_Guard status as "กำลังทำงาน" for `active`, "ว่าง" for `idle`, "ปิดอยู่" for `disabled` and "ใช้ไม่ได้" for `unavailable`, and SHALL update the shown label within 5 seconds after the status changes while the Web_Client stays open
11. IF the status is `unavailable`, THEN WHEN a start attempt of `caffeinate` succeeds, THE Power_Guard SHALL set its status to `active` and the Diagnostics_Service SHALL stop reporting `unavailable` within 5 seconds

### Requirement 14: ผลตรวจการเชื่อมต่อสำหรับ Owner

**User Story:** As an Owner, I want ให้ระบบตรวจสภาพแวดล้อมและบอกสาเหตุที่อุปกรณ์อื่นเชื่อมต่อไม่ได้, so that ฉันแก้ปัญหา Firewall หรือ Wi-Fi ได้เอง

#### Acceptance Criteria

1. WHEN an Owner requests diagnostics, THE Diagnostics_Service SHALL return exactly one Check_Result for each of the 7 checks (network interface, firewall, reachability, Wi-Fi band, disk space, sleep prevention, secure context), each with a `status` of `ok`, `warn`, `fail` or `unknown`, a title and a detail, and with a non-empty recommendation whenever the `status` is `warn` or `fail`
2. IF a Device whose Role is not Owner requests diagnostics, the list of unfinished Upload_Sessions or the cancellation of an Upload_Session through the Diagnostics_Service, THEN THE Web_Server SHALL answer HTTP 403 with error code `forbidden`, return no Check_Result and no Upload_Session data, and SHALL NOT run any check, run any external command or cancel any Upload_Session
3. WHEN the Active_Interface is a VPN or virtual interface, or more than one physical interface has an IPv4 address, THE Diagnostics_Service SHALL report the network interface check as `warn` and name the Active_Interface and its IPv4 address, and otherwise SHALL report it as `ok`
4. WHEN the macOS application firewall is enabled and the LanShare_Server program is not allowed to accept incoming connections, THE Diagnostics_Service SHALL report the firewall check as `warn` and list the steps to allow the program, naming the macOS firewall settings screen where the program is allowed
5. WHEN the macOS application firewall is disabled, or is enabled and the LanShare_Server program is allowed to accept incoming connections, THE Diagnostics_Service SHALL report the firewall check as `ok`
6. IF the firewall state cannot be read, THEN THE Diagnostics_Service SHALL report the firewall check as `unknown` and state the reason (command missing, command failed, output not understood or timeout)
7. WHEN at least one request from a non-loopback address was received within the last 10 minutes and none of the requests received in that period came from an IPv4 address outside the subnet of the Active_Interface, THE Diagnostics_Service SHALL report the reachability check as `ok` and show the date, time (to the second) and source address of the latest such request, where a non-loopback address is an address outside 127.0.0.0/8 and `::1` that is not assigned to a network interface of the Mac
8. IF no request from a non-loopback address was received within the last 10 minutes, THEN THE Diagnostics_Service SHALL report the reachability check as `warn` and give the hints to check AP Isolation on the router, the firewall and that all devices use the same Wi-Fi network, and SHALL NOT claim that AP Isolation is confirmed
9. IF at least one request from an IPv4 address outside the subnet of the Active_Interface was received within the last 10 minutes, THEN THE Diagnostics_Service SHALL report the reachability check as `warn`, show that source address and suggest that the device may be on a guest network or another VLAN
10. WHEN the Active_Interface is Wi-Fi on the 2.4 GHz band, THE Diagnostics_Service SHALL report the Wi-Fi band check as `warn` and recommend the 5 GHz band
11. WHEN the Active_Interface is Wi-Fi on the 5 GHz or 6 GHz band, THE Diagnostics_Service SHALL report the Wi-Fi band check as `ok`
12. IF the Active_Interface is not Wi-Fi or the band cannot be read, THEN THE Diagnostics_Service SHALL report the Wi-Fi band check as `unknown` and state the reason (not a Wi-Fi interface, command missing, command failed, output not understood or timeout)
13. WHEN diagnostics run, THE Diagnostics_Service SHALL report the disk check as `fail` if the free space of the Data_Dir volume is below the Free_Space_Reserve, as `warn` if it is at least the Free_Space_Reserve and below 4 times the Free_Space_Reserve, and as `ok` if it is at least 4 times the Free_Space_Reserve, and SHALL include the free space in bytes and the total size in bytes of all Partial_Files in every one of these cases
14. WHEN diagnostics run, THE Diagnostics_Service SHALL report the sleep prevention check as `ok` if the Power_Guard status is `active` or `idle`, and as `warn` if the status is `unavailable` or `disabled`, and SHALL state that status in the detail
15. WHILE the Web_Server is reached over plain HTTP, THE Diagnostics_Service SHALL report the secure context check as `warn` and state that Service Workers and system notifications are unavailable on other devices until HTTPS is available
16. WHEN the Diagnostics_Service runs an external macOS command, THE Diagnostics_Service SHALL terminate that command if it has not exited after 3 seconds, or after 10 seconds for `system_profiler`, and SHALL report the related check as `unknown` with a reason that states a timeout
17. THE Diagnostics_Service SHALL start all 7 checks concurrently and SHALL return a response containing all 7 Check_Results within 12 seconds of the request, reporting any check still running at 12 seconds as `unknown` with a reason that states a timeout
18. THE Diagnostics_Service SHALL exclude PINs, tokens, Resume_Keys and file names from every Check_Result, and from every Upload_Session entry returned by the Owner's list request
19. WHEN the Owner requests the list of unfinished Upload_Sessions, THE Diagnostics_Service SHALL return every unfinished Upload_Session of the Room of the requesting Owner and of no other Room, and for each one its Declared_Size in bytes, Confirmed_Offset in bytes and last activity time, without the Resume_Key
20. IF a check cannot obtain its input because an external command is missing, exits with a non-zero code, produces output that cannot be parsed, or is denied permission, THEN THE Diagnostics_Service SHALL report that check as `unknown` with a reason naming the cause, SHALL keep the other 6 Check_Results unaffected, and SHALL still answer the request successfully
21. WHEN the Owner cancels an Upload_Session of the Owner's Room from the list, THE Transfer_Service SHALL delete its Partial_File and persisted state and publish a transfer event with state `stopped`, without requiring the Resume_Key, and IF the session ID does not exist, belongs to another Room, or was already finished or cancelled, THEN THE Transfer_Service SHALL respond `not_found` and change nothing
22. WHEN an Owner requests diagnostics within 30 seconds of the completion of the last full run and does not ask for a fresh run, THE Diagnostics_Service SHALL return the cached Check_Results together with the time they were produced, and WHEN the Owner asks for a fresh run or the cache is older than 30 seconds, THE Diagnostics_Service SHALL run all 7 checks again and replace the cache

### Requirement 15: หน้าตรวจปัญหาการเชื่อมต่อบน Web_Client

**User Story:** As an Owner, I want ดูผลตรวจในหน้าเดียวและคัดลอกไปส่งให้คนช่วย, so that การแก้ปัญหาเร็วขึ้น

#### Acceptance Criteria

1. WHERE the signed-in Device is the Owner, THE Web_Client SHALL show a menu entry "ตรวจปัญหาการเชื่อมต่อ" that opens the diagnostics page
2. WHERE the signed-in Device is not the Owner, THE Web_Client SHALL render no diagnostics menu entry and no diagnostics page, SHALL NOT send any request to the diagnostics endpoints, and SHALL show no diagnostics data
3. IF a Device whose Role is not Owner requests a diagnostics endpoint or the network selection endpoint directly, THEN THE Web_Server SHALL respond with HTTP 403 and error code `forbidden`, SHALL return no Check_Result and no network data, and SHALL leave the selected network interface unchanged
4. IF the Web_Client receives HTTP 403 `forbidden` from a diagnostics endpoint, THEN THE Web_Client SHALL close the diagnostics page, remove the diagnostics menu entry, discard any Check_Results held in memory, and show a Thai message that this function is for the Owner only
5. WHEN the diagnostics page opens, THE Web_Client SHALL send exactly one request for the results and, on success, SHALL display every returned Check_Result as one row containing its title, its status, its details and its hint, in the order returned by the Web_Server
6. THE Web_Client SHALL show the status of each Check_Result with both an icon and Thai text, using "ปกติ" for `ok`, "ควรตรวจสอบ" for `warn`, "ผิดปกติ" for `fail` and "ไม่ทราบ" for `unknown`, with a different icon shape for each of the four statuses, so that the status is distinguishable without relying on colour
7. WHEN the Web_Client displays a Check_Result status, THE Web_Client SHALL expose the status text to assistive technology as text of the row, not only as an image or colour, and SHALL mark each icon as decorative
8. IF a Check_Result has an empty details or empty hint value, THEN THE Web_Client SHALL omit that field from the row and SHALL still show the title and status
9. IF a Check_Result carries a `status` other than `ok`, `warn`, `fail` or `unknown`, THEN THE Web_Client SHALL display that Check_Result with the "ไม่ทราบ" status text and `unknown` icon
10. WHILE the results are loading, THE Web_Client SHALL show a loading state with the text "กำลังตรวจ…" on the diagnostics page, SHALL disable the "ตรวจใหม่" button so that at most one diagnostics request is in flight, and SHALL keep the rest of the application usable
11. IF no response to a diagnostics request arrives within 30 seconds, THEN THE Web_Client SHALL abort the request and handle it as a failed request under criterion 17
12. WHEN the Owner presses "ตรวจใหม่", THE Web_Client SHALL send one new request, SHALL keep the previous Check_Results visible, marked as old, until the new results arrive, and SHALL replace the whole list with the new results on success
13. WHEN the Owner presses "คัดลอกรายงาน", THE Web_Client SHALL write to the clipboard a plain text report containing, for every currently displayed Check_Result, its title, status text, details and hint, together with the time of the run, and SHALL contain no PIN, token, Resume_Key or session secret
14. WHEN the report is copied successfully, THE Web_Client SHALL show the confirmation "คัดลอกแล้ว" for at least 3 seconds
15. IF writing the report to the clipboard fails or the clipboard is unavailable, THEN THE Web_Client SHALL show a Thai error message and SHALL show the report text in a selectable text area so that the Owner can copy it manually
16. WHEN the network interface check is shown, THE Web_Client SHALL list the selectable interfaces returned by the existing network selection endpoint, and WHEN the Owner chooses another interface, THE Web_Client SHALL send that selection and, on success, SHALL request a fresh run and update the display
17. IF the diagnostics request or the interface selection request fails for a reason other than `forbidden`, THEN THE Web_Client SHALL show an error message in Thai that names the cause category (network error, timeout or server error), SHALL keep the previously displayed Check_Results marked as old, SHALL keep the selected interface unchanged in the display, and SHALL enable the "ตรวจใหม่" button
18. WHEN the Web_Client shows or updates a status, a loading state or an error on the diagnostics page, THE Web_Client SHALL announce the change to assistive technology without moving keyboard focus
19. FOR ALL lists of Check_Results returned by the Web_Server, THE Web_Client SHALL show exactly as many rows as the list contains, each row showing a status icon and status text together (property)
20. FOR ALL Check_Result lists, THE copied report SHALL NOT contain any string equal to a current PIN or an issued token (invariant)

### Requirement 16: PWA บน HTTP และ Secure_Context

**User Story:** As a Member, I want เพิ่ม LAN Share ลงหน้าจอหลักของมือถือ, so that เปิดใช้งานได้เหมือนแอป

#### Acceptance Criteria

1. WHEN a client sends `GET /manifest.webmanifest`, THE Web_Server SHALL respond with status 200, Content-Type `application/manifest+json`, a `Cache-Control` value of `no-cache`, and a body that is a valid JSON object, over plain HTTP and over HTTPS alike
2. THE manifest at `/manifest.webmanifest` SHALL contain `name` (non-empty string), `short_name` (non-empty string of at most 12 characters), `start_url` `/`, `scope` `/`, `display` `standalone`, `theme_color` and `background_color` (each a valid CSS color string), and an `icons` array with PNG entries for `/icons/icon-192.png` (`sizes` `192x192`, `purpose` `any`), `/icons/icon-512.png` (`sizes` `512x512`, `purpose` `any`) and `/icons/icon-maskable-512.png` (`sizes` `512x512`, `purpose` `maskable`)
3. WHEN a client sends `GET` for `/icons/icon-192.png`, `/icons/icon-512.png`, `/icons/icon-maskable-512.png` or `/icons/apple-touch-icon.png`, THE Web_Server SHALL respond with status 200, Content-Type `image/png`, and a PNG whose pixel width and height equal the size declared for that file (192x192, 512x512, 512x512 and 180x180 respectively)
4. THE Web_Client SHALL include in the `<head>` of `index.html` exactly one `<link rel="manifest" href="/manifest.webmanifest">`, one `<meta name="theme-color">` whose `content` equals the `theme_color` of the manifest, and one `<link rel="apple-touch-icon" href="/icons/apple-touch-icon.png">`, and SHALL use no inline `<script>`, inline `<style>`, or inline event-handler attribute, so that every page remains valid under the existing Content-Security-Policy without any change to that policy
5. THE LanShare_Server SHALL embed `manifest.webmanifest`, `sw.js` and every file under `icons/` in the binary together with the other files of `web/`, so that criteria 1, 3 and 6 are satisfied when the binary runs from a directory that contains no `web/` folder
6. WHEN a client sends `GET /sw.js`, THE Web_Server SHALL respond with status 200, Content-Type `text/javascript`, and `Cache-Control` of `no-cache`, serving the script from the root path `/` so that a registration with scope `/` is permitted
7. WHILE the Web_Client runs in a Secure_Context and the browser supports Service Workers, THE Web_Client SHALL, once per page load after the `load` event, register the script `/sw.js` with scope `/`
8. WHILE the Web_Client does not run in a Secure_Context or the browser does not support Service Workers, THE Web_Client SHALL NOT call the Service Worker registration, SHALL show no error message to the user, SHALL throw no uncaught exception, and SHALL keep upload, download, file list, chat and clipboard working
9. THE Service Worker SHALL write to the cache only responses to same-origin `GET` requests whose URL path exactly equals one of these allow-list entries: `/`, `/index.html`, `/style.css`, `/app.js`, `/manifest.webmanifest`, `/icons/icon-192.png`, `/icons/icon-512.png`, `/icons/icon-maskable-512.png`, `/icons/apple-touch-icon.png`, and only when the response status is 200
10. THE Service Worker SHALL pass to the network without reading from or writing to any cache every request that is not a `GET`, whose URL path equals `/ws`, `/sw.js` or starts with `/api/` (this covers the file list, file downloads, zip downloads, thumbnails, messages, clipboard and the QR image), or whose URL path is not on the allow-list of criterion 9
11. WHEN a request URL is on the allow-list, THE Service Worker SHALL fetch it from the network first and respond with the cached copy only if the network request fails with a network error, so that a reachable LanShare_Server never receives a stale App_Shell file
12. WHEN the LanShare_Server serves a new version of the App_Shell, THE Service Worker SHALL use a cache name of the form `lanshare-shell-<version>` where `<version>` changes whenever any allow-list file changes, and WHEN the Service Worker activates, THE Service Worker SHALL delete every cache whose name starts with `lanshare-shell-` and differs from its own cache name
13. IF the Web_Client was loaded from the Service Worker cache and its first request to `/api/` fails with a network error or receives no response within 10 seconds, THEN THE Web_Client SHALL show a message in Thai "เชื่อมต่อเซิร์ฟเวอร์ไม่ได้" with a retry button labelled "ลองใหม่", SHALL render no file list, file name, thumbnail, message or clipboard content, and WHEN the user presses the retry button, THE Web_Client SHALL repeat the request and replace the message with the normal screen if the request succeeds
14. WHILE the Web_Client does not run in a Secure_Context, THE Web_Client SHALL show in the settings a note in Thai "การติดตั้งแอปและการแจ้งเตือนของระบบต้องใช้ HTTPS", SHALL show no text or control stating that the app is installed or offering to install it, and SHALL keep serving and linking the manifest as in criteria 1 and 4
15. IF `/manifest.webmanifest` or `/sw.js` fails to load, or the Service Worker registration is rejected, THEN THE Web_Client SHALL show no error message to the user, SHALL throw no uncaught exception, and SHALL keep upload, download, file list, chat and clipboard working as in a normal web page
16. FOR ALL requests handled by the Service Worker, a URL is written to the cache only if its path is on the allow-list of criterion 9, and no response from `/api/`, `/ws` or `/sw.js` is ever stored in or served from any cache (property)

### Requirement 17: แจ้งเตือนไฟล์ใหม่

**User Story:** As a Member, I want ได้รับแจ้งเมื่ออุปกรณ์อื่นส่งไฟล์เข้าห้อง, so that ฉันไม่ต้องเปิดหน้านี้ค้างไว้เพื่อเช็กเอง

#### Acceptance Criteria

1. WHEN the Web_Client receives a `file.added` event that carries only a file id and the Role of the Device satisfies `CanDownload`, THE Web_Client SHALL obtain the file name and the uploader Device identity (id and name) for that file id from the file metadata available to that Role, and SHALL treat the file as a notifiable file only if the uploader Device id differs from the Device's own id
2. WHEN a notifiable file is obtained while `document.visibilityState` is `visible`, THE Web_Client SHALL show an in-page notice that contains the file name and the uploader name, and SHALL remove the notice when the user dismisses it or 5 seconds after it was shown, whichever comes first
3. WHILE `document.visibilityState` is `hidden`, WHEN a notifiable file is obtained, THE Web_Client SHALL increase the unseen file count by 1 and SHALL set `document.title` to `"(N) "` followed by the original title, where N is the unseen file count shown as `99+` when it exceeds 99, and WHEN the page becomes visible, THE Web_Client SHALL reset the unseen file count to 0 and SHALL restore the original `document.title`
4. WHEN the user turns on system notifications in the settings by a user action and the Notification permission is `default`, THE Web_Client SHALL request the Notification permission at that moment and at no other time, and IF the permission result is not `granted`, THEN THE Web_Client SHALL keep the system notification setting off and SHALL show a message in Thai in the settings stating that the browser blocks notifications and that the permission can be changed in the browser settings
5. WHERE the Notification API is available in a Secure_Context and the system notification setting is on and the Notification permission is `granted`, WHILE `document.visibilityState` is `hidden`, WHEN a notifiable file is obtained, THE Web_Client SHALL show a system notification that contains the file name and the uploader name, and WHEN the user selects the system notification, THE Web_Client SHALL focus the page and open the file list
6. WHILE the Web_Client does not run in a Secure_Context or the Notification API is unavailable, THE Web_Client SHALL hide the control for system notifications, SHALL NOT request the Notification permission, and SHALL use only the in-page notice and the title count
7. WHEN more than one notifiable file is obtained within 5 seconds after the first notifiable file of a group, THE Web_Client SHALL replace the notice or system notification already shown for that group with a single notice or notification that states the total number of files in the group as `"{N} ไฟล์ใหม่"` and does not show individual file names, and SHALL start a new group at the next notifiable file obtained after the 5 seconds have passed
8. WHEN the uploader Device id of a file equals the Device's own id, THE Web_Client SHALL show no in-page notice, no system notification, and SHALL NOT count the file in the unseen file count or in a merged group
9. WHERE the Role of the Device is `uploader`, THE Web_Client SHALL NOT request file metadata after receiving `file.added`, SHALL show no in-page notice, no system notification, and no title count, and SHALL hide the control for system notifications
10. THE Web_Client SHALL store the system notification setting in `localStorage`, SHALL keep it off when no stored value exists or `localStorage` is unavailable, and SHALL NOT turn it on without a user action
11. IF the WebSocket is disconnected, THEN THE Web_Client SHALL show no in-page notice, no system notification, and no title count for files added while it was disconnected, including after the WebSocket reconnects
12. IF the file metadata for a file id cannot be obtained, THEN THE Web_Client SHALL skip that file, SHALL show no notice, notification, or title count for it, and SHALL show no error to the user
13. THE Web_Client SHALL show in the settings the Thai text `"การแจ้งเตือนทำงานเฉพาะขณะเปิดหน้านี้ไว้เท่านั้น"` to state that notifications work only while the page is open

### Requirement 18: ค่าตั้งของ Phase C

**User Story:** As an Owner, I want ปรับค่าที่เกี่ยวกับความเสถียรจาก command line, so that เหมาะกับดิสก์และเครือข่ายของฉัน

#### Acceptance Criteria

1. THE Config_Loader SHALL accept the following flags in addition to the existing `-port`, `-data`, `-iface`, `-room-name`, `-max-pin-failures` and `-lockout`, each given as `-name value` or `-name=value` (the boolean `-keep-awake` only as `-keep-awake` or `-keep-awake=<true|false>`), and SHALL use the default when the flag is absent: `-min-free` (size, default `1GiB`), `-upload-ttl` (duration, default `24h`), `-stall-timeout` (duration, default `30s`), `-keep-awake` (boolean, default `true`)
2. THE Config_Loader SHALL parse a `-min-free` size as a non-negative decimal integer of 1 to 19 digits immediately followed by exactly one case-sensitive unit `B`, `KiB`, `MiB` or `GiB` (1 KiB = 1024 B, 1 MiB = 1024 KiB, 1 GiB = 1024 MiB) with no whitespace, and SHALL parse `-upload-ttl` and `-stall-timeout` using Go duration syntax (for example `90s`, `45m`, `24h`)
3. THE Config_Loader SHALL treat as valid only: a `-min-free` of 0 B up to 9223372036854775807 B (2^63 - 1) after unit conversion, where 0 B means no reserve is required; a `-upload-ttl` of 1 minute (`1m`) or longer; a `-stall-timeout` of 5 seconds (`5s`) or longer; and a `-keep-awake` of `true` or `false`
4. IF the value of `-min-free`, `-upload-ttl`, `-stall-timeout` or `-keep-awake` cannot be parsed under criterion 2 or is outside the valid range in criterion 3 (including a negative size, a size without a unit, a fractional size, an unknown unit, a size that overflows 2^63 - 1 bytes, `-upload-ttl` below `1m` and `-stall-timeout` below `5s`), THEN THE Config_Loader SHALL write one error message to standard error that contains the flag name, the rejected value and the accepted range, SHALL cause the LanShare_Server process to exit with code 1, and SHALL do so before the HTTP port is bound, before Data_Dir is created or modified, and before any Upload_Session or Partial_File is read or deleted
5. WHEN the LanShare_Server is started with `-h` or `-help`, THE Config_Loader SHALL write a usage text to standard error that lists every flag from criterion 1 and every existing flag with its name, type, default value and a one-line description (including the unit and valid range for `-min-free`, `-upload-ttl` and `-stall-timeout`), and THE LanShare_Server SHALL exit with code 0 without starting any server component
6. IF the LanShare_Server is started with a flag that is not defined or with a flag whose value is missing, THEN THE Config_Loader SHALL write an error message naming the flag and the usage text to standard error and THE LanShare_Server SHALL exit with code 1 without starting any server component
7. WHEN the Config_Loader accepts the configuration and before the Web_Server accepts its first connection, THE LanShare_Server SHALL write one INFO log record to standard error that contains, for each of `-min-free`, `-upload-ttl`, `-stall-timeout` and `-keep-awake`, the flag name and its effective value (including values taken from defaults), with `-min-free` shown in the canonical size format of criterion 8 and the two durations shown as Go duration strings
8. THE Config_Loader SHALL format a size as an integer followed by the largest unit among `GiB`, `MiB`, `KiB` and `B` that divides the byte count exactly (0 is formatted as `0B`)
9. FOR ALL byte counts from 0 to 9223372036854775807, formatting the count as a size under criterion 8 and parsing the result under criterion 2 SHALL produce the same number of bytes (round-trip property)
