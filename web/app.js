'use strict';

// All user-controlled text (file names, device names, messages) is inserted with
// textContent / text nodes, never innerHTML. Links are built as anchor elements
// and only for http(s) URLs.

const $ = (id) => document.getElementById(id);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
const MAX_PARALLEL_UPLOADS = 2;
const MAX_DROPPED_FILES = 2000;
const JUNK_FILES = new Set(['.DS_Store', 'Thumbs.db', 'desktop.ini']);

const state = {
  me: null,
  room: null,
  ws: null,
  wsRetry: 0,
  devices: [],
  files: [],
  selected: new Set(),
  uploads: [], // local uploads (XHR gives accurate progress)
  remote: new Map(), // transfers of other devices, from WebSocket events
  messages: [],
  clip: null,
  view: 'files',
  unread: 0,
};

// ---------- helpers ----------

function el(tag, props, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (k === 'class') node.className = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else node[k] = v;
  }
  for (const c of children) {
    if (c == null) continue;
    node.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return node;
}

async function api(path, opts = {}) {
  const res = await fetch(path, {
    credentials: 'same-origin',
    ...opts,
    headers: { 'X-LanShare-Client': '1', ...(opts.headers || {}) },
  });
  let body = null;
  if ((res.headers.get('content-type') || '').includes('application/json')) {
    body = await res.json().catch(() => null);
  }
  if (!res.ok) {
    const err = new Error((body && body.error) || res.statusText);
    err.status = res.status;
    err.body = body;
    throw err;
  }
  return body;
}

const jsonBody = (method, obj) => ({
  method,
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(obj),
});

function formatBytes(n) {
  if (n == null || n < 0) return '-';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return (i === 0 ? n.toFixed(0) : n.toFixed(n >= 100 ? 0 : 1)) + ' ' + units[i];
}

function formatTime(iso) {
  try {
    // e.g. "20 ต.ค. 18:47" (no year: the Thai locale would print the Buddhist year as "69")
    return new Intl.DateTimeFormat('th-TH', {
      day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', hour12: false,
    }).format(new Date(iso));
  } catch { return iso; }
}

const KIND_ICON = { image: '🖼️', video: '🎬', audio: '🎵', document: '📄', archive: '📦', other: '📁' };
const PLATFORM_ICON = { iOS: '📱', Android: '🤖', Windows: '🖥️', macOS: '💻', Linux: '🐧', Web: '🌐' };
const ROLE_LABEL = { owner: 'Owner', member: 'Member', viewer: 'Viewer', uploader: 'Uploader' };

function guessPlatform() {
  const ua = navigator.userAgent;
  if (/iPhone|iPad|iPod/.test(ua)) return 'iOS';
  if (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1) return 'iOS';
  if (/Android/.test(ua)) return 'Android';
  if (/Windows/.test(ua)) return 'Windows';
  if (/Macintosh/.test(ua)) return 'macOS';
  if (/Linux/.test(ua)) return 'Linux';
  return 'Web';
}

const isLocalhost = () => ['localhost', '127.0.0.1', '[::1]'].includes(location.hostname);
const isDesktopLayout = () => window.matchMedia('(min-width: 821px)').matches;

let toastTimer;
function toast(msg) {
  const t = $('toast');
  t.textContent = msg;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, 3500);
}

function show(id) {
  for (const v of ['loading', 'join', 'app']) $(v).hidden = v !== id;
  $('tabbar').hidden = id !== 'app';
}

function debounce(fn, ms) {
  let t;
  return () => { clearTimeout(t); t = setTimeout(fn, ms); };
}

// Turns text into nodes, making http(s) URLs clickable.
function linkify(text) {
  const frag = document.createDocumentFragment();
  const re = /https?:\/\/[^\s<>"']+/g;
  let last = 0;
  for (const m of text.matchAll(re)) {
    let url = m[0];
    const trail = url.match(/[.,;:!?)\]]+$/);
    if (trail) url = url.slice(0, -trail[0].length);
    frag.append(text.slice(last, m.index));
    frag.append(el('a', { href: url, target: '_blank', rel: 'noopener noreferrer' }, url));
    last = m.index + url.length;
  }
  frag.append(text.slice(last));
  return frag;
}

async function copyText(text) {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch { /* fall through to the legacy path */ }
  // Plain-HTTP pages (the LAN address) are not a secure context, so use the legacy API.
  const ta = el('textarea', { value: text });
  ta.setAttribute('readonly', '');
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.append(ta);
  ta.select();
  ta.setSelectionRange(0, text.length);
  let ok = false;
  try { ok = document.execCommand('copy'); } catch { ok = false; }
  ta.remove();
  return ok;
}

// ---------- boot ----------

async function boot() {
  try {
    await enter(await api('/api/me'));
    return;
  } catch (e) {
    if (e.status !== 401) return fatal(e);
  }
  if (isLocalhost()) {
    try {
      await enter(await api('/api/owner/session', { method: 'POST' }));
      return;
    } catch { /* fall back to PIN */ }
  }
  await showJoin();
}

function fatal(e) {
  show('loading');
  $('loading').replaceChildren(el('p', { class: 'err' }, 'เกิดข้อผิดพลาด: ' + e.message));
}

// ---------- join with PIN ----------

let lockTimer;

async function showJoin() {
  const roomId = new URLSearchParams(location.search).get('r') || 'main';
  let info;
  try {
    info = await api('/api/rooms/' + encodeURIComponent(roomId) + '/info');
  } catch (e) {
    return fatal(e.status === 404 ? new Error('ไม่พบห้องนี้') : e);
  }
  $('join-room').textContent = info.name;
  $('pin-len').textContent = info.pinLength;
  $('pin').maxLength = info.pinLength;
  $('dev-name').value = localStorage.getItem('lanshare.deviceName') || guessPlatform() + ' ของฉัน';
  show('join');
  $('pin').focus();

  $('pin').addEventListener('input', (e) => {
    e.target.value = e.target.value.replace(/\D/g, '');
  });
  $('join-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const err = $('join-error');
    err.hidden = true;
    $('join-btn').disabled = true;
    const name = $('dev-name').value.trim();
    try {
      const me = await api('/api/rooms/' + encodeURIComponent(info.id) + '/join',
        jsonBody('POST', { pin: $('pin').value, deviceName: name, platform: guessPlatform() }));
      localStorage.setItem('lanshare.deviceName', name);
      await enter(me);
    } catch (e) {
      $('pin').value = '';
      if (e.body && e.body.error === 'invalid_pin') {
        err.textContent = `PIN ไม่ถูกต้อง เหลืออีก ${e.body.remaining} ครั้ง`;
      } else if (e.body && e.body.error === 'locked') {
        startLockCountdown(e.body.retryAfterSeconds);
        return;
      } else {
        err.textContent = 'เข้าห้องไม่สำเร็จ: ' + e.message;
      }
      err.hidden = false;
      $('join-btn').disabled = false;
      $('pin').focus();
    }
  });
}

function startLockCountdown(seconds) {
  const err = $('join-error');
  clearInterval(lockTimer);
  let left = seconds;
  const tick = () => {
    const m = String(Math.floor(left / 60)).padStart(2, '0');
    const s = String(left % 60).padStart(2, '0');
    err.textContent = `กรอก PIN ผิดหลายครั้ง ลองใหม่ได้ใน ${m}:${s}`;
    err.hidden = false;
    if (left <= 0) {
      clearInterval(lockTimer);
      err.hidden = true;
      $('join-btn').disabled = false;
    }
    left--;
  };
  $('join-btn').disabled = true;
  tick();
  lockTimer = setInterval(tick, 1000);
}

// ---------- main app ----------

const role = () => state.me.role;
const canUpload = () => ['owner', 'member', 'uploader'].includes(role());
const canDownload = () => ['owner', 'member', 'viewer'].includes(role());
const canChat = () => ['owner', 'member'].includes(role());

async function enter(me) {
  state.me = me.device;
  state.room = me.room;
  show('app');
  $('side-room-name').textContent = me.room.name;
  $('side-room-sub').textContent = ROLE_LABEL[me.device.role] || '';
  $('room-name').textContent = '"' + me.room.name + '"';
  document.title = me.room.name + ' • LAN Share';

  setupNav();
  setupUploads();
  setupFilesPane();
  setupChat();
  setView(isDesktopLayout() ? 'files' : 'upload');
  if (role() === 'owner') await setupOwner();
  connectWS();
  await Promise.all([refreshDevices(), refreshFiles(), refreshChat()]);
}

function setConn(on) {
  $('conn-dot').classList.toggle('off', !on);
  $('conn-text').textContent = on ? 'เชื่อมต่อแล้ว' : 'ขาดการเชื่อมต่อ กำลังลองใหม่…';
}

function connectWS() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const ws = new WebSocket(`${proto}://${location.host}/ws`);
  state.ws = ws;
  ws.onopen = () => {
    state.wsRetry = 0;
    setConn(true);
    refreshDevices();
    refreshFiles();
    refreshChat();
  };
  ws.onmessage = (ev) => {
    let msg;
    try { msg = JSON.parse(ev.data); } catch { return; }
    handleEvent(msg);
  };
  ws.onclose = async () => {
    setConn(false);
    state.wsRetry++;
    if (state.wsRetry >= 3) {
      try { await api('/api/me'); } catch (e) { if (e.status === 401) return location.reload(); }
    }
    setTimeout(connectWS, Math.min(1000 * 2 ** state.wsRetry, 10000));
  };
}

const refreshDevicesSoon = debounce(refreshDevices, 100);
const refreshFilesSoon = debounce(refreshFiles, 100);

function handleEvent(msg) {
  switch (msg.type) {
    case 'devices': refreshDevicesSoon(); break;
    case 'file.added':
    case 'file.deleted': refreshFilesSoon(); break;
    case 'transfer': onRemoteTransfer(msg.data); break;
    case 'message': onMessage(msg.data); break;
    case 'clipboard': onClipboard(msg.data); break;
  }
}

// ---------- navigation ----------

function setupNav() {
  if (setupNav.done) return;
  setupNav.done = true;
  for (const btn of $$('[data-go]')) btn.addEventListener('click', () => setView(btn.dataset.go));
  window.matchMedia('(min-width: 821px)').addEventListener('change', () => setView(state.view));
}

function setView(v) {
  state.view = v;
  $('app').dataset.view = v;
  const sideActive = v === 'upload' ? 'files' : v; // on wide screens upload is always visible
  for (const b of $$('.side [data-go]')) b.setAttribute('aria-current', b.dataset.go === sideActive ? 'page' : 'false');
  for (const b of $$('#tabbar [data-go]')) b.setAttribute('aria-current', b.dataset.go === v ? 'page' : 'false');
  if (v === 'chat') {
    state.unread = 0;
    renderUnread();
    scrollMessages();
  }
}

function renderUnread() {
  for (const id of ['nav-chat-badge', 'tab-chat-badge']) {
    const b = $(id);
    b.hidden = state.unread === 0;
    if (id === 'nav-chat-badge') b.textContent = state.unread;
  }
}

// ---------- devices ----------

async function refreshDevices() {
  try { state.devices = await api('/api/devices'); } catch { return; }
  $('device-list').replaceChildren(...state.devices.map((d) => el('li', { class: d.me ? 'me' : '' },
    el('span', { class: 'dev-ic' }, PLATFORM_ICON[d.platform] || '🌐'),
    el('span', { class: 'dev-name grow ellipsis' }, d.name + (d.me ? ' (คุณ)' : ''), el('small', {}, d.platform)),
    el('span', { class: 'dot' + (d.online ? '' : ' off'), title: d.online ? 'ออนไลน์' : 'ออฟไลน์' }),
    el('span', { class: 'pill ' + d.role }, ROLE_LABEL[d.role] || d.role),
  )));
}

// ---------- files ----------

const filePath = (f) => (f.dir ? f.dir + '/' : '') + f.name;

async function refreshFiles() {
  if (!canDownload()) return;
  try { state.files = await api('/api/files'); } catch { return; }
  const ids = new Set(state.files.map((f) => f.id));
  for (const id of state.selected) if (!ids.has(id)) state.selected.delete(id);
  renderFiles();
}

let videoObserver;
function lazyVideo(video, src) {
  if (!('IntersectionObserver' in window)) { video.src = src; return; }
  videoObserver ||= new IntersectionObserver((entries) => {
    for (const e of entries) {
      if (e.isIntersecting) {
        e.target.src = e.target.dataset.src;
        videoObserver.unobserve(e.target);
      }
    }
  }, { rootMargin: '200px' });
  video.dataset.src = src;
  videoObserver.observe(video);
}

function thumbFor(f) {
  const kind = KIND_ICON[f.kind] ? f.kind : 'other';
  const th = el('div', { class: 'th k-' + kind, title: filePath(f) }, KIND_ICON[kind]);
  const fallback = () => { th.querySelectorAll('img,video,.play').forEach((n) => n.remove()); };
  if (f.thumb) {
    th.append(el('img', {
      src: '/api/files/' + encodeURIComponent(f.id) + '/thumb', alt: '', loading: 'lazy', onerror: fallback,
    }));
  } else if (f.kind === 'video') {
    const v = el('video', { muted: true, preload: 'metadata', playsInline: true, onerror: fallback });
    v.muted = true;
    lazyVideo(v, '/api/files/' + encodeURIComponent(f.id) + '#t=0.1');
    th.append(v, el('span', { class: 'play' }, '▶'));
  }
  return th;
}

function renderFiles() {
  const grid = $('files');
  $('files-empty').hidden = state.files.length > 0;
  $('file-count').textContent = state.files.length ? `(${state.files.length})` : '';
  $('select-all').hidden = state.files.length === 0;
  $('nav-files-count').hidden = state.files.length === 0;
  $('nav-files-count').textContent = state.files.length;
  const isOwner = role() === 'owner';

  grid.replaceChildren(...state.files.map((f) => {
    const checked = state.selected.has(f.id);
    const th = thumbFor(f);
    th.append(el('span', { class: 'ck', 'aria-hidden': 'true' }, checked ? '✓' : ''));
    th.addEventListener('click', () => toggleSelect(f.id));

    const acts = el('div', { class: 'acts' },
      el('a', { class: 'btn ghost sm', href: '/api/files/' + encodeURIComponent(f.id), download: f.name }, '⬇ ดาวน์โหลด'));
    if (isOwner) {
      acts.append(el('button', { class: 'btn danger sm', type: 'button', onclick: () => deleteFile(f) }, 'ลบ'));
    }
    return el('div', { class: 'fc' + (checked ? ' chk' : '') },
      th,
      el('div', { class: 'bd' },
        f.dir ? el('div', { class: 'dir ellipsis' }, '📁 ' + f.dir + '/') : null,
        el('div', { class: 'nm ellipsis', title: filePath(f) }, f.name),
        el('div', { class: 'sub ellipsis' }, `${formatBytes(f.size)} • ${f.uploaderName}`),
        el('div', { class: 'sub' }, formatTime(f.createdAt)),
        acts,
      ),
    );
  }));
  renderSelection();
}

function toggleSelect(id) {
  if (state.selected.has(id)) state.selected.delete(id); else state.selected.add(id);
  renderFiles();
}

function renderSelection() {
  const n = state.selected.size;
  $('selbar').hidden = n === 0;
  if (n) {
    const total = state.files.filter((f) => state.selected.has(f.id)).reduce((s, f) => s + f.size, 0);
    $('sel-count').textContent = `เลือก ${n} ไฟล์`;
    $('sel-size').textContent = '• ' + formatBytes(total);
  }
  const all = state.files.length > 0 && n === state.files.length;
  $('select-all').textContent = all ? 'ยกเลิกทั้งหมด' : 'เลือกทั้งหมด';
}

function setupFilesPane() {
  if (setupFilesPane.done) return;
  setupFilesPane.done = true;
  $('select-all').addEventListener('click', () => {
    if (state.selected.size === state.files.length) state.selected.clear();
    else state.files.forEach((f) => state.selected.add(f.id));
    renderFiles();
  });
  $('sel-clear').addEventListener('click', () => { state.selected.clear(); renderFiles(); });
  $('sel-zip').addEventListener('click', () => {
    if (!state.selected.size) return;
    // The response is an attachment, so the browser downloads it without leaving the page.
    const a = el('a', { href: '/api/files/zip?ids=' + [...state.selected].map(encodeURIComponent).join(',') });
    a.setAttribute('download', '');
    document.body.append(a);
    a.click();
    a.remove();
  });
}

async function deleteFile(f) {
  if (!confirm(`ลบไฟล์ "${filePath(f)}" ?`)) return;
  try {
    await api('/api/files/' + encodeURIComponent(f.id), { method: 'DELETE' });
    refreshFiles();
  } catch (e) { toast('ลบไม่สำเร็จ: ' + e.message); }
}

// ---------- uploads ----------

let uploadSeq = 0;
const queue = [];
let active = 0;

function setupUploads() {
  if (setupUploads.done) return;
  setupUploads.done = true;
  const drop = $('drop');
  const input = $('file-input');
  const dirInput = $('dir-input');

  const policy = $('conflict-policy');
  policy.value = localStorage.getItem('lanshare.conflict') || 'ask';
  policy.addEventListener('change', () => localStorage.setItem('lanshare.conflict', policy.value));

  if (!canUpload()) {
    drop.hidden = true;
    $('pick-dir').hidden = true;
    return;
  }
  // Folder picking only makes sense with a mouse; phones cannot choose folders.
  $('pick-dir').hidden = !window.matchMedia('(pointer: fine)').matches;
  $('paste-hint').hidden = !window.matchMedia('(pointer: fine)').matches;

  drop.addEventListener('click', () => input.click());
  drop.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); input.click(); }
  });
  $('pick-dir').addEventListener('click', () => dirInput.click());
  input.addEventListener('change', () => {
    addFiles([...input.files].map((file) => ({ file, dir: '' })));
    input.value = '';
  });
  dirInput.addEventListener('change', () => {
    addFiles([...dirInput.files].map((file) => {
      const rel = file.webkitRelativePath || file.name;
      const i = rel.lastIndexOf('/');
      return { file, dir: i > 0 ? rel.slice(0, i) : '' };
    }));
    dirInput.value = '';
  });

  for (const ev of ['dragenter', 'dragover']) {
    drop.addEventListener(ev, (e) => { e.preventDefault(); drop.classList.add('over'); });
  }
  drop.addEventListener('dragleave', (e) => { e.preventDefault(); drop.classList.remove('over'); });
  drop.addEventListener('drop', async (e) => {
    e.preventDefault();
    drop.classList.remove('over');
    // dataTransfer is only valid synchronously, so grab entries first.
    const dt = e.dataTransfer;
    const entries = [];
    const plain = [];
    for (const item of dt.items || []) {
      if (item.kind !== 'file') continue;
      const entry = item.webkitGetAsEntry && item.webkitGetAsEntry();
      if (entry) entries.push(entry);
      else { const f = item.getAsFile(); if (f) plain.push({ file: f, dir: '' }); }
    }
    const found = plain;
    try {
      for (const entry of entries) await walkEntry(entry, '', found);
    } catch (err) { toast('อ่านโฟลเดอร์ไม่สำเร็จ: ' + err.message); }
    if (found.length > MAX_DROPPED_FILES) {
      toast(`ส่งได้ครั้งละไม่เกิน ${MAX_DROPPED_FILES} ไฟล์ ระบบจะส่งเฉพาะส่วนแรก`);
      found.length = MAX_DROPPED_FILES;
    }
    addFiles(found);
  });

  // Paste an image (screenshot) from the clipboard.
  document.addEventListener('paste', (e) => {
    const imgs = [...(e.clipboardData?.files || [])].filter((f) => f.type.startsWith('image/'));
    if (!imgs.length) return;
    e.preventDefault();
    const d = new Date();
    const p2 = (n) => String(n).padStart(2, '0');
    const stamp = `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}.${p2(d.getMinutes())}.${p2(d.getSeconds())}`;
    addFiles(imgs.map((f, i) => {
      const ext = { 'image/png': 'png', 'image/jpeg': 'jpg', 'image/gif': 'gif', 'image/webp': 'webp' }[f.type] || 'png';
      const name = `สกรีนช็อต ${stamp}${imgs.length > 1 ? ' ' + (i + 1) : ''}.${ext}`;
      return { file: new File([f], name, { type: f.type }), dir: '' };
    }));
  });
}

async function walkEntry(entry, dir, out) {
  if (out.length > MAX_DROPPED_FILES) return;
  if (entry.isFile) {
    if (JUNK_FILES.has(entry.name)) return;
    const file = await new Promise((res, rej) => entry.file(res, rej));
    out.push({ file, dir });
  } else if (entry.isDirectory) {
    const sub = dir ? dir + '/' + entry.name : entry.name;
    const reader = entry.createReader();
    let batch;
    do { // readEntries returns results in chunks
      batch = await new Promise((res, rej) => reader.readEntries(res, rej));
      for (const child of batch) await walkEntry(child, sub, out);
    } while (batch.length);
  }
}

function findExisting(dir, name) {
  const d = dir.toLowerCase();
  const n = name.toLowerCase();
  return state.files.find((f) => f.dir.toLowerCase() === d && f.name.toLowerCase() === n);
}

// Decides what to do for files whose name already exists, then queues the uploads.
async function addFiles(items) {
  items = items.filter(({ file }) => !JUNK_FILES.has(file.name));
  let remembered = null; // choice made with "apply to all"
  let warnedOverwrite = false;
  const accepted = [];
  for (const { file, dir } of items) {
    const cleanDir = dir.replace(/^\/+|\/+$/g, '');
    const existing = findExisting(cleanDir, file.name);
    let conflict = '';
    if (existing) {
      const canOverwrite = role() === 'owner' || existing.mine;
      let choice = $('conflict-policy').value;
      if (choice === 'ask') {
        if (remembered) {
          choice = remembered;
        } else {
          const r = await askConflict(file, cleanDir, existing, canOverwrite);
          choice = r.choice;
          if (r.all) remembered = choice;
        }
      }
      if (choice === 'skip') continue;
      if (choice === 'overwrite' && !canOverwrite) {
        if (!warnedOverwrite) toast('เขียนทับไฟล์ของคนอื่นไม่ได้ จะเก็บทั้งสองไฟล์แทน');
        warnedOverwrite = true;
        choice = 'rename';
      }
      conflict = choice;
    }
    accepted.push({ file, dir: cleanDir, conflict });
  }
  if (accepted.length) enqueue(accepted);
}

function askConflict(file, dir, existing, canOverwrite) {
  const dlg = $('conflict-dlg');
  $('cf-name').textContent = (dir ? dir + '/' : '') + file.name;
  $('cf-info').textContent = `ไฟล์เดิม ${formatBytes(existing.size)} จาก ${existing.uploaderName} • ไฟล์ใหม่ ${formatBytes(file.size)}`;
  const ow = dlg.querySelector('input[value="overwrite"]');
  ow.disabled = !canOverwrite;
  $('cf-overwrite-opt').classList.toggle('disabled', !canOverwrite);
  $('cf-overwrite-hint').textContent = canOverwrite ? 'แทนที่ไฟล์เดิม' : 'ทำได้เฉพาะไฟล์ที่คุณส่งเอง หรือเจ้าของห้อง';
  dlg.querySelector('input[value="rename"]').checked = true;
  $('cf-all').checked = false;
  return new Promise((resolve) => {
    dlg.addEventListener('close', () => {
      const picked = dlg.returnValue === 'ok' ? dlg.querySelector('input[name="cf"]:checked').value : 'skip';
      resolve({ choice: picked, all: $('cf-all').checked });
    }, { once: true });
    dlg.returnValue = '';
    dlg.showModal();
  });
}

function enqueue(items) {
  for (const { file, dir, conflict } of items) {
    const item = { id: ++uploadSeq, file, dir, conflict, loaded: 0, total: file.size, speed: 0, state: 'queued', xhr: null, error: '' };
    state.uploads.unshift(item);
    queue.push(item);
  }
  pump();
  renderTransfers();
  if (!isDesktopLayout()) setView('upload');
}

function pump() {
  while (active < MAX_PARALLEL_UPLOADS && queue.length) {
    const item = queue.shift();
    if (item.state === 'cancelled') continue;
    active++;
    startUpload(item).finally(() => { active--; pump(); });
  }
}

function startUpload(item) {
  return new Promise((resolve) => {
    const xhr = new XMLHttpRequest();
    item.xhr = xhr;
    item.state = 'running';
    let lastT = performance.now();
    let lastB = 0;

    let url = '/api/files?name=' + encodeURIComponent(item.file.name);
    if (item.dir) url += '&dir=' + encodeURIComponent(item.dir);
    if (item.conflict) url += '&conflict=' + item.conflict;
    xhr.open('POST', url);
    xhr.setRequestHeader('X-LanShare-Client', '1');
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');

    xhr.upload.onprogress = (e) => {
      item.loaded = e.loaded;
      const now = performance.now();
      if (now - lastT >= 500) {
        item.speed = ((e.loaded - lastB) / (now - lastT)) * 1000;
        lastT = now;
        lastB = e.loaded;
      }
      renderTransfersSoon();
    };
    const finish = (stateName, error) => {
      item.state = stateName;
      item.speed = 0;
      item.error = error || '';
      if (stateName === 'done') item.loaded = item.total;
      renderTransfers();
      if (stateName === 'done') refreshFiles();
      setTimeout(() => {
        const i = state.uploads.indexOf(item);
        if (i >= 0 && item.state === 'done') { state.uploads.splice(i, 1); renderTransfers(); }
      }, 6000);
      resolve();
    };
    xhr.onload = () => {
      if (xhr.status === 201) return finish('done');
      const msg = xhr.status === 403 ? 'ไม่มีสิทธิ์ทำรายการนี้'
        : xhr.status === 401 ? 'หมดเวลาเข้าสู่ระบบ' : 'ผิดพลาด (' + xhr.status + ')';
      finish('failed', msg);
    };
    xhr.onerror = () => finish('failed', 'การเชื่อมต่อขัดข้อง');
    xhr.onabort = () => finish('cancelled', 'ยกเลิกแล้ว');
    xhr.send(item.file);
  });
}

function cancelUpload(item) {
  if (item.xhr && item.state === 'running') item.xhr.abort();
  else { item.state = 'cancelled'; renderTransfers(); }
}

// ---------- remote transfers (other devices) ----------

const remoteTimers = new Map();

function onRemoteTransfer(p) {
  if (p.deviceId === state.me.id) return; // local transfers are shown with exact browser progress
  const key = p.direction + ':' + p.id + ':' + p.deviceId;
  state.remote.set(key, p);
  clearTimeout(remoteTimers.get(key));
  if (p.state !== 'running') {
    remoteTimers.set(key, setTimeout(() => { state.remote.delete(key); renderTransfers(); }, p.state === 'done' ? 4000 : 8000));
  }
  renderTransfersSoon();
}

// ---------- transfer list rendering ----------

let raf = 0;
function renderTransfersSoon() {
  if (raf) return;
  raf = requestAnimationFrame(() => { raf = 0; renderTransfers(); });
}

function transferRow({ icon, name, detail, pct, barClass, action }) {
  const fill = el('i', { class: barClass || '' });
  fill.style.width = Math.max(0, Math.min(100, pct)) + '%'; // CSSOM is allowed by our CSP
  return el('li', {},
    el('span', {}, icon),
    el('div', { class: 'grow' },
      el('div', { class: 'ellipsis' }, name),
      el('div', { class: 'bar' }, fill),
      el('div', { class: 'xfer-meta' }, detail),
    ),
    action || null,
  );
}

function renderTransfers() {
  const rows = [];

  for (const it of state.uploads) {
    if (it.state === 'cancelled') continue;
    const pct = it.total > 0 ? (it.loaded / it.total) * 100 : (it.state === 'done' ? 100 : 0);
    let detail;
    let barClass = '';
    if (it.state === 'queued') detail = 'รอคิว • ' + formatBytes(it.total);
    else if (it.state === 'running') detail = `⬆ ${formatBytes(it.loaded)} / ${formatBytes(it.total)} • ${pct.toFixed(0)}% • ${formatBytes(it.speed)}/s`;
    else if (it.state === 'done') { detail = '✓ ส่งสำเร็จ • ' + formatBytes(it.total); barClass = 'done'; }
    else { detail = '✗ ' + it.error; barClass = 'failed'; }
    const cancellable = it.state === 'queued' || it.state === 'running';
    rows.push(transferRow({
      icon: '⬆️', name: (it.dir ? it.dir + '/' : '') + it.file.name, detail, pct, barClass,
      action: cancellable ? el('button', { class: 'btn ghost sm', type: 'button', title: 'ยกเลิก', onclick: () => cancelUpload(it) }, '✕') : null,
    }));
  }

  for (const p of state.remote.values()) {
    const pct = p.total > 0 ? (p.bytes / p.total) * 100 : 0;
    const arrow = p.direction === 'upload' ? '⬆' : '⬇';
    let detail = `${arrow} ${p.deviceName} • ${formatBytes(p.bytes)}${p.total > 0 ? ' / ' + formatBytes(p.total) : ''}`;
    let barClass = '';
    if (p.state === 'running') detail += ` • ${formatBytes(p.speed)}/s`;
    else if (p.state === 'done') { detail += ' • เสร็จแล้ว ✓'; barClass = 'done'; }
    else { detail += ' • ไม่สำเร็จ'; barClass = 'failed'; }
    rows.push(transferRow({ icon: p.direction === 'upload' ? '📥' : '📤', name: p.name, detail, pct: p.state === 'done' ? 100 : pct, barClass }));
  }

  $('transfers-card').hidden = rows.length === 0;
  $('transfers').replaceChildren(...rows);
}

// ---------- messages & shared clipboard ----------

function setupChat() {
  if (setupChat.done) return;
  setupChat.done = true;
  if (!canChat()) {
    $('clip-form').hidden = true;
    $('clip-clear').hidden = true;
    $('msg-form').hidden = true;
  }

  $('msg-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const input = $('msg-input');
    const text = input.value.trim();
    if (!text) return;
    input.value = '';
    autosize(input);
    try { await api('/api/messages', jsonBody('POST', { text })); } catch (err) {
      input.value = text;
      toast('ส่งข้อความไม่สำเร็จ: ' + err.message);
    }
  });
  $('msg-input').addEventListener('input', (e) => autosize(e.target));
  $('msg-input').addEventListener('keydown', (e) => {
    // Enter sends on desktop; on touch keyboards Enter inserts a new line and the button sends.
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && window.matchMedia('(pointer: fine)').matches) {
      e.preventDefault();
      $('msg-form').requestSubmit();
    }
  });

  $('clip-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const text = $('clip-input').value;
    if (!text.trim()) return;
    try {
      await api('/api/clipboard', jsonBody('PUT', { text }));
      $('clip-input').value = '';
      toast('แชร์ไปคลิปบอร์ดร่วมแล้ว');
    } catch (err) { toast('แชร์ไม่สำเร็จ: ' + err.message); }
  });
  $('clip-clear').addEventListener('click', async () => {
    try { await api('/api/clipboard', jsonBody('PUT', { text: '' })); } catch (err) { toast('ล้างไม่สำเร็จ: ' + err.message); }
  });
  $('clip-copy').addEventListener('click', async () => {
    if (!state.clip || !state.clip.text) return toast('ยังไม่มีข้อความในคลิปบอร์ดร่วม');
    toast((await copyText(state.clip.text)) ? 'คัดลอกแล้ว' : 'คัดลอกไม่สำเร็จ ให้เลือกข้อความแล้วคัดลอกเอง');
  });
}

function autosize(ta) {
  ta.style.height = 'auto';
  ta.style.height = Math.min(ta.scrollHeight, 140) + 'px';
}

async function refreshChat() {
  if (!canDownload()) return;
  try {
    const [msgs, clip] = await Promise.all([api('/api/messages'), api('/api/clipboard')]);
    state.messages = msgs;
    state.clip = clip;
  } catch { return; }
  renderMessages();
  renderClip();
}

function renderClip() {
  const c = state.clip;
  const box = $('clip-text');
  if (c && c.text) {
    box.classList.remove('placeholder');
    box.replaceChildren(linkify(c.text));
    $('clip-meta').textContent = `จาก ${c.deviceName} • ${formatTime(c.at)}`;
  } else {
    box.classList.add('placeholder');
    box.replaceChildren('ยังไม่มีข้อความในคลิปบอร์ดร่วม');
    $('clip-meta').textContent = '';
  }
  $('clip-copy').disabled = !(c && c.text);
}

function messageNode(m) {
  const mine = m.deviceId === state.me.id;
  return el('div', { class: 'bub' + (mine ? ' me' : '') },
    mine ? null : el('div', { class: 'who' }, m.deviceName),
    el('div', { class: 'txt' }, linkify(m.text)),
    el('div', { class: 'when' }, formatTime(m.at)),
  );
}

function renderMessages() {
  $('messages-empty').hidden = state.messages.length > 0;
  $('messages').replaceChildren(...state.messages.map(messageNode));
  scrollMessages();
}

function scrollMessages() {
  const box = $('messages');
  box.scrollTop = box.scrollHeight;
}

function onMessage(m) {
  if (state.messages.some((x) => x.id === m.id)) return;
  state.messages.push(m);
  if (state.messages.length > 200) state.messages.shift();
  $('messages-empty').hidden = true;
  $('messages').append(messageNode(m));
  scrollMessages();
  if (m.deviceId !== state.me.id && state.view !== 'chat') {
    state.unread++;
    renderUnread();
    toast(`ข้อความใหม่จาก ${m.deviceName}`);
  }
}

function onClipboard(c) {
  state.clip = c;
  renderClip();
  if (c.deviceId !== state.me.id && c.text && state.view !== 'chat') {
    toast(`${c.deviceName} แชร์ข้อความไปคลิปบอร์ดร่วม`);
  }
}

// ---------- owner panel ----------

const KIND_LABEL = { physical: 'Wi-Fi/Ethernet', vpn: 'VPN', virtual: 'เสมือน' };

async function setupOwner() {
  if (setupOwner.done) return;
  setupOwner.done = true;
  $('owner').hidden = false;
  await loadJoinInfo();
  await loadNetworks();

  $('rotate-pin').addEventListener('click', async () => {
    if (!confirm('สร้าง PIN ใหม่? อุปกรณ์ที่เข้าห้องแล้วจะยังใช้งานต่อได้')) return;
    try {
      const r = await api('/api/admin/pin/rotate', { method: 'POST' });
      renderPin(r.pin);
    } catch (e) { toast('สร้าง PIN ไม่สำเร็จ: ' + e.message); }
  });
  $('nic').addEventListener('change', async (e) => {
    try {
      await api('/api/admin/network', jsonBody('POST', { name: e.target.value }));
      await loadJoinInfo();
      await loadNetworks();
    } catch (err) { toast('เปลี่ยนเครือข่ายไม่สำเร็จ: ' + err.message); }
  });
  $('copy-url').addEventListener('click', async () => {
    toast((await copyText($('join-url').textContent)) ? 'คัดลอกลิงก์แล้ว' : 'คัดลอกไม่สำเร็จ ให้เลือกข้อความแล้วคัดลอกเอง');
  });
}

function renderPin(pin) {
  $('pin-digits').replaceChildren(...[...pin].map((d) => el('span', {}, d)));
}

async function loadJoinInfo() {
  try {
    const info = await api('/api/admin/join-info');
    $('join-url').textContent = info.url || 'ไม่พบเครือข่ายที่ใช้งานได้';
    renderPin(info.pin);
    $('qr').src = '/api/admin/qr.png?ts=' + Date.now();
  } catch (e) { toast('โหลดข้อมูลห้องไม่สำเร็จ: ' + e.message); }
}

async function loadNetworks() {
  try {
    const list = await api('/api/admin/network');
    $('nic').replaceChildren(...list.map((n) => {
      const label = `${n.name} • ${n.ip} (${KIND_LABEL[n.kind] || n.kind})${n.recommended ? ' — แนะนำ' : ''}`;
      return el('option', { value: n.name, selected: n.selected }, label);
    }));
  } catch { /* the select stays empty */ }
}

boot();
