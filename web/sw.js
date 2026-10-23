'use strict';

// Service Worker ของ Share Group (ทำงานเฉพาะ Secure_Context)
//
// นโยบาย: เก็บ cache เฉพาะไฟล์ App_Shell ใน ALLOW ที่ขอด้วย GET จาก origin
// เดียวกัน แบบ network-first และใช้ cache เมื่อ fetch ล้มด้วย network error
// เท่านั้น ทุกอย่างอื่น (/api/*, /ws, /sw.js, ดาวน์โหลด/อัปโหลด, พาธนอก
// allow-list) ไม่เรียก respondWith เลย เบราว์เซอร์จึงส่งตรงไปเครือข่ายเองโดย
// ไม่ผ่าน cache ไม่มี token หรือข้อมูลของห้องถูกเก็บ

const CACHE_PREFIX = 'lanshare-shell-';
// เซิร์ฟเวอร์แทน placeholder ด้วย 12 ตัวแรกของ SHA-256 ของไฟล์ใน ALLOW
// ชื่อ cache จึงเปลี่ยนเมื่อไฟล์ใดเปลี่ยน (ต้องมี placeholder นี้แห่งเดียวในไฟล์)
const CACHE_NAME = CACHE_PREFIX + '__SHELL_VERSION__';

const ALLOW = [
  '/',
  '/index.html',
  '/style.css',
  '/app.js',
  '/sha256.js',
  '/hash-core.js',
  '/hash-worker.js',
  '/logic.js',
  '/manifest.webmanifest',
  '/icons/icon-192.png',
  '/icons/icon-512.png',
  '/icons/icon-maskable-512.png',
  '/icons/apple-touch-icon.png',
];

// isCacheable เป็นจริงก็ต่อเมื่อ method เป็น GET, origin เดียวกัน และ pathname
// ตรงกับรายการใน ALLOW ตรงตัว (ไม่สนใจ query string)
function isCacheable(url, method, origin) {
  if (method !== 'GET') return false;
  let u;
  try {
    u = new URL(url, origin);
  } catch (e) {
    return false;
  }
  if (u.origin !== new URL(origin).origin) return false;
  return ALLOW.indexOf(u.pathname) !== -1;
}

async function networkFirst(request) {
  const key = new URL(new URL(request.url).pathname, self.location.origin).href;
  let response;
  try {
    response = await fetch(request);
  } catch (e) {
    // network error เท่านั้นที่ใช้ cache: เซิร์ฟเวอร์ที่ตอบได้ต้องไม่ได้ไฟล์เก่า
    const cached = await caches.match(key, { cacheName: CACHE_NAME });
    if (cached) return cached;
    throw e;
  }
  // 200 เท่านั้น และไม่เก็บ response ที่ผ่าน redirect (Safari ปฏิเสธเมื่อนำไปตอบ navigation)
  if (response.status === 200 && !response.redirected) {
    const copy = response.clone();
    caches.open(CACHE_NAME).then((c) => c.put(key, copy)).catch(() => {});
  }
  return response;
}

if (typeof self !== 'undefined' && typeof self.addEventListener === 'function') {
  self.addEventListener('install', () => {
    self.skipWaiting();
  });

  self.addEventListener('activate', (event) => {
    event.waitUntil(
      caches.keys()
        .then((names) => Promise.all(names
          .filter((n) => n.startsWith(CACHE_PREFIX) && n !== CACHE_NAME)
          .map((n) => caches.delete(n))))
        .then(() => self.clients.claim())
    );
  });

  self.addEventListener('fetch', (event) => {
    const req = event.request;
    if (!isCacheable(req.url, req.method, self.location.origin)) return;
    event.respondWith(networkFirst(req));
  });
}

if (typeof module !== 'undefined') module.exports = { isCacheable, ALLOW, CACHE_PREFIX };
