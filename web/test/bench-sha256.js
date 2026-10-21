'use strict';

// หน้าวัดความเร็ว SHA-256 บนมือถือ ใช้ชั่วคราวผ่าน `go run web/test/bench/main.go` เท่านั้น
// ผลลัพธ์ทุกอย่างใส่ด้วย textContent/value ไม่ใช้ innerHTML

const $ = (id) => document.getElementById(id);

// ใช้หน่วยทศนิยม (1 MB = 1,000,000 ไบต์) เพื่อเทียบกับความเร็ว Wi-Fi (Mbit/s) ได้ตรงๆ
const BENCH_SIZES = [
  { label: '100 MB', bytes: 100 * 1000 * 1000 },
  { label: '1 GB', bytes: 1000 * 1000 * 1000 },
];

let benchWorker = null;
let benchBusy = false;
let benchNextId = 1;
const benchLines = [];

function benchSetStatus(text) {
  $('status').textContent = text;
}

function benchRenderResults() {
  $('results').value = benchLines.join('\n');
}

function benchHeader() {
  const nav = navigator;
  return [
    'SHA-256 bench (hashBlob ใน Web Worker, slice 4 MiB)',
    'วันที่: ' + new Date().toISOString(),
    'เบราว์เซอร์: ' + nav.userAgent,
    'CPU threads: ' + (nav.hardwareConcurrency || '?') + ', RAM (ประมาณ): ' + (nav.deviceMemory ? nav.deviceMemory + ' GB' : '?'),
  ];
}

function benchRunOne(spec) {
  return new Promise(function (resolve, reject) {
    const id = benchNextId++;
    benchWorker.onmessage = function (ev) {
      const m = ev.data || {};
      if (m.id !== id) return;
      if (m.error) {
        reject(new Error(m.error));
      } else if (m.result) {
        resolve(m.result);
      } else {
        const pct = m.total ? Math.floor((m.done / m.total) * 100) : 0;
        benchSetStatus('กำลังวัด ' + spec.label + ': ' + pct + '%');
      }
    };
    benchWorker.onerror = function (ev) {
      reject(new Error(ev.message || 'worker error'));
    };
    benchWorker.postMessage({ id: id, size: spec.bytes });
  });
}

function benchFormat(spec, r) {
  const sec = r.elapsedMs / 1000;
  const mbps = r.bytes / 1e6 / sec;
  return [
    spec.label + ': ' + mbps.toFixed(1) + ' MB/s (' + (mbps * 8).toFixed(0) + ' Mbit/s)' +
      ' ใช้เวลา ' + sec.toFixed(2) + ' วินาที, สร้าง slice ' + (r.genMs / 1000).toFixed(2) + ' วินาที (รวมอยู่ในเวลาแล้ว)',
    '  digest: ' + r.hex,
  ];
}

async function benchRunSpecs(specs) {
  if (benchBusy) return;
  benchBusy = true;
  for (const b of $$('button.run')) b.disabled = true;
  try {
    if (benchWorker) benchWorker.terminate();
    benchWorker = new Worker('bench-worker.js');
  } catch (err) {
    benchSetStatus('สร้าง Web Worker ไม่ได้: ' + String((err && err.message) || err));
    benchBusy = false;
    for (const b of $$('button.run')) b.disabled = false;
    return;
  }
  benchLines.length = 0;
  benchLines.push(...benchHeader());
  benchRenderResults();
  try {
    for (const spec of specs) {
      benchSetStatus('กำลังวัด ' + spec.label + ': 0%');
      const r = await benchRunOne(spec);
      benchLines.push(...benchFormat(spec, r));
      benchRenderResults();
    }
    benchSetStatus('วัดเสร็จแล้ว คัดลอกผลด้านล่างไปกรอกใน docs/project-scope.md');
  } catch (err) {
    benchLines.push('ผิดพลาด: ' + String((err && err.message) || err));
    benchRenderResults();
    benchSetStatus('วัดไม่สำเร็จ: ' + String((err && err.message) || err));
  } finally {
    if (benchWorker) benchWorker.terminate();
    benchWorker = null;
    benchBusy = false;
    for (const b of $$('button.run')) b.disabled = false;
  }
}

// หน้านี้เสิร์ฟผ่าน HTTP จึงไม่ใช่ Secure_Context navigator.clipboard มักใช้ไม่ได้
// ให้เลือกข้อความทั้งหมดไว้ให้ผู้ใช้คัดลอกเองเป็นทางสำรอง
async function benchCopy() {
  const area = $('results');
  area.focus();
  area.select();
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(area.value);
      benchSetStatus('คัดลอกผลแล้ว');
      return;
    }
  } catch (err) {
    // ตกไปใช้วิธีสำรองด้านล่าง
  }
  let ok = false;
  try {
    ok = document.execCommand('copy');
  } catch (err) {
    ok = false;
  }
  benchSetStatus(ok ? 'คัดลอกผลแล้ว' : 'คัดลอกอัตโนมัติไม่ได้ เลือกข้อความไว้แล้ว ให้กดคัดลอกเอง');
}

function $$(sel) {
  return Array.prototype.slice.call(document.querySelectorAll(sel));
}

$('run-100').addEventListener('click', function () { benchRunSpecs([BENCH_SIZES[0]]); });
$('run-1g').addEventListener('click', function () { benchRunSpecs([BENCH_SIZES[1]]); });
$('run-all').addEventListener('click', function () { benchRunSpecs(BENCH_SIZES); });
$('copy').addEventListener('click', benchCopy);
