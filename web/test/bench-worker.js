'use strict';

// Worker ของหน้าวัดความเร็ว SHA-256 (bench-sha256.html) ใช้ชั่วคราวระหว่างวัดเท่านั้น ไม่ฝังใน binary
//
// เรียก hashBlob ตัวจริงจาก hash-core.js กับ "ไฟล์" ที่สร้างขึ้นทีละ slice ในหน่วยความจำ
// จึงไม่ต้องถือทั้งไฟล์ (1 GB) ไว้ ใช้เทมเพลตรอบ 256 ไบต์ขนาดคงที่ตัวเดียวแล้วคัดลอกเป็น slice
// ความเร็ว SHA-256 ไม่ขึ้นกับเนื้อหา แต่เนื้อหาเป็นแบบกำหนดได้เพื่อให้ตรวจ digest เทียบกับ Node ได้
//
// ข้อความเข้า:  {id, size}
// ข้อความออก:   {id, done, total} ความคืบหน้า (ห่าง ~200 ms)
//               {id, result: {bytes, elapsedMs, genMs, hex}} หรือ {id, error}

const BENCH_PERIOD = 256;
// ต้องไม่น้อยกว่าขนาด slice ของ hashBlob (4 MiB) + BENCH_PERIOD
const BENCH_TEMPLATE_SIZE = 4 * 1024 * 1024 + BENCH_PERIOD;
const BENCH_PROGRESS_MS = 200;

function benchByteAt(pos) {
  return (pos * 31 + 7) & 0xff;
}

function benchMakeTemplate() {
  const t = new Uint8Array(BENCH_TEMPLATE_SIZE);
  for (let i = 0; i < t.length; i++) t[i] = benchByteAt(i);
  return t;
}

// SyntheticBlob มีเฉพาะ size และ slice(a, b).arrayBuffer() ซึ่งเป็นส่วนที่ hashBlob ใช้
// ไบต์ที่ตำแหน่ง p เท่ากับ benchByteAt(p) เสมอ ไม่ว่า slice จะเริ่มที่ใด
// timing.genMs สะสมเวลาที่ใช้สร้าง slice เพื่อรายงานแยกจากเวลาแฮช
class SyntheticBlob {
  constructor(size, template, timing) {
    this.size = size;
    this._template = template;
    this._timing = timing;
  }

  slice(a, b) {
    const self = this;
    return {
      arrayBuffer: async function () {
        const t0 = benchNow();
        const off = a % BENCH_PERIOD;
        const len = b - a;
        if (off + len > self._template.length) throw new RangeError('slice too large');
        const buf = self._template.buffer.slice(off, off + len);
        self._timing.genMs += benchNow() - t0;
        return buf;
      },
    };
  }
}

function benchNow() {
  return (typeof performance !== 'undefined' ? performance : Date).now();
}

async function benchRun(size, onProgress) {
  const timing = { genMs: 0 };
  const blob = new SyntheticBlob(size, benchMakeTemplate(), timing);
  const t0 = benchNow();
  const hex = await hashBlob(blob, { onProgress: onProgress });
  const elapsedMs = benchNow() - t0;
  return { bytes: size, elapsedMs: elapsedMs, genMs: timing.genMs, hex: hex };
}

if (typeof importScripts === 'function') {
  importScripts('/sha256.js', '/hash-core.js');

  self.onmessage = async function (ev) {
    const msg = ev.data || {};
    let last = 0;
    try {
      const result = await benchRun(msg.size, function (done, total) {
        const now = Date.now();
        if (now - last >= BENCH_PROGRESS_MS) {
          last = now;
          self.postMessage({ id: msg.id, done: done, total: total });
        }
      });
      self.postMessage({ id: msg.id, result: result });
    } catch (err) {
      self.postMessage({ id: msg.id, error: String((err && err.message) || err) });
    }
  };
}

if (typeof module !== 'undefined') module.exports = { SyntheticBlob, benchByteAt, benchRun, BENCH_TEMPLATE_SIZE };
