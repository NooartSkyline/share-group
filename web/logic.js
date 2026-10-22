'use strict';

// ตรรกะล้วน (ไม่แตะ DOM, ไม่อ่านเวลา/สุ่มเอง) ของ uploader แบบต่อได้, การแจ้งเตือนไฟล์ใหม่
// และหน้าตรวจปัญหาการเชื่อมต่อ แยกจาก app.js เพื่อให้ `node --test` โหลดได้โดยไม่ต้องมี DOM
//
// รูปแบบ UMD: ในเบราว์เซอร์เป็น classic script ที่ตั้ง `globalThis.LanShareLogic`
// (ห่อใน IIFE จึงไม่เพิ่มชื่อระดับบนสุดที่ชนกับ app.js) ใน Node ใช้ `require('./logic.js')` ได้
//
// Invariant: ทุกฟังก์ชันที่ export ไม่มีผลข้างเคียงนอกอ็อบเจกต์ที่ตัวเองสร้าง
// (createRetryScheduler / createStreakCounter เก็บสถานะภายในเท่านั้น) เวลาปัจจุบันส่งเข้ามาเป็นอาร์กิวเมนต์
// ข้อความสำหรับผู้ใช้เป็นภาษาไทย และผู้เรียกต้องแสดงด้วย textContent เท่านั้น

(function (root, factory) {
  const api = factory();
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  root.LanShareLogic = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  // ---------- ค่าคงที่ ----------

  const MIB = 1024 * 1024;
  // ก้อนที่ส่งต่อ request: ใช้เพดาน 32 MiB ของ Req 11.7 เพื่อลดจำนวน
  // PATCH + fsync ต่อไฟล์ (บน macOS แต่ละครั้งจบ request เรียก F_FULLFSYNC)
  const CHUNK_SIZE = 32 * MIB;
  const MAX_CHUNK_SIZE = 32 * MIB;
  // ตารางรอก่อน retry หน่วยวินาที ค่าสุดท้ายใช้ซ้ำไปเรื่อยๆ
  const RETRY_WAITS_SEC = Object.freeze([1, 2, 4, 8, 15]);
  // ไม่มี response หรือความคืบหน้าอัปโหลดเกินเวลานี้ถือว่าขาด (Req 11.1)
  const NO_PROGRESS_TIMEOUT_MS = 45 * 1000;
  // offset ไม่เพิ่มเกินเวลานี้ขณะ retry → หยุดชั่วคราว (Req 11.4)
  const PAUSE_AFTER_MS = 10 * 60 * 1000;
  // 409 offset_mismatch/upload_busy ติดกันโดย offset ไม่เพิ่มกี่ครั้งจึงนับเป็นความล้มเหลวที่ลองใหม่ได้ (Req 11.13)
  const CONFLICT_STREAK_LIMIT = 3;
  // 404 ติดกันโดยไม่มีไบต์ที่ยืนยันกี่ครั้งจึงหยุดชั่วคราว (Req 11.5)
  const NOT_FOUND_STREAK_LIMIT = 3;
  // หน้าต่างรวมการแจ้งเตือนไฟล์ใหม่ (Req 17.7)
  const NOTIFY_WINDOW_MS = 5 * 1000;
  const TITLE_COUNT_MAX = 99;

  // การกระทำที่ classifyResult คืน
  const ACTION = Object.freeze({
    RETRY: 'retry',
    CONTINUE_NOW: 'continue-now',
    NEW_SESSION: 'new-session',
    PAUSE_AUTH: 'pause-auth',
    STOP: 'stop',
    STOP_KEEP_ENTRY: 'stop-keep-entry',
  });

  // ---------- ตัวจัดตาราง retry ----------

  // retryWaitSec คืนเวลารอ (วินาที) ของการ retry ครั้งที่ attempt (เริ่มที่ 0): 1, 2, 4, 8, 15, 15, …
  function retryWaitSec(attempt) {
    const n = Number.isInteger(attempt) && attempt > 0 ? attempt : 0;
    return RETRY_WAITS_SEC[Math.min(n, RETRY_WAITS_SEC.length - 1)];
  }

  // createRetryScheduler สร้างตัวจัดตารางรอของหนึ่ง upload (หนึ่งตัวต่อ item)
  //   failure(nowMs)   บันทึกความล้มเหลวหนึ่งครั้ง คืน {waitSec, pause, attempt}
  //                    pause เป็นจริงเมื่อ offset ไม่เพิ่มมาครบ pauseAfterMs นับจากการเพิ่มล่าสุด
  //                    (หรือจากความล้มเหลวแรกถ้ายังไม่เคยมีไบต์ที่ยืนยัน) ผู้เรียกต้องหยุด retry เมื่อ pause เป็นจริง
  //   progress(nowMs)  offset เพิ่ม: รีเซ็ตเวลารอเป็น 1 วินาที และเริ่มนับ 10 นาทีใหม่
  //   resetWait()      event `online`: รีเซ็ตเวลารอเป็น 1 วินาที โดยไม่ถือว่ามีความคืบหน้า
  //   shouldPause(nowMs) ตรวจเงื่อนไขหยุดชั่วคราวโดยไม่บันทึกความล้มเหลว
  //   reset()          ล้างสถานะทั้งหมด (เช่น เมื่อ "ต่อ" หลัง paused)
  //   state()          สำเนาสถานะสำหรับดีบัก/เทสต์
  function createRetryScheduler(opts) {
    const pauseAfterMs = opts && opts.pauseAfterMs !== undefined ? opts.pauseAfterMs : PAUSE_AFTER_MS;
    let attempt = 0;
    let lastIncreaseAt = null;
    let firstFailureAt = null;

    function overdue(nowMs) {
      const base = lastIncreaseAt !== null ? lastIncreaseAt : firstFailureAt;
      return base !== null && nowMs - base >= pauseAfterMs;
    }

    return {
      failure(nowMs) {
        if (firstFailureAt === null) firstFailureAt = nowMs;
        const waitSec = retryWaitSec(attempt);
        attempt += 1;
        return { waitSec, pause: overdue(nowMs), attempt };
      },
      progress(nowMs) {
        attempt = 0;
        lastIncreaseAt = nowMs;
        firstFailureAt = null;
      },
      resetWait() {
        attempt = 0;
      },
      shouldPause(nowMs) {
        return overdue(nowMs);
      },
      reset() {
        attempt = 0;
        lastIncreaseAt = null;
        firstFailureAt = null;
      },
      state() {
        return { attempt, lastIncreaseAt, firstFailureAt };
      },
    };
  }

  // createStreakCounter นับเหตุการณ์ที่เกิดติดกัน (409 หรือ 404) hit() คืนจำนวนรวมครั้งนี้
  // reset() เรียกเมื่อ offset เพิ่ม/ได้ไบต์ที่ยืนยัน
  function createStreakCounter() {
    let n = 0;
    return {
      hit() { n += 1; return n; },
      reset() { n = 0; },
      count() { return n; },
    };
  }

  // ---------- ตัวแบ่งก้อน Blob.slice ----------

  function checkRange(offset, size) {
    if (!Number.isSafeInteger(offset) || !Number.isSafeInteger(size) || offset < 0 || size < 0 || offset > size) {
      throw new RangeError('invalid upload range');
    }
  }

  function effectiveChunk(chunkSize) {
    if (chunkSize === undefined) return CHUNK_SIZE;
    if (!Number.isSafeInteger(chunkSize) || chunkSize < 1) throw new RangeError('invalid chunk size');
    return Math.min(chunkSize, MAX_CHUNK_SIZE);
  }

  // nextChunk คืนก้อนถัดไป {start, end} (ใช้กับ blob.slice(start, end)) ของช่วง [offset, size) หรือ null เมื่อส่งครบแล้ว
  // ก้อนยาว ≤ chunkSize (ค่าเริ่มต้น 32 MiB, ถูกจำกัดไม่เกิน 32 MiB เสมอ)
  // ผู้เรียกส่งก้อนเดียวต่อ session ต่อครั้ง แล้วเรียกใหม่ด้วย offset ที่เซิร์ฟเวอร์ยืนยัน
  function nextChunk(offset, size, chunkSize) {
    checkRange(offset, size);
    const step = effectiveChunk(chunkSize);
    if (offset >= size) return null;
    return { start: offset, end: Math.min(offset + step, size) };
  }

  // planChunks แบ่ง [offset, size) ทั้งช่วงเป็นก้อนต่อเนื่องที่ไม่ทับซ้อน (ใช้ทดสอบและคำนวณจำนวนก้อน)
  function planChunks(offset, size, chunkSize) {
    checkRange(offset, size);
    const out = [];
    let at = offset;
    for (;;) {
      const c = nextChunk(at, size, chunkSize);
      if (!c) break;
      out.push(c);
      at = c.end;
    }
    return out;
  }

  // ---------- ตัวจัดประเภทผลลัพธ์ ----------

  function isOffsetConflict(status, code) {
    return status === 409 && (code === 'offset_mismatch' || code === 'upload_busy');
  }

  // classifyResult เลือกการกระทำหนึ่งอย่างพอดีตามตารางตอบสนองของ Web_Client
  //   result: {status, code} หรือ {networkError: true} หรือ {noProgress: true} (ไม่มี response/ความคืบหน้า 45 วินาที)
  //   ctx.conflictStreak: จำนวน 409 offset_mismatch/upload_busy ที่ติดกันมาก่อนหน้านี้โดย offset ไม่เพิ่ม
  //
  //   network error / ไม่มี progress / 408 / 5xx ที่ไม่ใช่ 507 → retry
  //   409 offset_mismatch|upload_busy → continue-now (ติดกันครบ CONFLICT_STREAK_LIMIT แล้ว → retry)
  //   404 → new-session (ผู้เรียกใช้ notFoundAction ตัดสินว่าครั้งที่ 3 ติดกันให้หยุดชั่วคราว)
  //   401 → pause-auth,  507 → stop-keep-entry,  422 และ 4xx อื่น (รวม 409 ชนิดอื่น) → stop
  //   status ที่ไม่ใช่ความล้มเหลว (1xx-3xx) ไม่ควรส่งเข้ามา คืน stop เพื่อไม่ให้วนซ้ำ
  function classifyResult(result, ctx) {
    const r = result || {};
    if (r.networkError || r.noProgress) return ACTION.RETRY;
    const status = r.status;
    if (!Number.isInteger(status) || status === 0) return ACTION.RETRY;
    if (status === 507) return ACTION.STOP_KEEP_ENTRY;
    if (status === 408 || status >= 500) return ACTION.RETRY;
    if (status === 401) return ACTION.PAUSE_AUTH;
    if (status === 404) return ACTION.NEW_SESSION;
    if (isOffsetConflict(status, r.code)) {
      const streak = ctx && Number.isInteger(ctx.conflictStreak) ? ctx.conflictStreak : 0;
      return streak >= CONFLICT_STREAK_LIMIT ? ACTION.RETRY : ACTION.CONTINUE_NOW;
    }
    return ACTION.STOP;
  }

  // notFoundAction: 404 ครั้งที่ streak (นับครั้งปัจจุบันแล้ว) ติดกันโดยไม่มีไบต์ที่ยืนยัน
  // → 'new-session' สองครั้งแรก ครั้งที่ 3 เป็น 'pause' (Req 11.5)
  function notFoundAction(streak) {
    return Number.isInteger(streak) && streak >= NOT_FOUND_STREAK_LIMIT ? 'pause' : ACTION.NEW_SESSION;
  }

  // isChecksumMismatch: 422 checksum_mismatch ตอน complete ต้องแสดง "ไฟล์ไม่ตรงกัน" ไม่ใช่ข้อผิดพลาดทั่วไป
  function isChecksumMismatch(result) {
    return !!result && result.status === 422 && result.code === 'checksum_mismatch';
  }

  // ---------- ขนาดไฟล์ ----------

  // formatBytes: หน่วยไบนารี B, KiB, MiB, GiB ทศนิยม 1 ตำแหน่งเสมอ (เช่น "512.0 B", "1.5 GiB")
  // ค่าที่ปัดแล้วถึง 1024 จะขึ้นหน่วยถัดไป ค่าที่ไม่ใช่จำนวนจริงไม่ติดลบคืน '-'
  function formatBytes(n) {
    if (typeof n !== 'number' || !Number.isFinite(n) || n < 0) return '-';
    const units = ['B', 'KiB', 'MiB', 'GiB'];
    const last = units.length - 1;
    let v = n;
    let i = 0;
    while (v >= 1024 && i < last) { v /= 1024; i += 1; }
    if (i < last && Number(v.toFixed(1)) >= 1024) { v /= 1024; i += 1; }
    return v.toFixed(1) + ' ' + units[i];
  }

  // insufficientStorageMessage: ข้อความของ 507 insufficient_storage (Req 7.7)
  function insufficientStorageMessage(requiredBytes, availableBytes) {
    return 'พื้นที่ดิสก์ไม่พอ: ต้องการ ' + formatBytes(requiredBytes) + ' มี ' + formatBytes(availableBytes);
  }

  // ---------- ความคืบหน้า ----------

  function validProgress(v) {
    return typeof v === 'number' && Number.isFinite(v) && v >= 0;
  }

  // nextProgress คืนค่ามากสุดของความคืบหน้าปัจจุบันกับค่าใหม่ (offset จาก response, bytes จาก event) จึงไม่ลดลงเอง
  // ค่าที่ไม่ใช่จำนวนไม่ติดลบถูกข้าม การรีเซ็ตเป็น 0 (สร้าง session ใหม่หลัง 404) ผู้เรียกกำหนดค่าตรงๆ เอง
  function nextProgress(current, ...values) {
    let m = validProgress(current) ? current : 0;
    for (const v of values) {
      if (validProgress(v) && v > m) m = v;
    }
    return m;
  }

  // ---------- ลำดับในรายการถ่ายโอน ----------

  // transferRank จัดกลุ่มการถ่ายโอนเพื่อเรียง: 0 = กำลังทำงาน (อยู่บนสุด), 1 = รอคิว,
  // 2 = หยุด/ไม่สำเร็จ (ยังต้องการให้ผู้ใช้สนใจ), 3 = เสร็จแล้ว (คงไว้ท้ายรายการ)
  // state ที่ไม่รู้จักถือเป็นกลุ่ม 2 เพื่อไม่ให้แทรกขึ้นไปเหนือรายการที่กำลังทำงาน
  function transferRank(state) {
    switch (state) {
      case 'running': case 'reconnecting': case 'completing': return 0;
      case 'queued': return 1;
      case 'done': return 3;
      default: return 2;
    }
  }

  // compareTransfers: เรียงตามกลุ่มก่อน แล้วตาม order (ลำดับที่เพิ่มเข้ามา) จากน้อยไปมาก
  // a และ b เป็น {state, order}
  function compareTransfers(a, b) {
    return transferRank(a.state) - transferRank(b.state) || a.order - b.order;
  }

  // ---------- จับคู่ไฟล์ที่ค้าง ----------

  // matchPendingUpload: รับไฟล์ที่ผู้ใช้เลือกก็ต่อเมื่อ name และ size ตรงกับรายการที่เก็บไว้ (ไม่เทียบ lastModified)
  function matchPendingUpload(entry, file) {
    if (!entry || !file) return false;
    if (typeof entry.name !== 'string' || entry.name !== file.name) return false;
    return Number.isSafeInteger(entry.size) && entry.size === file.size;
  }

  // ---------- ป้ายความถูกต้องของไฟล์ ----------

  // integrityBadge คืน {kind, icon, text} หรือ null (unknown/ไม่มีค่า = ไม่มีป้าย ทั้งไอคอนและข้อความ)
  function integrityBadge(integrity) {
    if (integrity === 'verified') return { kind: 'verified', icon: '✓', text: 'ตรวจแล้ว' };
    if (integrity === 'unverified') return { kind: 'unverified', icon: '○', text: 'ยังไม่ได้ตรวจกับต้นทาง' };
    return null;
  }

  // ---------- การแจ้งเตือนไฟล์ใหม่ ----------

  function roleCanDownload(role) {
    return role === 'owner' || role === 'member' || role === 'viewer';
  }

  function hasId(pending, id) {
    if (!pending) return false;
    if (typeof pending.has === 'function') return pending.has(id);
    if (Array.isArray(pending)) return pending.includes(id);
    return false;
  }

  // isNotifiable: ไฟล์ id เป็น notifiable ก็ต่อเมื่ออยู่ใน pendingAdded, ปรากฏใน files, ไม่ใช่ของเรา (mine เท็จ)
  // และ role ดาวน์โหลดได้ (ไฟล์ที่ไม่ปรากฏในรายการถูกข้ามเงียบๆ)
  function isNotifiable(id, files, pendingAdded, role) {
    if (!roleCanDownload(role) || !hasId(pendingAdded, id) || !Array.isArray(files)) return false;
    const f = files.find((x) => x && x.id === id);
    return !!f && !f.mine;
  }

  // notifiableFiles คืนไฟล์ที่ notifiable ตามลำดับที่อยู่ใน pendingAdded (ลำดับที่ event มาถึง)
  function notifiableFiles(pendingAdded, files, role) {
    if (!pendingAdded || !Array.isArray(files) || !roleCanDownload(role)) return [];
    const out = [];
    for (const id of pendingAdded) {
      const f = files.find((x) => x && x.id === id);
      if (f && !f.mine) out.push(f);
    }
    return out;
  }

  // titleCount: คำนำหน้า document.title ตามจำนวนไฟล์ที่ยังไม่ได้ดู "(n) " และ "(99+) " เมื่อเกิน 99
  // n < 1 หรือไม่ใช่จำนวนเต็ม → '' (ไม่มีคำนำหน้า)
  function titleCount(n) {
    if (!Number.isInteger(n) || n < 1) return '';
    return n > TITLE_COUNT_MAX ? '(' + TITLE_COUNT_MAX + '+) ' : '(' + n + ') ';
  }

  function notifyMessage(files) {
    if (files.length === 1) {
      const f = files[0];
      return (f.uploaderName || 'อุปกรณ์อื่น') + ' ส่ง ' + (f.name || 'ไฟล์ใหม่');
    }
    // ไม่ใส่ชื่อไฟล์ในข้อความที่รวมกลุ่ม (Req 17.7)
    return files.length + ' ไฟล์ใหม่';
  }

  // addToNotifyGroup เพิ่มไฟล์ที่ notifiable (ที่เวลา atMs) เข้ากลุ่มปัจจุบัน คืนกลุ่มใหม่โดยไม่แก้ของเดิม
  //   กลุ่ม = {startAt, count, files, message}
  //   ไฟล์ที่มาถึงภายใน windowMs (ค่าเริ่มต้น 5 วินาที, เทียบแบบ < ) นับจากไฟล์แรกของกลุ่มอยู่กลุ่มเดียวกัน
  //   ไฟล์ที่มาถึงเมื่อครบหน้าต่างแล้วเริ่มกลุ่มใหม่ message ของกลุ่มที่มีหลายไฟล์คือ "{N} ไฟล์ใหม่"
  //   ผลคืน {group, isNew} isNew เป็นจริงเมื่อเริ่มกลุ่มใหม่
  function addToNotifyGroup(group, file, atMs, windowMs) {
    const win = windowMs === undefined ? NOTIFY_WINDOW_MS : windowMs;
    if (group && atMs - group.startAt < win) {
      const files = group.files.concat([file]);
      return { group: { startAt: group.startAt, count: files.length, files, message: notifyMessage(files) }, isNew: false };
    }
    const files = [file];
    return { group: { startAt: atMs, count: 1, files, message: notifyMessage(files) }, isNew: true };
  }

  // groupNotifications รวมรายการ [{at, file}] (at = เวลา ms) ที่ notifiable เป็นกลุ่มตามหน้าต่าง 5 วินาที
  // เรียงตามเวลาก่อน (คงลำดับเดิมเมื่อเวลาเท่ากัน) คืนอาร์เรย์ของกลุ่ม
  function groupNotifications(items, windowMs) {
    if (!Array.isArray(items)) return [];
    const sorted = items
      .map((it, idx) => ({ it, idx }))
      .sort((a, b) => (a.it.at - b.it.at) || (a.idx - b.idx))
      .map((x) => x.it);
    const groups = [];
    let cur = null;
    for (const it of sorted) {
      const r = addToNotifyGroup(cur, it.file, it.at, windowMs);
      if (r.isNew && cur) groups.push(cur);
      cur = r.group;
    }
    if (cur) groups.push(cur);
    return groups;
  }

  // ---------- หน้าตรวจปัญหาการเชื่อมต่อ ----------

  const DIAG_STATUS = Object.freeze({
    ok: { icon: '✓', text: 'ปกติ' },
    warn: { icon: '⚠', text: 'ควรตรวจสอบ' },
    fail: { icon: '✕', text: 'ผิดปกติ' },
    unknown: { icon: '?', text: 'ไม่ทราบ' },
  });

  // diagnosticStatusInfo: status ที่ไม่รู้จัก (รวมค่าที่ไม่ใช่สตริง) ถือเป็น unknown → {status, icon, text}
  function diagnosticStatusInfo(status) {
    const key = typeof status === 'string' && Object.prototype.hasOwnProperty.call(DIAG_STATUS, status) ? status : 'unknown';
    return { status: key, icon: DIAG_STATUS[key].icon, text: DIAG_STATUS[key].text };
  }

  function scrub(text, secrets) {
    let s = text;
    if (Array.isArray(secrets)) {
      for (const sec of secrets) {
        if (typeof sec === 'string' && sec !== '') s = s.split(sec).join('[ซ่อน]');
      }
    }
    return s;
  }

  function textField(v, secrets) {
    if (typeof v !== 'string') return '';
    const t = scrub(v, secrets).trim();
    return t;
  }

  // buildDiagnosticRows แปลง checks จาก GET /api/admin/diagnostics เป็นแถวสำหรับแสดงผล
  // จำนวนแถวเท่ากับจำนวนรายการ ลำดับเดิม ทุกแถวมี icon และ statusText; detail/hint ที่ว่างถูกละ (ไม่มีคีย์)
  //   opts.secrets: สตริงลับ (PIN, token) ที่ต้องไม่ปรากฏในแถวหรือรายงาน จะถูกแทนด้วย "[ซ่อน]"
  // แถว = {id, title, status, icon, statusText, detail?, hint?}
  function buildDiagnosticRows(results, opts) {
    if (!Array.isArray(results)) return [];
    const secrets = opts && opts.secrets;
    return results.map((r) => {
      const item = r && typeof r === 'object' ? r : {};
      const info = diagnosticStatusInfo(item.status);
      const id = typeof item.id === 'string' ? item.id : '';
      const row = {
        id,
        title: textField(item.title, secrets) || textField(id, secrets),
        status: info.status,
        icon: info.icon,
        statusText: info.text,
      };
      const detail = textField(item.detail, secrets);
      const hint = textField(item.hint, secrets);
      if (detail) row.detail = detail;
      if (hint) row.hint = hint;
      return row;
    });
  }

  // buildReport สร้างรายงานข้อความล้วนจากแถวที่แสดง (title, สถานะ, detail, hint และเวลาที่ตรวจ)
  //   opts.generatedAt: เวลาที่ตรวจ (สตริงจากเซิร์ฟเวอร์ หรือ Date) ไม่ส่ง = ไม่มีบรรทัดเวลา
  //   opts.secrets: เหมือน buildDiagnosticRows
  // ไม่ใส่ฟิลด์อื่นของ Check_Result (เช่น reason) จึงไม่มีข้อมูลลับนอกจากที่อยู่ในข้อความที่แสดง
  function buildReport(results, opts) {
    const rows = buildDiagnosticRows(results, opts);
    const lines = ['รายงานตรวจปัญหาการเชื่อมต่อ LAN Share'];
    const at = opts ? opts.generatedAt : undefined;
    if (at instanceof Date && !Number.isNaN(at.getTime())) lines.push('เวลาที่ตรวจ: ' + at.toISOString());
    else if (typeof at === 'string' && at !== '') lines.push('เวลาที่ตรวจ: ' + scrub(at, opts && opts.secrets));
    for (const row of rows) {
      lines.push('');
      lines.push('[' + row.statusText + '] ' + row.title);
      if (row.detail) lines.push('  รายละเอียด: ' + row.detail);
      if (row.hint) lines.push('  คำแนะนำ: ' + row.hint);
    }
    return lines.join('\n') + '\n';
  }

  // ---------- ค้นหาและจัดกลุ่มไฟล์ ----------

  function filePathOf(f) {
    return ((f && f.dir) ? f.dir + '/' : '') + (f && f.name ? f.name : '');
  }

  function fileTime(f) {
    const t = Date.parse(f && f.createdAt);
    return Number.isNaN(t) ? 0 : t;
  }

  function fileDayKey(f) {
    const d = new Date(f && f.createdAt);
    if (Number.isNaN(d.getTime())) return '';
    const y = d.getFullYear();
    const m = String(d.getMonth() + 1).padStart(2, '0');
    const day = String(d.getDate()).padStart(2, '0');
    return y + '-' + m + '-' + day;
  }

  // filterAndSortFiles กรองด้วยคำค้น (ชื่อไฟล์ โฟลเดอร์ ชื่ออุปกรณ์) แล้วเรียง
  // sort: newest | device | date — ไม่แตะอาร์เรย์ต้นทาง
  function filterAndSortFiles(files, query, sort) {
    const src = Array.isArray(files) ? files : [];
    const q = String(query || '').trim().toLowerCase();
    let list = src.slice();
    if (q) {
      list = list.filter((f) => {
        const hay = (filePathOf(f) + ' ' + (f && f.uploaderName ? f.uploaderName : '')).toLowerCase();
        return hay.indexOf(q) !== -1;
      });
    }
    if (sort === 'device') {
      list.sort((a, b) => String(a.uploaderName || '').localeCompare(String(b.uploaderName || '')) || fileTime(b) - fileTime(a));
    } else if (sort === 'date') {
      list.sort((a, b) => fileDayKey(b).localeCompare(fileDayKey(a)) || fileTime(b) - fileTime(a));
    } else {
      list.sort((a, b) => fileTime(b) - fileTime(a));
    }
    return list;
  }

  function fileGroupKey(f, sort) {
    if (sort === 'device') return (f && f.uploaderName) ? f.uploaderName : '';
    if (sort === 'date') return fileDayKey(f);
    return '';
  }

  const CODE_KEYWORDS = {
    go: 'func package import return if else for range struct interface type const var map chan defer go select case switch break continue default',
    js: 'function const let var return if else for of in while class async await import export default new this try catch finally throw',
    ts: 'function const let var return if else for of in while class async await import export default new this try catch finally throw type interface',
    py: 'def class return if elif else for while import from as try except finally with yield lambda pass break continue True False None',
    json: 'true false null',
  };

  function keywordsFor(ext) {
    const e = String(ext || '').toLowerCase();
    if (e === 'ts') return CODE_KEYWORDS.ts;
    if (e === 'py') return CODE_KEYWORDS.py;
    if (e === 'go') return CODE_KEYWORDS.go;
    if (e === 'json') return CODE_KEYWORDS.json;
    return CODE_KEYWORDS.js;
  }

  // highlightCode แยก token ของข้อความโค้ดโดยไม่ใช้ HTML — ผู้เรียกใส่ textContent
  function highlightCode(src, ext) {
    const text = String(src || '');
    const kw = new Set(keywordsFor(ext).split(' ').filter(Boolean));
    const re = /("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|\/\/[^\n]*|\/\*[\s\S]*?\*\/|#(?!\{)[^\n]*|\b\d+(?:\.\d+)?\b|[A-Za-z_][\w]*)/g;
    const out = [];
    let last = 0;
    let m;
    while ((m = re.exec(text)) !== null) {
      if (m.index > last) out.push({ text: text.slice(last, m.index) });
      const tok = m[0];
      let cls = '';
      if (tok.startsWith('//') || tok.startsWith('/*') || tok.startsWith('#')) cls = 'cm';
      else if (tok[0] === '"' || tok[0] === "'" || tok[0] === '`') cls = 'st';
      else if (/^\d/.test(tok)) cls = 'nu';
      else if (kw.has(tok)) cls = 'kw';
      out.push(cls ? { cls, text: tok } : { text: tok });
      last = m.index + tok.length;
    }
    if (last < text.length) out.push({ text: text.slice(last) });
    return out;
  }

  return {
    // ค่าคงที่
    CHUNK_SIZE,
    MAX_CHUNK_SIZE,
    RETRY_WAITS_SEC,
    NO_PROGRESS_TIMEOUT_MS,
    PAUSE_AFTER_MS,
    CONFLICT_STREAK_LIMIT,
    NOT_FOUND_STREAK_LIMIT,
    NOTIFY_WINDOW_MS,
    ACTION,
    // retry
    retryWaitSec,
    createRetryScheduler,
    createStreakCounter,
    // ก้อน
    nextChunk,
    planChunks,
    // จัดประเภทผลลัพธ์
    classifyResult,
    notFoundAction,
    isChecksumMismatch,
    // แสดงผล
    formatBytes,
    insufficientStorageMessage,
    nextProgress,
    transferRank,
    compareTransfers,
    matchPendingUpload,
    integrityBadge,
    // ค้นหาและจัดกลุ่มไฟล์
    filterAndSortFiles,
    fileGroupKey,
    highlightCode,
    // แจ้งเตือน
    roleCanDownload,
    isNotifiable,
    notifiableFiles,
    titleCount,
    addToNotifyGroup,
    groupNotifications,
    // diagnostics
    diagnosticStatusInfo,
    buildDiagnosticRows,
    buildReport,
  };
});
