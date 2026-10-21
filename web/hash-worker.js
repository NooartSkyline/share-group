'use strict';

// Classic Web Worker สำหรับแฮชไฟล์โดยไม่บล็อก main thread
//
// ข้อความเข้า:  {id, file, start, end}  (start/end เว้นได้ = ทั้งไฟล์), {id, cancel: true}
// ข้อความออก:   {id, done, total} ความคืบหน้า ส่งห่างไม่เกิน ~250 ms ระหว่างที่แฮช
//               {id, hex} เมื่อสำเร็จ หรือ {id, error} เมื่ออ่านไฟล์ไม่ได้/ผิดพลาด
// Worker แต่ละตัวอาจรับหลายงาน แยกด้วย id; งานที่ถูกยกเลิกจะไม่ส่ง hex หรือ error

importScripts('/sha256.js', '/hash-core.js');

// ส่งความคืบหน้าอย่างน้อยทุก 250 ms: ป้อนข้อมูลทีละ ≤ 256 KiB จึงส่งเมื่อพ้น 100 ms เพื่อเหลือช่องว่าง
const HASH_PROGRESS_INTERVAL_MS = 100;
const hashCancelledIds = new Set();

self.onmessage = async function (ev) {
  const msg = ev.data || {};
  const id = msg.id;
  if (msg.cancel) {
    hashCancelledIds.add(id);
    return;
  }
  let lastPost = 0;
  try {
    const hex = await hashBlob(msg.file, {
      start: msg.start,
      end: msg.end,
      isCancelled: function () { return hashCancelledIds.has(id); },
      onProgress: function (done, total) {
        const now = Date.now();
        if (done === total || now - lastPost >= HASH_PROGRESS_INTERVAL_MS) {
          lastPost = now;
          self.postMessage({ id: id, done: done, total: total });
        }
      },
    });
    self.postMessage({ id: id, hex: hex });
  } catch (err) {
    if (!(err && err.cancelled)) {
      self.postMessage({ id: id, error: String((err && err.message) || err) });
    }
  } finally {
    hashCancelledIds.delete(id);
  }
};

if (typeof module !== 'undefined') module.exports = {};
