'use strict';

// SHA-256 แบบ JavaScript ล้วน ไม่พึ่ง crypto.subtle (ใช้ไม่ได้บน HTTP ธรรมดา)
// และไม่ใช้ WebAssembly (ติด CSP) โหลดได้ทั้งใน Web Worker และ Node
//
// Invariant: ผลลัพธ์ของ digestHex() เป็น hex ตัวพิมพ์เล็ก 64 ตัวเสมอ และเรียกซ้ำได้
// โดยไม่กระทบสถานะ จึง update() ต่อหลังเรียก digestHex() ได้

const SHA256_K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

const SHA256_INIT = new Uint32Array([
  0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
]);

// ประมวลผลบล็อก 64 ไบต์ที่ data[off..off+64) เข้า state h โดยใช้ w เป็นพื้นที่ทำงาน
function sha256Block(h, w, data, off) {
  for (let i = 0; i < 16; i++, off += 4) {
    w[i] = ((data[off] << 24) | (data[off + 1] << 16) | (data[off + 2] << 8) | data[off + 3]) >>> 0;
  }
  for (let i = 16; i < 64; i++) {
    const x = w[i - 15];
    const y = w[i - 2];
    const s0 = ((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3);
    const s1 = ((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10);
    w[i] = (w[i - 16] + s0 + w[i - 7] + s1) >>> 0;
  }
  let a = h[0], b = h[1], c = h[2], d = h[3], e = h[4], f = h[5], g = h[6], hh = h[7];
  for (let i = 0; i < 64; i++) {
    const S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7));
    const ch = (e & f) ^ (~e & g);
    const t1 = (hh + S1 + ch + SHA256_K[i] + w[i]) >>> 0;
    const S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10));
    const maj = (a & b) ^ (a & c) ^ (b & c);
    const t2 = (S0 + maj) >>> 0;
    hh = g; g = f; f = e;
    e = (d + t1) >>> 0;
    d = c; c = b; b = a;
    a = (t1 + t2) >>> 0;
  }
  h[0] = (h[0] + a) >>> 0; h[1] = (h[1] + b) >>> 0; h[2] = (h[2] + c) >>> 0; h[3] = (h[3] + d) >>> 0;
  h[4] = (h[4] + e) >>> 0; h[5] = (h[5] + f) >>> 0; h[6] = (h[6] + g) >>> 0; h[7] = (h[7] + hh) >>> 0;
}

class Sha256 {
  constructor() {
    this._h = new Uint32Array(SHA256_INIT);
    this._w = new Uint32Array(64);
    this._buf = new Uint8Array(64); // ไบต์ที่ยังไม่ครบบล็อก
    this._bufLen = 0;
    this._total = 0; // จำนวนไบต์ที่รับแล้ว (Number ปลอดภัยถึง 2^53)
  }

  // update รับ Uint8Array เท่านั้น ไม่เก็บอ้างอิงถึง data หลังคืนค่า
  update(data) {
    if (!(data instanceof Uint8Array)) {
      throw new TypeError('Sha256.update expects a Uint8Array');
    }
    let off = 0;
    const len = data.length;
    this._total += len;
    if (this._bufLen > 0) {
      const take = Math.min(64 - this._bufLen, len);
      this._buf.set(data.subarray(0, take), this._bufLen);
      this._bufLen += take;
      off = take;
      if (this._bufLen < 64) return this;
      sha256Block(this._h, this._w, this._buf, 0);
      this._bufLen = 0;
    }
    // ประมวลผลบล็อกเต็มจาก data โดยตรง ไม่คัดลอก
    for (; off + 64 <= len; off += 64) {
      sha256Block(this._h, this._w, data, off);
    }
    if (off < len) {
      this._buf.set(data.subarray(off), 0);
      this._bufLen = len - off;
    }
    return this;
  }

  // digestHex คืน hex ตัวพิมพ์เล็ก 64 ตัวโดยไม่เปลี่ยนสถานะภายใน
  digestHex() {
    const h = new Uint32Array(this._h);
    const tail = new Uint8Array(128);
    tail.set(this._buf.subarray(0, this._bufLen));
    tail[this._bufLen] = 0x80;
    const tailLen = this._bufLen < 56 ? 64 : 128;
    // ความยาวเป็นบิต 64 บิต: hi = floor(total*8 / 2^32), lo = (total*8) mod 2^32
    const hi = Math.floor(this._total / 0x20000000);
    const lo = ((this._total % 0x20000000) * 8) >>> 0;
    tail[tailLen - 8] = hi >>> 24; tail[tailLen - 7] = hi >>> 16;
    tail[tailLen - 6] = hi >>> 8; tail[tailLen - 5] = hi;
    tail[tailLen - 4] = lo >>> 24; tail[tailLen - 3] = lo >>> 16;
    tail[tailLen - 2] = lo >>> 8; tail[tailLen - 1] = lo;
    const w = new Uint32Array(64);
    for (let off = 0; off < tailLen; off += 64) sha256Block(h, w, tail, off);
    let hex = '';
    for (let i = 0; i < 8; i++) hex += h[i].toString(16).padStart(8, '0');
    return hex;
  }
}

if (typeof module !== 'undefined') module.exports = { Sha256 };
