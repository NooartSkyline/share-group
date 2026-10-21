'use strict';

// ตรรกะแฮชไฟล์แบบเพิ่มทีละส่วน แยกจาก Worker เพื่อให้ทดสอบใน Node ได้
//
// Invariant: ถือข้อมูลไฟล์ในหน่วยความจำไม่เกินหนึ่ง slice (ค่าเริ่มต้น 4 MiB) ต่อครั้ง
// และผลลัพธ์เป็น hex ตัวพิมพ์เล็ก 64 ตัว

const HASH_DEFAULT_SLICE = 4 * 1024 * 1024;
// ป้อนข้อมูลเข้า Sha256 ทีละก้อนเล็กเพื่อให้รายงานความคืบหน้าและตรวจการยกเลิกได้ถี่
const HASH_STEP = 256 * 1024;

const HashSha256 = (typeof module !== 'undefined' && module.exports)
  ? require('./sha256.js').Sha256
  : Sha256;

function hashCancelledError() {
  const err = new Error('hash cancelled');
  err.cancelled = true;
  return err;
}

// hashBlob คืน SHA-256 (hex ตัวพิมพ์เล็ก) ของ blob ช่วง [start, end)
//   opts.sliceSize    ขนาด slice ที่อ่านต่อครั้ง (ค่าเริ่มต้น 4 MiB)
//   opts.onProgress   (done, total) จำนวนไบต์ที่แฮชแล้วในช่วงนี้ เรียกหลังป้อนข้อมูลแต่ละก้อนย่อย
//   opts.isCancelled  () => boolean ถ้าเป็นจริงจะ throw error ที่มี cancelled = true
// อ่านไม่ครบ (ไฟล์ถูกลบ/ถูกแก้ไขระหว่างอ่าน) จะ throw Error โดยไม่คืนค่าบางส่วน
async function hashBlob(blob, opts) {
  const o = opts || {};
  const size = blob.size;
  const start = o.start === undefined ? 0 : o.start;
  const end = o.end === undefined ? size : o.end;
  const sliceSize = o.sliceSize === undefined ? HASH_DEFAULT_SLICE : o.sliceSize;
  if (!Number.isSafeInteger(start) || !Number.isSafeInteger(end) || start < 0 || end < start || end > size) {
    throw new RangeError('invalid hash range');
  }
  if (!Number.isSafeInteger(sliceSize) || sliceSize < 1) {
    throw new RangeError('invalid slice size');
  }
  const total = end - start;
  const hasher = new HashSha256();
  let done = 0;
  while (done < total) {
    if (o.isCancelled && o.isCancelled()) throw hashCancelledError();
    const a = start + done;
    const b = Math.min(a + sliceSize, end);
    const buf = await blob.slice(a, b).arrayBuffer();
    if (buf.byteLength !== b - a) {
      throw new Error('short read: expected ' + (b - a) + ' bytes, got ' + buf.byteLength);
    }
    const bytes = new Uint8Array(buf);
    for (let p = 0; p < bytes.length; p += HASH_STEP) {
      if (o.isCancelled && o.isCancelled()) throw hashCancelledError();
      const part = bytes.subarray(p, Math.min(p + HASH_STEP, bytes.length));
      hasher.update(part);
      done += part.length;
      if (o.onProgress) o.onProgress(done, total);
    }
  }
  if (total === 0 && o.onProgress) o.onProgress(0, 0);
  return hasher.digestHex();
}

// compareChecksum เทียบ checksum แบบไม่สนตัวพิมพ์ใหญ่-เล็ก ค่าที่ไม่ใช่สตริงถือว่าไม่เท่ากัน
function compareChecksum(a, b) {
  if (typeof a !== 'string' || typeof b !== 'string') return false;
  return a.toLowerCase() === b.toLowerCase();
}

if (typeof module !== 'undefined') module.exports = { hashBlob, compareChecksum };
