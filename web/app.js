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
  checksums: new Map(), // file id -> SHA-256 computed on request for files the listing reports as `unknown`
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
    else if (k.startsWith('aria-') || k === 'role') node.setAttribute(k, v); // attributes, not JS properties
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

const KIND_ICON = { image: '🖼️', video: '🎬', audio: '🎵', document: '📄', archive: '📦', other: '📄' };
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

const BOOT_TIMEOUT_MS = 10000; // the first request must answer within this, or the offline page shows

// bootRequest is api() with an AbortController timeout. A failure without an HTTP status (the
// network is down, the server is unreachable, or no answer in time) is marked e.offline = true.
async function bootRequest(path, opts = {}) {
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), BOOT_TIMEOUT_MS);
  try {
    return await api(path, { ...opts, signal: ctl.signal });
  } catch (e) {
    if (typeof e.status !== 'number') e.offline = true;
    throw e;
  } finally {
    clearTimeout(timer);
  }
}

async function boot() {
  let me = null;
  try {
    me = await bootRequest('/api/me');
  } catch (e) {
    if (e.offline) return showOffline();
    if (e.status !== 401) return fatal(e);
  }
  if (me) {
    try {
      await enter(me);
      return;
    } catch (e) {
      if (e.status !== 401) return fatal(e);
    }
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

// Shown when the first API call cannot reach the server (for instance when the page itself came
// from the Service Worker cache). Nothing from the room is rendered; "ลองใหม่" runs boot() again,
// which replaces this message with the normal screen once the server answers.
function showOffline() {
  show('loading');
  const retry = el('button', { class: 'btn', type: 'button' }, 'ลองใหม่');
  retry.addEventListener('click', () => {
    retry.disabled = true;
    $('loading').replaceChildren(el('p', { class: 'mut' }, 'กำลังโหลด…'));
    boot();
  });
  $('loading').replaceChildren(el('div', { class: 'offline' },
    el('p', { class: 'err', role: 'alert' }, 'เชื่อมต่อเซิร์ฟเวอร์ไม่ได้'), retry));
  retry.focus();
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
  $('join-btn').disabled = false;
  show('join');
  $('pin').focus();

  // showJoin can run again when an upload needs the user to enter the room anew (see
  // promptRejoin); bind the handlers once and let them read the latest room info.
  showJoin.info = info;
  if (showJoin.bound) return;
  showJoin.bound = true;

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
      const me = await api('/api/rooms/' + encodeURIComponent(showJoin.info.id) + '/join',
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
  resetUnseen(); // a title left over from the unseen count must not become the "original" one
  document.title = me.room.name + ' • LAN Share';

  setupNav();
  setupUploads();
  setupFilesPane();
  setupChat();
  setView(isDesktopLayout() ? 'files' : 'upload');
  if (role() === 'owner') await setupOwner();
  setupSettings();
  connectWS();
  resumeAfterRejoin(); // uploads paused by a 401 carry on without asking for the files again
  await Promise.all([refreshDevices(), refreshFiles(), refreshChat()]);
}

function setConn(on) {
  $('conn-dot').classList.toggle('off', !on);
  $('conn-text').textContent = on ? 'เชื่อมต่อแล้ว' : 'ขาดการเชื่อมต่อ กำลังลองใหม่…';
}

function connectWS() {
  // enter() runs again after a re-join; never keep two sockets open
  if (state.ws && (state.ws.readyState === WebSocket.CONNECTING || state.ws.readyState === WebSocket.OPEN)) return;
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
      try { await api('/api/me'); } catch (e) {
        if (e.status === 401) {
          // a reload would drop the File objects of paused uploads; ask for the PIN in place instead
          if (hasAuthPausedUploads()) return promptRejoin();
          return location.reload();
        }
      }
    }
    setTimeout(connectWS, Math.min(1000 * 2 ** state.wsRetry, 10000));
  };
}

const refreshDevicesSoon = debounce(refreshDevices, 100);
const refreshFilesSoon = debounce(refreshFiles, 100);

function handleEvent(msg) {
  switch (msg.type) {
    case 'devices': refreshDevicesSoon(); break;
    case 'file.added': onFileAdded(msg.data); break;
    case 'file.deleted': refreshFilesSoon(); break;
    case 'transfer': onRemoteTransfer(msg.data); break;
    case 'message': onMessage(msg.data); break;
    case 'clipboard': onClipboard(msg.data); break;
  }
}

// ---------- new-file notifications ----------
//
// `file.added` carries only the file id. The id is parked in pendingAdded and the file list is
// asked for again; a parked id that shows up in the answer and is not ours is a notifiable file.
// An id that is missing, or an answer that never comes, is dropped without a word. Files added
// while the WebSocket was down never produce an event, so they are never announced. The pure
// rules (who is notifiable, grouping within 5 s, the "(N) " title prefix) live in LanShareLogic.

const NOTIFY_KEY = 'lanshare.notify';
const NOTICE_MS = 5000;
const pendingAdded = new Set();
const notifyState = {
  group: null, // current group from Logic.addToNotifyGroup
  unseen: 0, // notifiable files that arrived while the page was hidden
  baseTitle: null, // document.title before the "(N) " prefix, null when no prefix is set
  noticeTimer: 0,
  notice: null, // in-page banner element, built on first use
  noticeText: null,
  sys: null, // system notification of the current group
  sysOn: false, // the setting; follows the stored value only if the permission is granted
};

function onFileAdded(id) {
  if (!canDownload()) return; // uploader: no metadata request, no notice, no count
  if (typeof id === 'string' && id) pendingAdded.add(id);
  refreshFilesSoon();
}

function forgetAdded(ids) {
  for (const id of ids) pendingAdded.delete(id);
}

function notifyNewFiles(asked) {
  const live = new Set(asked.filter((id) => pendingAdded.has(id))); // another refresh may have taken some
  forgetAdded(asked);
  if (live.size === 0) return;
  for (const f of Logic.notifiableFiles(live, state.files, role())) announceFile(f);
}

function announceFile(f) {
  const { group, isNew } = Logic.addToNotifyGroup(notifyState.group, f, Date.now());
  notifyState.group = group;
  if (document.visibilityState === 'visible') {
    showNotice(group.message);
    return;
  }
  notifyState.unseen++;
  if (notifyState.baseTitle === null) notifyState.baseTitle = document.title;
  document.title = Logic.titleCount(notifyState.unseen) + notifyState.baseTitle;
  showSystemNotification(group, isNew);
}

function resetUnseen() {
  if (notifyState.baseTitle !== null) document.title = notifyState.baseTitle;
  notifyState.baseTitle = null;
  notifyState.unseen = 0;
}

document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') resetUnseen();
});

function hideNotice() {
  clearTimeout(notifyState.noticeTimer);
  if (notifyState.notice) notifyState.notice.hidden = true;
}

function showNotice(text) {
  if (!notifyState.notice) {
    notifyState.noticeText = el('span', { class: 'grow' });
    notifyState.notice = el('div', { class: 'notice', role: 'status', 'aria-live': 'polite', hidden: true },
      notifyState.noticeText,
      el('button', { class: 'notice-x', type: 'button', 'aria-label': 'ปิดการแจ้งเตือน', onclick: hideNotice }, '✕'));
    document.body.append(notifyState.notice);
  }
  notifyState.noticeText.textContent = text; // file and device names: text only
  notifyState.notice.hidden = false;
  clearTimeout(notifyState.noticeTimer);
  notifyState.noticeTimer = setTimeout(hideNotice, NOTICE_MS);
}

const systemNotifySupported = () => window.isSecureContext && 'Notification' in window && role() !== 'uploader';

function showSystemNotification(group, isNew) {
  if (!notifyState.sysOn || !systemNotifySupported() || Notification.permission !== 'granted') return;
  if (!isNew && notifyState.sys) { // same group: one notification that states the total
    try { notifyState.sys.close(); } catch { /* already gone */ }
  }
  try {
    const n = new Notification('LAN Share', { body: group.message, tag: 'lanshare-' + group.startAt });
    n.onclick = () => {
      window.focus();
      setView('files');
      n.close();
    };
    notifyState.sys = n;
  } catch { notifyState.sys = null; } // a browser that refuses the constructor: title count still works
}

function readNotifyPref() {
  try { return localStorage.getItem(NOTIFY_KEY) === '1'; } catch { return false; } // default off
}

function setSystemNotify(on) {
  notifyState.sysOn = on;
  try { localStorage.setItem(NOTIFY_KEY, on ? '1' : '0'); } catch { /* it just does not persist */ }
}

function requestNotifyPermission() {
  return new Promise((resolve) => {
    try {
      const r = Notification.requestPermission(resolve); // older Safari only takes the callback
      if (r && typeof r.then === 'function') r.then(resolve, () => resolve('denied'));
    } catch { resolve('denied'); }
  });
}

// The control exists only where system notifications can work (Secure_Context, Notification API,
// a role that gets file events). The permission is asked for when the user turns it on, never else.
function setupNotifySetting() {
  settingsRow(el('p', { class: 'mut small', id: 'notify-note' }, 'การแจ้งเตือนทำงานเฉพาะขณะเปิดหน้านี้ไว้เท่านั้น'));
  if (!systemNotifySupported()) return;

  const box = el('input', { type: 'checkbox', id: 'notify-toggle' });
  const msg = el('p', { class: 'mut small', id: 'notify-msg', role: 'status', hidden: true });
  notifyState.sysOn = readNotifyPref() && Notification.permission === 'granted';
  box.checked = notifyState.sysOn;
  box.addEventListener('change', async () => {
    msg.hidden = true;
    msg.textContent = '';
    if (!box.checked) { setSystemNotify(false); return; }
    box.disabled = true;
    let perm = Notification.permission;
    if (perm === 'default') perm = await requestNotifyPermission();
    box.disabled = false;
    if (perm === 'granted') { setSystemNotify(true); return; }
    box.checked = false;
    setSystemNotify(false);
    msg.textContent = 'เบราว์เซอร์บล็อกการแจ้งเตือน เปลี่ยนสิทธิ์ได้ในการตั้งค่าของเบราว์เซอร์';
    msg.hidden = false;
  });
  settingsRow(el('div', {}, el('label', { class: 'conflict-pick' }, box, 'แจ้งเตือนของระบบเมื่อมีไฟล์ใหม่'), msg));
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
  if (ownerView) ownerView(v); // only an Owner ever sets this (see setupOwnerTools)
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
  // Only the ids whose event arrived before this request left can be judged by its answer.
  const asked = [...pendingAdded];
  try { state.files = await api('/api/files'); } catch { forgetAdded(asked); return; }
  const ids = new Set(state.files.map((f) => f.id));
  for (const id of state.selected) if (!ids.has(id)) state.selected.delete(id);
  for (const id of state.checksums.keys()) if (!ids.has(id)) state.checksums.delete(id);
  renderFiles();
  if (fileDetail) fileDetail.sync();
  notifyNewFiles(asked);
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

// fileExt is the extension shown on a card that has no picture ("ZIP", "CR2"), or ''.
function fileExt(name) {
  const i = name.lastIndexOf('.');
  return i > 0 && i < name.length - 1 ? name.slice(i + 1, i + 7).toUpperCase() : '';
}

function thumbFor(f) {
  const kind = KIND_ICON[f.kind] ? f.kind : 'other';
  const th = el('div', { class: 'th k-' + kind, title: filePath(f) }, KIND_ICON[kind]);
  const ext = fileExt(f.name);
  if (kind === 'document') th.classList.add('pg'); // pages: show the top of the page, not the middle
  const addExt = () => { if (ext && !th.querySelector('.ext')) th.append(el('span', { class: 'ext' }, ext)); };
  // When no picture can be made (or loading it fails) the card keeps its icon and shows the extension.
  const fallback = () => { th.querySelectorAll('img,video,.play').forEach((n) => n.remove()); addExt(); };
  if (f.thumb) {
    th.append(el('img', {
      src: '/api/files/' + encodeURIComponent(f.id) + '/thumb', alt: '', loading: 'lazy', onerror: fallback,
    }));
  } else if (f.kind === 'video') {
    const v = el('video', { muted: true, preload: 'metadata', playsInline: true, onerror: fallback });
    v.muted = true;
    lazyVideo(v, '/api/files/' + encodeURIComponent(f.id) + '#t=0.1');
    th.append(v, el('span', { class: 'play' }, '▶'));
  } else {
    addExt();
  }
  return th;
}

// Browsers can show these themselves; the original file is used as the preview.
const PLAY_VIDEO = new Set(['mp4', 'm4v', 'mov', 'webm']);
const PLAY_AUDIO = new Set(['mp3', 'm4a', 'wav', 'aac', 'ogg', 'opus', 'flac']);
const SHOW_IMAGE = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp', 'avif', 'bmp']);

// previewNode is the large preview of the details dialog, or null when there is nothing to show:
// the picture itself, a player for video and audio, or the server's preview picture for other
// formats (HEIC, camera RAW, PDF, Office, text ...). The file is loaded only when the dialog opens.
function previewNode(f) {
  const url = '/api/files/' + encodeURIComponent(f.id);
  const ext = f.name.includes('.') ? f.name.slice(f.name.lastIndexOf('.') + 1).toLowerCase() : '';
  const thumbImg = () => el('img', { src: url + '/thumb', alt: '' });
  if (SHOW_IMAGE.has(ext)) {
    const img = el('img', { src: url, alt: '', loading: 'lazy' });
    img.addEventListener('error', () => { if (f.thumb) img.replaceWith(thumbImg()); else img.remove(); }, { once: true });
    return img;
  }
  if (PLAY_VIDEO.has(ext)) return el('video', { src: url, controls: true, preload: 'metadata', playsInline: true });
  if (PLAY_AUDIO.has(ext)) return el('audio', { src: url, controls: true, preload: 'metadata' });
  if (f.thumb) {
    const img = thumbImg();
    img.addEventListener('error', () => img.remove(), { once: true });
    return img;
  }
  return null;
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
      el('a', { class: 'btn ghost sm', href: '/api/files/' + encodeURIComponent(f.id), download: f.name }, '⬇ ดาวน์โหลด'),
      el('button', { class: 'btn ghost sm', type: 'button', onclick: () => openFileDetails(f.id) }, 'รายละเอียด'));
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
        integrityBadgeNode(f.integrity),
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
  $('sel-delete').hidden = role() !== 'owner'; // only the Owner may delete files
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
  $('sel-delete').addEventListener('click', deleteSelected);
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

// deleteSelected deletes the selected files one by one with the single-file endpoint, so a file
// that cannot be deleted does not stop the others. Only the Owner sees the button.
async function deleteSelected() {
  const ids = state.files.filter((f) => state.selected.has(f.id)).map((f) => f.id);
  if (role() !== 'owner' || ids.length === 0) return;
  if (!confirm(`ลบ ${ids.length} ไฟล์ที่เลือก ?\nลบแล้วกู้คืนไม่ได้`)) return;
  const btn = $('sel-delete');
  btn.disabled = true;
  let failed = 0;
  for (const id of ids) {
    try {
      await api('/api/files/' + encodeURIComponent(id), { method: 'DELETE' });
      state.selected.delete(id);
    } catch (e) {
      if (e.status !== 404) failed++; // 404: already gone
      else state.selected.delete(id);
    }
  }
  btn.disabled = false;
  toast(failed ? `ลบแล้ว ${ids.length - failed} ไฟล์ ลบไม่สำเร็จ ${failed} ไฟล์` : `ลบแล้ว ${ids.length} ไฟล์`);
  refreshFiles();
}

async function deleteFile(f) {
  if (!confirm(`ลบไฟล์ "${filePath(f)}" ?`)) return;
  try {
    await api('/api/files/' + encodeURIComponent(f.id), { method: 'DELETE' });
    refreshFiles();
  } catch (e) { toast('ลบไม่สำเร็จ: ' + e.message); }
}

// ---------- file integrity: badge, details, local comparison (Req 8.6, 10.11-10.13, 10.15) ----------

const HEX64 = /^[0-9a-fA-F]{64}$/;
const COPIED_MS = 2000;
let fileDetail = null; // the open details dialog: {close, sync}

// fileSum is the checksum we know for f. The listing never carries a checksum for an `unknown` file,
// so one the server computed on request is kept in state.checksums.
const fileSum = (f) => f.sha256 || state.checksums.get(f.id) || '';

// integrityBadgeNode is the badge of the file list: an icon (decorative) plus Thai text, or
// null for `unknown`/absent so that no icon and no text is shown.
function integrityBadgeNode(integrity) {
  const b = Logic.integrityBadge(integrity);
  if (!b) return null;
  return el('div', { class: 'ig ' + b.kind },
    el('span', { class: 'ig-ic', 'aria-hidden': 'true' }, b.icon), el('span', {}, b.text));
}

function calcErrorMessage(e) {
  const code = (e.body && e.body.error) || '';
  if (e.status === 503 || code === 'checksum_busy') return 'เซิร์ฟเวอร์กำลังคำนวณไฟล์อื่นอยู่ ลองใหม่อีกครั้งในอีกสักครู่';
  if (e.status === 500 || code === 'checksum_failed') return 'เซิร์ฟเวอร์อ่านไฟล์นี้ไม่ได้ จึงคำนวณ SHA-256 ไม่สำเร็จ';
  if (e.status === 404) return 'ไม่พบไฟล์นี้แล้ว (ถูกลบหรือถูกแทนที่)';
  if (e.status === 403) return 'บทบาทของคุณไม่มีสิทธิ์ดูค่า SHA-256 ของไฟล์';
  if (e.status === 401) return 'หมดเวลาการเข้าห้อง กรุณาเข้าห้องใหม่';
  if (!e.status) return 'เชื่อมต่อเซิร์ฟเวอร์ไม่ได้ ลองใหม่อีกครั้ง';
  return 'คำนวณ SHA-256 ไม่สำเร็จ: ' + e.message;
}

function selectNodeText(node) {
  const sel = window.getSelection();
  if (!sel) return;
  const range = document.createRange();
  range.selectNodeContents(node);
  sel.removeAllRanges();
  sel.addRange(range);
}

// openFileDetails shows a modal with the full checksum of a file, a copy button, a "คำนวณ"
// button for files that have none yet, and a comparison with a file chosen on this device. The
// dialog is only built for roles that can download; the other roles never see file metadata.
function openFileDetails(id) {
  if (!canDownload() || !state.files.some((f) => f.id === id)) return;
  if (fileDetail) fileDetail.close();

  let closed = false;
  let busy = false; // the server is computing the checksum
  let calcAbort = null;
  let copyTimer = 0;
  let noteKind = '';
  let cmp = null; // the running local hash (see spawnHash)
  let cmpTimer = 0;

  const dlg = el('dialog', { class: 'dlg fd-dlg' });
  dlg.setAttribute('aria-labelledby', 'fd-title');
  const fname = el('p', { class: 'fd-name' });
  const fmeta = el('p', { class: 'mut small' });
  const badgeBox = el('div', { class: 'fd-badge' });
  const previewBox = el('div', { class: 'fd-preview' });
  const hex = el('code', { class: 'fd-hex', tabIndex: 0 });
  const copyBtn = el('button', { class: 'btn ghost sm', type: 'button', onclick: () => copy() }, 'คัดลอก');
  const calcBtn = el('button', { class: 'btn sm', type: 'button', onclick: () => calc() }, 'คำนวณ');
  const note = el('p', { class: 'small mut', role: 'status' });
  const pickInput = el('input', { type: 'file', hidden: true });
  const pickBtn = el('button', { class: 'btn ghost sm', type: 'button', onclick: () => pickInput.click() }, 'เลือกไฟล์ในเครื่องเพื่อเทียบ');
  const cmpStop = el('button', { class: 'btn ghost sm', type: 'button', hidden: true, onclick: () => stopCmp(true) }, 'ยกเลิก');
  const cmpHint = el('p', { class: 'small mut' }, 'ต้องมีค่า SHA-256 ของไฟล์นี้ก่อนจึงจะเทียบได้ กด "คำนวณ" ด้านบน');
  const cmpProg = el('p', { class: 'small mut fd-prog' });
  const cmpOut = el('div', { class: 'fd-cmp', role: 'status' });
  const closeBtn = el('button', { class: 'btn', type: 'button', onclick: () => dlg.close() }, 'ปิด');

  dlg.append(
    el('h3', { id: 'fd-title' }, 'รายละเอียดไฟล์'),
    fname, fmeta, badgeBox, previewBox,
    el('h4', { class: 'fd-h' }, 'SHA-256'),
    hex,
    el('div', { class: 'fd-row' }, copyBtn, calcBtn),
    note,
    el('h4', { class: 'fd-h' }, 'เทียบกับไฟล์ในเครื่อง'),
    el('div', { class: 'fd-row' }, pickBtn, cmpStop, pickInput),
    cmpHint, cmpProg, cmpOut,
    el('div', { class: 'dlg-actions' }, closeBtn),
  );

  const current = () => state.files.find((f) => f.id === id);
  const first = current();
  const pv = first && previewNode(first);
  if (pv) previewBox.append(pv); else previewBox.hidden = true;
  function setNote(text, kind) { note.textContent = text; noteKind = kind || ''; }

  function sync() {
    if (closed) return;
    const f = current();
    if (!f) {
      dlg.close();
      toast('ไฟล์นี้ถูกลบแล้ว');
      return;
    }
    const sum = fileSum(f);
    fname.textContent = filePath(f);
    fmeta.textContent = `${formatBytes(f.size)} • ${f.uploaderName} • ${formatTime(f.createdAt)}`;
    badgeBox.replaceChildren(...[integrityBadgeNode(f.integrity)].filter(Boolean));
    hex.textContent = sum || 'ยังไม่มีค่า SHA-256 ของไฟล์นี้';
    hex.classList.toggle('none', !sum);
    copyBtn.hidden = !sum;
    calcBtn.hidden = !!sum;
    calcBtn.disabled = busy;
    pickBtn.disabled = !sum || !!cmp;
    cmpStop.hidden = !cmp;
    cmpHint.hidden = !!sum;
  }

  async function copy() {
    const f = current();
    const sum = f && fileSum(f);
    if (!sum) return;
    const ok = await copyText(sum);
    if (closed) return;
    clearTimeout(copyTimer);
    if (ok) {
      copyBtn.textContent = 'คัดลอกแล้ว';
      setNote('คัดลอกแล้ว', 'copied');
      copyTimer = setTimeout(() => {
        copyBtn.textContent = 'คัดลอก';
        if (noteKind === 'copied') setNote('');
      }, COPIED_MS);
    } else {
      copyBtn.textContent = 'คัดลอก';
      selectNodeText(hex);
      setNote('คัดลอกอัตโนมัติไม่ได้ เลือกข้อความ SHA-256 ไว้ให้แล้ว กรุณาคัดลอกเอง', 'manual');
    }
  }

  async function calc() {
    if (busy || !current()) return;
    busy = true;
    calcAbort = new AbortController();
    setNote('กำลังให้เซิร์ฟเวอร์คำนวณ SHA-256 ไฟล์ใหญ่อาจใช้เวลานาน', 'calc');
    sync();
    try {
      const r = await api('/api/files/' + encodeURIComponent(id) + '/sha256', { signal: calcAbort.signal });
      if (closed) return;
      if (!r || typeof r.sha256 !== 'string' || !HEX64.test(r.sha256)) {
        setNote('เซิร์ฟเวอร์ตอบค่า SHA-256 ที่ไม่ถูกต้อง', 'err');
      } else {
        state.checksums.set(id, r.sha256.toLowerCase());
        setNote('คำนวณเสร็จแล้ว', 'calc');
        refreshFiles();
      }
    } catch (e) {
      if (closed || e.name === 'AbortError') return;
      setNote(calcErrorMessage(e), 'err');
      if (e.status === 404) refreshFiles();
    } finally {
      busy = false;
      calcAbort = null;
      sync();
    }
  }

  function showCmp(kind, icon, text, extra) {
    const ic = el('span', { class: 'ig-ic', 'aria-hidden': 'true' }, icon);
    cmpOut.replaceChildren(...[el('p', { class: 'fd-res ' + kind }, ic, el('span', {}, text)), extra].filter(Boolean));
  }

  function stopCmp(announce) {
    clearInterval(cmpTimer);
    if (cmp && !cmp.finished) cmp.stop();
    const was = cmp;
    cmp = null;
    cmpProg.textContent = '';
    if (was && announce) showCmp('warn', '!', 'ยกเลิกการเทียบไฟล์แล้ว');
    sync();
  }

  function compareLocal(file) {
    const f = current();
    const sum = f && fileSum(f);
    if (!sum) return;
    stopCmp(false);
    cmpOut.replaceChildren();
    const h = spawnHash(file, null, () => {});
    if (!h) { // no Worker: the same hashing method is unavailable, so say so rather than guess
      showCmp('warn', '!', 'เบราว์เซอร์นี้สร้าง Web Worker ไม่ได้ จึงเทียบไฟล์ไม่ได้');
      return;
    }
    cmp = h;
    const tick = () => {
      if (cmp !== h || h.finished) return;
      const pct = h.total > 0 ? Math.min(100, Math.floor((h.done * 100) / h.total)) : 0;
      cmpProg.textContent = `กำลังคำนวณ SHA-256 ${pct}%`;
    };
    tick();
    cmpTimer = setInterval(tick, 250);
    sync();
    h.promise.then((r) => {
      if (cmp !== h) return; // cancelled or replaced
      clearInterval(cmpTimer);
      cmp = null;
      cmpProg.textContent = '';
      if (r.error !== undefined) {
        showCmp('bad', '✕', 'อ่านไฟล์ที่เลือกไม่ได้ จึงเทียบไม่สำเร็จ');
      } else if (r.hex && r.hex.toLowerCase() === sum.toLowerCase()) {
        showCmp('ok', '✓', `ตรงกัน: ไฟล์ในเครื่อง (${file.name}) เหมือนกับไฟล์ในห้อง`);
      } else if (r.hex) {
        showCmp('bad', '✕', `ไม่ตรงกัน: ไฟล์ในเครื่อง (${file.name}) ต่างจากไฟล์ในห้อง`,
          el('p', { class: 'small mut' }, 'SHA-256 ของไฟล์ในเครื่อง ', el('code', { class: 'fd-hex2' }, r.hex)));
      }
      sync();
    });
  }

  pickInput.addEventListener('change', () => {
    const file = pickInput.files && pickInput.files[0];
    pickInput.value = '';
    if (file) compareLocal(file);
  });

  dlg.addEventListener('close', () => {
    closed = true;
    clearTimeout(copyTimer);
    clearInterval(cmpTimer);
    if (cmp && !cmp.finished) cmp.stop();
    cmp = null;
    if (calcAbort) calcAbort.abort();
    dlg.remove();
    if (fileDetail === handle) fileDetail = null;
  });

  const handle = { close: () => dlg.close(), sync };
  fileDetail = handle;
  sync();
  document.body.append(dlg);
  if (typeof dlg.showModal === 'function') dlg.showModal(); else dlg.setAttribute('open', '');
}

// ---------- uploads ----------

let uploadSeq = 0;
// orderSeq numbers every transfer shown in the list (ours and other devices') in the order it
// first appeared. The number stays the same while the row moves around as states change.
let orderSeq = 0;
const MAX_DONE_KEPT = 50; // finished transfers stay in the list; only the oldest ones beyond this go
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
  // "ตรวจ SHA-256" setting; it applies to files queued after the change.
  const verifyBox = el('input', { type: 'checkbox' });
  verifyBox.checked = verifyEnabled();
  verifyBox.addEventListener('change', () => setVerifyEnabled(verifyBox.checked));
  $$('.drop-tools')[0].append(el('label', { class: 'conflict-pick mut small' }, verifyBox, 'ตรวจ SHA-256 ตอนอัปโหลด'));
  setupPending(); // lists the uploads a previous page left unfinished

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
    const item = makeUploadItem(file, dir, conflict);
    state.uploads.unshift(item);
    queue.push(item);
  }
  pump();
  renderTransfers();
  if (!isDesktopLayout()) setView('upload');
}


function makeUploadItem(file, dir, conflict) {
  return {
    id: ++uploadSeq, order: ++orderSeq, file, dir, conflict, loaded: 0, total: file.size, speed: 0, state: 'queued', xhr: null, error: '',
    // resumable engine state (see "resumable uploader" below)
    session: null, // {id, key}: the key lives only here and in localStorage, never in the DOM or a URL
    offset: 0, // last Confirmed_Offset returned by the server
    verify: verifyEnabled(), // the setting applies to files queued after it changes
    hash: null, // SHA-256 job running in a worker, see startHashing
    note: '', // extra line shown to the user, e.g. that the check was skipped
    cancelled: false,
    // auto-resume state (see "recovery" below)
    sched: Logic.createRetryScheduler(), // wait schedule + the 10 minute pause rule
    conflicts: Logic.createStreakCounter(), // 409 offset_mismatch/upload_busy in a row without progress
    notFounds: Logic.createStreakCounter(), // 404 in a row without a confirmed byte
    wake: null, // set while waiting to retry: wake('online' | 'cancel') ends the wait early
    waitUntil: 0, // Date.now() deadline of the current wait, for the countdown
    pauseReason: '', // 'stalled' | 'auth' | 'expired' while state === 'paused'
    resumable: false, // a failed item (507) that keeps its entry and offers "ต่อ"
    needsQuery: false, // a kept session must be queried before the next append
    created: false, // a session was created at least once (the legacy fallback only applies before)
    notice: '', // transient line, e.g. that the session expired and the file restarts
    // continuing an unfinished upload after a reload (see "unfinished uploads" below)
    prefixCheck: false, // compare the first Confirmed_Offset bytes with the server's digest before sending
    queryInfo: null, // body of the latest successful session query, consumed by the prefix check
    prefix: null, // the running prefix hash, see startPrefixHash
  };
}


function pump() {
  while (active < MAX_PARALLEL_UPLOADS && queue.length) {
    const item = queue.shift();
    if (item.state === 'cancelled') continue;
    active++;
    startUpload(item).finally(() => { active--; pump(); });
  }
}

// Legacy one-shot upload (POST /api/files). It cannot resume, so the engine below uses it
// only as a fallback when the server has no /api/uploads endpoint (an older build).
function startLegacyUpload(item) {
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

// ---------- resumable uploader ----------
//
// Flow of one item (entry point: startUpload, called by pump):
//   POST /api/uploads -> remember {id, key, ...} in localStorage -> PATCH chunks one at a time
//   (Blob.slice, <= 16 MiB, offset from the server) while a worker hashes the file in parallel
//   -> POST /api/uploads/{id}/complete {sha256} once every byte is confirmed and the hash is ready.
//
// The pure decisions (chunking, result classification, progress) live in logic.js (Logic).
// The Resume_Key is sent only in the X-Resume-Key header; it is never put in a URL, the DOM,
// a toast or a log line.
//
// Failures: driveUpload loops sendAll -> completeUpload; every failed request goes to
// failUpload, which applies the response table of Req 11 ("recovery" section below) and
// answers 'again' (keep driving) or 'stop' (the item is paused, failed or cancelled).
// The pending store helpers back the "unfinished uploads" list.

const Logic = globalThis.LanShareLogic;
const PENDING_KEY = 'lanshare.uploads.v1';
const VERIFY_KEY = 'lanshare.verify';
const VERIFY_DEFAULT = true; // no phone measurements yet (docs/project-scope.md); the check is on unless the user turns it off
const HASH_JOB = 1; // each item owns its worker, so one job id is enough
const uploadBySession = new Map(); // session id -> item, to attribute our own `transfer` events

const verifyEnabled = () => {
  try {
    const v = localStorage.getItem(VERIFY_KEY);
    return v === null ? VERIFY_DEFAULT : v !== '0';
  } catch { return VERIFY_DEFAULT; }
};

function setVerifyEnabled(on) {
  try { localStorage.setItem(VERIFY_KEY, on ? '1' : '0'); } catch { /* the setting just does not persist */ }
}

// ---- pending store (localStorage lanshare.uploads.v1) ----
// {[sessionId]: {id, key, name, size, lastModified, dir, conflict, roomId, createdAt}}

function readPending() {
  try {
    const v = JSON.parse(localStorage.getItem(PENDING_KEY) || '{}');
    return v && typeof v === 'object' && !Array.isArray(v) ? v : {};
  } catch { return {}; }
}

function writePending(map) {
  try {
    if (Object.keys(map).length === 0) localStorage.removeItem(PENDING_KEY);
    else localStorage.setItem(PENDING_KEY, JSON.stringify(map));
    return true;
  } catch { return false; } // storage full or blocked: the upload still works in this page
}

function putPending(entry) {
  const map = readPending();
  map[entry.id] = entry;
  return writePending(map);
}

function dropPending(id) {
  const map = readPending();
  if (!(id in map)) return;
  delete map[id];
  writePending(map);
}

// ---- requests ----

function parseResult(status, text, contentType) {
  let body = null;
  if (text && (contentType || '').includes('application/json')) {
    try { body = JSON.parse(text); } catch { body = null; }
  }
  return {
    status,
    ok: status >= 200 && status < 300,
    code: body && typeof body.error === 'string' ? body.error : '',
    body,
  };
}

// uploadRequest never throws: it resolves {status, ok, code, body} or {networkError: true}.
// `key` goes in X-Resume-Key; `json`, when given, is the JSON body.
// A request that gets no response within NO_PROGRESS_TIMEOUT_MS counts as a network error.
async function uploadRequest(method, path, key, json) {
  const headers = { 'X-LanShare-Client': '1' };
  if (key) headers['X-Resume-Key'] = key;
  let body;
  if (json !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(json);
  }
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), Logic.NO_PROGRESS_TIMEOUT_MS);
  try {
    const res = await fetch(path, { method, headers, body, credentials: 'same-origin', signal: ctl.signal });
    const text = await res.text().catch(() => '');
    return parseResult(res.status, text, res.headers.get('content-type'));
  } catch { return { networkError: true }; } finally { clearTimeout(timer); }
}

const sessionPath = (session) => '/api/uploads/' + encodeURIComponent(session.id);

// sendChunk sends file[chunk.start, chunk.end) as one PATCH. Resolves like uploadRequest,
// {noProgress: true} when no upload progress or response arrived for NO_PROGRESS_TIMEOUT_MS
// (the request is aborted), or {aborted: true} when the item was cancelled meanwhile.
function sendChunk(item, chunk) {
  return new Promise((resolve) => {
    const xhr = new XMLHttpRequest();
    item.xhr = xhr;
    let lastT = performance.now();
    let lastB = 0;
    let stalled = false;
    let dog = 0;
    const disarm = () => clearTimeout(dog);
    const arm = () => { // restarted by every sign of life
      disarm();
      dog = setTimeout(() => { stalled = true; xhr.abort(); }, Logic.NO_PROGRESS_TIMEOUT_MS);
    };
    xhr.open('PATCH', sessionPath(item.session) + '?offset=' + chunk.start);
    xhr.setRequestHeader('X-LanShare-Client', '1');
    xhr.setRequestHeader('X-Resume-Key', item.session.key);
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    // Only the speed comes from the browser; the progress bar moves with offsets the server
    // confirmed (responses and our own `transfer` events).
    xhr.upload.onprogress = (e) => {
      arm();
      const now = performance.now();
      if (now - lastT >= 500) {
        item.speed = ((e.loaded - lastB) / (now - lastT)) * 1000;
        lastT = now;
        lastB = e.loaded;
        renderTransfersSoon();
      }
    };
    xhr.upload.onload = arm; // body sent; now waiting for the answer
    xhr.onload = () => {
      disarm();
      item.xhr = null;
      resolve(parseResult(xhr.status, xhr.responseText, xhr.getResponseHeader('content-type')));
    };
    xhr.onerror = () => { disarm(); item.xhr = null; resolve({ networkError: true }); };
    xhr.onabort = () => { disarm(); item.xhr = null; resolve(stalled ? { noProgress: true } : { aborted: true }); };
    arm();
    xhr.send(item.file.slice(chunk.start, chunk.end));
  });
}

// ---- session lifecycle ----

// createSession asks for a session and, on success, stores it in memory and in localStorage
// before any byte is sent. Resolves with the response (check .ok).
async function createSession(item) {
  const req = { name: item.file.name, size: item.total };
  if (item.dir) req.dir = item.dir;
  if (item.conflict) req.conflict = item.conflict;
  const res = await uploadRequest('POST', '/api/uploads', null, req);
  if (!res.ok) return res;
  const b = res.body || {};
  if (typeof b.id !== 'string' || typeof b.resumeKey !== 'string' || !Number.isSafeInteger(b.offset)) {
    return { status: res.status, ok: false, code: 'bad_response', body: null };
  }
  item.session = { id: b.id, key: b.resumeKey };
  item.offset = b.offset;
  uploadBySession.set(b.id, item);
  putPending({
    id: b.id, key: b.resumeKey, name: item.file.name, size: item.total, lastModified: item.file.lastModified,
    dir: item.dir, conflict: item.conflict, roomId: state.room.id, createdAt: Date.now(),
  });
  return res;
}

// forgetSession drops the local trace of a session that is finished, cancelled or gone.
function forgetSession(item) {
  if (!item.session) return;
  uploadBySession.delete(item.session.id);
  dropPending(item.session.id);
}

// cancelRemote asks the server to discard a session. Best effort, one request, result ignored.
function cancelRemote(session) {
  uploadRequest('DELETE', sessionPath(session), session.key);
}

// ---- SHA-256 in a worker ----

const hashSkipNote = (why) => 'ข้ามการตรวจ SHA-256 (' + why + ')';

// addNote appends a line to the extra text of an item without losing an earlier one.
const addNote = (item, text) => { item.note = item.note ? item.note + ' • ' + text : text; };

// startHashing begins hashing item.file in a Web Worker, in parallel with the upload.
// item.hash.promise resolves {hex} | {error} | {cancelled}; it never rejects. When the worker
// cannot be created or fails the upload goes on without a checksum and item.note says so.
function startHashing(item) {
  if (!item.verify || item.hash) return;
  const h = spawnHash(item.file, null, (result) => {
    if (result.error) addNote(item, hashSkipNote('อ่านไฟล์ไม่ได้'));
  });
  if (!h) {
    addNote(item, hashSkipNote('เบราว์เซอร์ไม่รองรับ'));
    return;
  }
  item.hash = h;
}

// spawnHash hashes file bytes [range.start, range.end) (the whole file when range is null) in a
// fresh Web Worker. Returns null when the worker cannot be created, otherwise
// {done, total, finished, result, stop, promise}; promise resolves {hex} | {error} | {cancelled}
// and never rejects. `onEnd(result)` runs once, before the promise resolves.
function spawnHash(file, range, onEnd) {
  let worker;
  try {
    worker = new Worker('/hash-worker.js');
  } catch {
    return null;
  }
  const h = { done: 0, total: range ? range.end - range.start : file.size, finished: false, result: null, stop: null, promise: null };
  h.promise = new Promise((resolve) => {
    const end = (result) => {
      if (h.finished) return;
      h.finished = true;
      h.result = result;
      worker.terminate();
      onEnd(result);
      resolve(result);
      renderTransfersSoon();
    };
    h.stop = () => end({ cancelled: true });
    worker.onmessage = (ev) => {
      const m = ev.data || {};
      if (m.id !== HASH_JOB) return;
      if (typeof m.hex === 'string') end({ hex: m.hex });
      else if (m.error !== undefined) end({ error: String(m.error) });
      else if (Number.isFinite(m.done) && Number.isFinite(m.total)) {
        h.done = m.done;
        h.total = m.total;
        renderTransfersSoon();
      }
    };
    worker.onerror = (ev) => { ev.preventDefault(); end({ error: 'worker' }); };
    try {
      const msg = { id: HASH_JOB, file };
      if (range) { msg.start = range.start; msg.end = range.end; }
      worker.postMessage(msg);
    } catch { end({ error: 'post' }); }
  });
  return h;
}

function stopHashing(item) {
  if (item.hash && !item.hash.finished) item.hash.stop();
  if (item.prefix && !item.prefix.finished) item.prefix.stop();
}

// hashPercent is the whole-number progress of a running hash, or null when none is running.
function hashPercent(item) {
  const h = item.hash;
  if (!h || h.finished) return null;
  return h.total > 0 ? Math.min(100, Math.floor((h.done * 100) / h.total)) : 0;
}

// ---- engine ----

// startUpload is the entry point used by pump, for new items and for resumed ones. It never rejects.
async function startUpload(item) {
  try {
    await runUpload(item);
  } catch {
    settle(item, 'failed', 'เกิดข้อผิดพลาดภายในหน้าเว็บ');
  }
}

async function runUpload(item) {
  // an item that kept its session (resumed after a pause or a 507) first asks where the server is
  item.state = item.session ? 'reconnecting' : 'running';
  item.speed = 0;
  item.error = '';
  renderTransfers();
  await driveUpload(item);
}

// driveUpload makes sure there is a session, sends every byte, then completes. Every failed
// request goes through failUpload, which says whether to go round again. Resolves once the item
// is done, paused, failed or cancelled.
async function driveUpload(item) {
  let res = null; // the failed result to react to, or null
  for (;;) {
    if (item.cancelled) return undefined;
    if (!item.session) {
      const created = await createSession(item);
      if (item.cancelled) { // cancelled while the session was being created
        if (created.ok) { cancelRemote(item.session); forgetSession(item); }
        return undefined;
      }
      if (created.ok) item.created = true;
      else if (!item.created && (created.status === 404 || created.status === 405)) return startLegacyUpload(item);
      else res = created;
    } else if (item.needsQuery) {
      item.needsQuery = false;
      const q = await queryUpload(item);
      if (item.cancelled) return undefined;
      if (q.ok) adoptOffset(item, q.offset);
      else res = q;
    }
    if (!res && item.prefixCheck && item.queryInfo) {
      const v = await verifyPrefix(item); // before any byte is sent or hashed for the upload
      if (v === 'stop') return undefined;
      if (v === 'restart') continue; // the session was discarded: go round and create a new one
    }
    if (!res) {
      startHashing(item); // no-op when a hash is already running or finished
      res = await sendAll(item);
      if (item.cancelled) return undefined;
    }
    if (!res) {
      res = await completeUpload(item);
      if (!res) return undefined; // done, mismatch or cancelled
    }
    if ((await failUpload(item, res)) !== 'again') return undefined;
    res = null;
  }
}

// ---- unfinished uploads (Req 10.5-10.7, 11.9, 11.10) ----

// startPrefixHash hashes the first `end` bytes of item.file. null when no worker can be made.
function startPrefixHash(item, end) {
  const p = spawnHash(item.file, { start: 0, end }, () => {});
  if (p) item.prefix = p;
  return p;
}

// prefixPercent is the whole-number progress of a running prefix hash, or null when none runs.
function prefixPercent(item) {
  const p = item.prefix;
  if (!p || p.finished) return null;
  return p.total > 0 ? Math.min(100, Math.floor((p.done * 100) / p.total)) : 0;
}

// verifyPrefix runs once for an item that continues a stored session, right after a successful
// query. It checks that the re-selected file is the one that was partly sent: the size must
// equal the session's size and, when verification is on and bytes exist, the SHA-256 of
// [0, offset) must equal the server's digest (case-insensitive). A worker that cannot be created
// skips the comparison and says so. Resolves 'ok' (send on), 'restart' (the old session was
// deleted; create a new one from 0) or 'stop' (cancelled, or the file could not be read and the
// item is failed with its entry kept so "ต่อ" can try again).
async function verifyPrefix(item) {
  const info = item.queryInfo;
  item.queryInfo = null;
  item.prefixCheck = false;
  let same = info.size === item.file.size;
  if (same && info.offset > 0 && item.verify) {
    const p = startPrefixHash(item, info.offset);
    if (!p) {
      addNote(item, 'ข้ามการเทียบไฟล์กับส่วนที่ส่งไปแล้ว (เบราว์เซอร์ไม่รองรับ)');
    } else {
      const r = await p.promise;
      item.prefix = null;
      if (item.cancelled || r.cancelled) return 'stop';
      if (r.error !== undefined) {
        item.prefixCheck = true; // "ต่อ" asks the server again and compares again
        settle(item, 'failed', 'อ่านไฟล์ไม่ได้ ลองเลือกไฟล์ใหม่หรือกด "ต่อ"');
        item.resumable = true;
        return 'stop';
      }
      same = typeof info.sha256 === 'string' && r.hex.toLowerCase() === info.sha256.toLowerCase();
    }
  }
  if (same) return 'ok';
  // Not the same file: delete the old session whatever the answer is (404 included), then start over.
  const old = item.session;
  item.session = null; // a cancel during the DELETE must not send a second one
  await uploadRequest('DELETE', sessionPath(old), old.key);
  uploadBySession.delete(old.id);
  dropPending(old.id);
  if (item.cancelled) return 'stop';
  item.needsQuery = false;
  item.offset = 0;
  item.loaded = 0; // a new session: the bar may go back
  item.notice = 'ไฟล์ที่เลือกไม่ตรงกับไฟล์ที่ส่งไปบางส่วน จึงเริ่มส่งใหม่จากต้นไฟล์';
  return 'restart';
}

// sendAll sends everything after item.offset, one chunk at a time, each starting at the offset
// the server last confirmed. Resolves null when all bytes are confirmed (or the item was
// cancelled), otherwise the failed result.
async function sendAll(item) {
  let offset = item.offset;
  for (;;) {
    const chunk = Logic.nextChunk(offset, item.total);
    if (!chunk) return null;
    const res = await sendChunk(item, chunk);
    if (res.aborted || item.cancelled) return null;
    if (!res.ok) return res;
    const next = res.body && res.body.offset;
    if (!Number.isSafeInteger(next) || next <= offset || next > item.total) {
      return { status: res.status, ok: false, code: 'bad_response', body: null };
    }
    offset = next;
    item.offset = next;
    item.loaded = Logic.nextProgress(item.loaded, next);
    progressed(item);
    renderTransfersSoon();
  }
}

// completeUpload waits for the hash, then publishes the file. Resolves null when the item
// reached a final state, otherwise the failed result (tagged phase: 'complete').
async function completeUpload(item) {
  item.state = 'completing';
  item.speed = 0;
  renderTransfers();
  const hashed = item.hash ? await item.hash.promise : null;
  if (item.cancelled) return null;
  const body = {};
  if (hashed && typeof hashed.hex === 'string') body.sha256 = hashed.hex;
  const res = await uploadRequest('POST', sessionPath(item.session) + '/complete', item.session.key, body);
  if (item.cancelled) return null;
  if (res.ok) { // 201, or 200 when the server had already completed this session
    forgetSession(item);
    settle(item, 'done');
    return null;
  }
  if (Logic.isChecksumMismatch(res)) { // the server dropped the session; the user decides to resend
    forgetSession(item);
    settle(item, 'mismatch', 'ไฟล์ไม่ตรงกัน');
    return null;
  }
  res.phase = 'complete';
  return res;
}

// ---- recovery (Req 11, response table in the design) ----

const validOffset = (item, off) => Number.isSafeInteger(off) && off >= 0 && off <= item.total;

// progressed runs whenever the Confirmed_Offset increases: waits start again at 1 second, the
// streaks end and the "reconnecting" state is over.
function progressed(item) {
  item.sched.progress(Date.now());
  item.conflicts.reset();
  item.notFounds.reset();
  if (item.state === 'reconnecting') item.state = 'running';
  item.error = '';
}

// adoptOffset takes an offset the server just reported as the truth. The bar then shows exactly
// that value (Req 11.11); an increase counts as progress.
function adoptOffset(item, off) {
  const increased = off > item.offset;
  item.offset = off;
  item.loaded = off;
  if (increased) progressed(item);
  renderTransfersSoon();
}

// queryUpload asks the server for the durable offset. Resolves like uploadRequest; when ok,
// `.offset` is the validated Confirmed_Offset.
async function queryUpload(item) {
  const res = await uploadRequest('GET', sessionPath(item.session), item.session.key);
  if (!res.ok) return res;
  const off = res.body && res.body.offset;
  if (!validOffset(item, off)) return { status: res.status, ok: false, code: 'bad_response', body: null };
  res.offset = off;
  item.queryInfo = res.body; // the prefix check of a continued upload needs size and sha256 from here
  return res;
}

// reconnect brings a session back after a failure: creates one when the old one is gone,
// otherwise queries it and adopts the returned offset. Resolves with the request result.
async function reconnect(item) {
  if (!item.session) {
    const created = await createSession(item);
    if (created.ok) {
      item.created = true;
      if (item.cancelled) { cancelRemote(item.session); forgetSession(item); }
    }
    return created;
  }
  const q = await queryUpload(item);
  if (q.ok) adoptOffset(item, q.offset);
  return q;
}

// failUpload is the single place that reacts to a failed request (the table of Req 11).
// Resolves 'again' when the drive loop should carry on, 'stop' when the item has been paused,
// failed or cancelled.
async function failUpload(item, res) {
  const A = Logic.ACTION;
  if (item.cancelled) return 'stop';
  // a complete that finds the session short means our offset is stale, same as offset_mismatch
  if (res.phase === 'complete' && res.status === 409 && res.code === 'incomplete') res = { ...res, code: 'offset_mismatch' };
  switch (Logic.classifyResult(res, { conflictStreak: item.conflicts.count() })) {
    case A.CONTINUE_NOW: return continueNow(item, res);
    case A.RETRY: return retryLoop(item, res);
    case A.NEW_SESSION: return restartSession(item, res);
    case A.PAUSE_AUTH:
      pauseItem(item, 'auth', 'หมดเวลาเข้าสู่ระบบ กรุณาเข้าห้องใหม่ ระบบจะส่งต่อให้เอง');
      confirmSignedOut();
      return 'stop';
    case A.STOP_KEEP_ENTRY: // 507: keep the entry so "ต่อ" works after space is freed
      settle(item, 'failed', failMessage(res));
      item.resumable = true;
      return 'stop';
    default: // 422 and every other 4xx: no retry; nobody will use the session again
      if (item.session && ![403, 404, 422].includes(res.status)) cancelRemote(item.session);
      forgetSession(item);
      settle(item, 'failed', failMessage(res));
      return 'stop';
  }
}

// 409 offset_mismatch / upload_busy: carry on at once from the server's offset. Three in a row
// without progress make classifyResult answer RETRY instead (the streak is kept until progress).
async function continueNow(item, res) {
  item.conflicts.hit();
  const off = res.body && res.body.offset;
  if (validOffset(item, off)) {
    adoptOffset(item, off);
    return 'again';
  }
  const q = await queryUpload(item); // the response carried no usable offset
  if (item.cancelled) return 'stop';
  if (!q.ok) return failUpload(item, q);
  adoptOffset(item, q.offset);
  return 'again';
}

// Network error, no progress for 45 s, 408 and 5xx: wait 1, 2, 4, 8, 15, 15… s, then query the
// session (or just send complete again) and go on. Pauses after 10 minutes without progress.
async function retryLoop(item, failure) {
  let cur = failure;
  for (;;) {
    const f = item.sched.failure(Date.now());
    if (f.pause) {
      pauseItem(item, 'stalled', 'ไม่มีความคืบหน้านาน 10 นาที');
      return 'stop';
    }
    item.state = 'reconnecting';
    item.error = '';
    await waitToRetry(item, f.waitSec);
    if (item.cancelled) return 'stop';
    // The lost answer may have been the one to complete: send complete again with the same key
    // (no query), the server answers repeated completes for a while.
    if (cur.phase === 'complete') return 'again';
    const r = await reconnect(item);
    if (item.cancelled) return 'stop';
    if (r.ok) return 'again';
    if (Logic.classifyResult(r) === Logic.ACTION.RETRY) { cur = r; continue; }
    return failUpload(item, r);
  }
}

// waitToRetry sleeps `sec` seconds; item.wake(why) ends the wait early ('online', 'cancel').
// item.waitUntil drives the countdown shown in the list.
function waitToRetry(item, sec) {
  return new Promise((resolve) => {
    const ms = sec * 1000;
    item.waitUntil = Date.now() + ms;
    const tick = setInterval(renderTransfersSoon, 1000);
    const timer = setTimeout(() => end('timer'), ms);
    function end(why) {
      clearTimeout(timer);
      clearInterval(tick);
      item.wake = null;
      item.waitUntil = 0;
      renderTransfersSoon();
      resolve(why);
    }
    item.wake = end;
    renderTransfers();
  });
}

// 404: the session is gone. Forget it and start a new one from byte 0; the third 404 in a row
// without a confirmed byte pauses the item instead.
async function restartSession(item, res) {
  if (res.phase === 'complete') {
    // After complete we cannot tell "expired" from "already published and the 10 minute
    // record is gone", so do not start another copy: refresh the list and let the user look.
    forgetSession(item);
    refreshFiles();
    settle(item, 'failed', 'ไม่พบการอัปโหลดนี้บนเซิร์ฟเวอร์ ตรวจรายการไฟล์ว่าบันทึกแล้วหรือไม่');
    return 'stop';
  }
  const streak = item.notFounds.hit();
  forgetSession(item);
  item.session = null;
  item.needsQuery = false;
  item.offset = 0;
  item.loaded = 0; // the one place the bar may go back (Req 11.11)
  if (Logic.notFoundAction(streak) === 'pause') {
    pauseItem(item, 'expired', 'การอัปโหลดหมดอายุซ้ำหลายครั้ง');
    return 'stop';
  }
  item.notice = 'การอัปโหลดหมดอายุ เริ่มส่งใหม่จากต้นไฟล์';
  return 'again';
}

// pauseItem stops automatic work and offers "ต่อ". The stored entry (and its key) stay.
function pauseItem(item, reason, message) {
  if (item.cancelled) return;
  item.xhr = null;
  item.state = 'paused';
  item.pauseReason = reason;
  item.error = message;
  item.speed = 0;
  renderTransfers();
}

// resumeUpload is the "ต่อ" action (and what a re-join triggers): back into the queue, asking
// the server for the offset first when the session is still known.
function resumeUpload(item) {
  const canResume = item.state === 'paused' || (item.state === 'failed' && item.resumable);
  if (item.cancelled || !canResume) return;
  item.state = 'queued';
  item.error = '';
  item.pauseReason = '';
  item.resumable = false;
  item.needsQuery = !!item.session;
  item.sched.reset();
  item.conflicts.reset();
  item.notFounds.reset();
  // a hash stopped by a failure starts over; a finished or running one is kept
  if (item.hash && item.hash.result && item.hash.result.cancelled) item.hash = null;
  queue.push(item);
  pump();
  renderTransfers();
}

// ---- re-join after a 401 (Req 11.6, 11.14) ----

let rejoining = false;

const hasAuthPausedUploads = () => state.uploads.some((it) => it.state === 'paused' && it.pauseReason === 'auth');

// confirmSignedOut asks the server whether the device token is really gone before moving the
// user to the PIN screen, so a stray 401 does not throw them out.
async function confirmSignedOut() {
  try { await api('/api/me'); } catch (e) { if (e.status === 401) promptRejoin(); }
}

// promptRejoin shows the PIN form without reloading, so the File objects of paused uploads survive.
function promptRejoin() {
  if (rejoining) return;
  rejoining = true;
  showJoin().catch(() => { rejoining = false; });
}

// resumeAfterRejoin runs at the end of enter(): the paused uploads query their sessions and go on.
function resumeAfterRejoin() {
  rejoining = false;
  for (const it of state.uploads) {
    if (it.state === 'paused' && it.pauseReason === 'auth') resumeUpload(it);
  }
}

// `online`: stop waiting and retry now (the wait starts again at 1 s). Paused items ignore it.
window.addEventListener('online', () => {
  for (const it of state.uploads) {
    if (it.state === 'reconnecting' && it.wake) {
      it.sched.resetWait();
      it.wake('online');
    }
  }
});

function failMessage(res) {
  if (res.networkError || res.noProgress) return 'การเชื่อมต่อขัดข้อง';
  if (res.code === 'bad_response') return 'คำตอบจากเซิร์ฟเวอร์ไม่ถูกต้อง';
  const b = res.body || {};
  switch (res.status) {
    case 400: return 'ชื่อหรือขนาดไฟล์ไม่ถูกต้อง';
    case 401: return 'หมดเวลาเข้าสู่ระบบ';
    case 403: return 'ไม่มีสิทธิ์ทำรายการนี้';
    case 404: return 'การอัปโหลดนี้หมดอายุหรือถูกยกเลิกแล้ว';
    case 408: return 'ไม่ได้รับข้อมูลนานเกินไป';
    case 413: return 'ขนาดไฟล์เกินที่แจ้งไว้';
    case 429: return 'มีการอัปโหลดค้างอยู่มากเกินไป';
    case 507:
      return Number.isFinite(b.requiredBytes) && Number.isFinite(b.availableBytes)
        ? Logic.insufficientStorageMessage(b.requiredBytes, b.availableBytes)
        : 'พื้นที่ดิสก์ไม่พอ';
    default: return 'ผิดพลาด (' + res.status + ')';
  }
}

// settle puts an item into a final state. A cancelled item stays cancelled.
function settle(item, stateName, error) {
  if (item.cancelled) return;
  stopHashing(item);
  item.xhr = null;
  item.state = stateName;
  item.speed = 0;
  item.error = error || '';
  item.resumable = false; // failUpload sets it again for 507
  if (stateName === 'done') item.loaded = item.total;
  renderTransfers();
  if (stateName === 'done') refreshFiles();
}

// cancelUpload stops an item for good: aborts the running request, ends any retry wait, stops
// hashing, asks the server once to discard the session and forgets it locally even if that
// request fails.
function cancelUpload(item) {
  if (item.cancelled || item.state === 'done') return;
  item.cancelled = true;
  item.state = 'cancelled';
  item.speed = 0;
  if (item.xhr) item.xhr.abort();
  if (item.wake) item.wake('cancel');
  stopHashing(item);
  if (item.session) {
    cancelRemote(item.session);
    forgetSession(item);
  }
  renderTransfers();
}

// resendUpload restarts an item whose checksum did not match: hash again from byte 0 and
// create a new session (never automatic).
function resendUpload(item) {
  if (item.state !== 'mismatch') return;
  Object.assign(item, {
    session: null, offset: 0, loaded: 0, speed: 0, state: 'queued', error: '', hash: null, note: '', notice: '',
    verify: verifyEnabled(), needsQuery: false, created: false, resumable: false, pauseReason: '',
  });
  item.sched.reset();
  item.conflicts.reset();
  item.notFounds.reset();
  queue.push(item);
  pump();
  renderTransfers();
}

// ---------- unfinished uploads list ("อัปโหลดที่ค้างอยู่") ----------

// Entries of lanshare.uploads.v1 that belong to this room and are not being driven by this page
// (a page that was reloaded or closed). The Resume_Key is read from them but never displayed.
const pendingNotes = new Map(); // entry id -> message under the row (e.g. "ไฟล์ไม่ตรง")
let pendingPickId = ''; // entry the file picker was opened for
let pendingInput = null;
let pendingSig = null; // null forces the next render

function pendingEntries() {
  if (!state.room) return [];
  return Object.values(readPending())
    .filter((e) => e && typeof e === 'object' && typeof e.id === 'string' && typeof e.key === 'string'
      && typeof e.name === 'string' && Number.isSafeInteger(e.size) && e.size >= 0
      && e.roomId === state.room.id && !uploadBySession.has(e.id))
    .sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0));
}

function setPendingNote(id, text) {
  if (text) pendingNotes.set(id, text);
  else pendingNotes.delete(id);
  pendingSig = null;
  renderPending();
}

function setupPending() {
  if (pendingInput) return;
  pendingInput = el('input', { type: 'file', hidden: true });
  pendingInput.addEventListener('change', () => {
    const file = pendingInput.files[0];
    const id = pendingPickId;
    pendingPickId = '';
    pendingInput.value = '';
    if (file && id) pickPendingFile(id, file);
  });
  const card = el('div', { id: 'pending-card', class: 'card', hidden: true },
    el('h2', {}, 'อัปโหลดที่ค้างอยู่'),
    el('p', { class: 'mut small' }, 'เลือกไฟล์เดิมเพื่อส่งต่อจากจุดที่ค้างไว้'),
    el('ul', { id: 'pending', class: 'list' }),
    pendingInput,
  );
  $('transfers-card').before(card);
  renderPending();
}

// renderPending redraws the list only when its content changed, so a button keeps its focus
// while transfer progress repaints the page many times a second.
function renderPending() {
  if (!pendingInput) return;
  const entries = pendingEntries();
  const sig = entries.map((e) => e.id + '|' + (pendingNotes.get(e.id) || '')).join('\n');
  if (sig === pendingSig) return;
  pendingSig = sig;
  $('pending-card').hidden = entries.length === 0;
  $('pending').replaceChildren(...entries.map((e) => {
    const note = pendingNotes.get(e.id);
    const meta = formatBytes(e.size) + ' • ส่งค้างอยู่';
    return el('li', {},
      el('span', {}, '⏳'),
      el('div', { class: 'grow' },
        el('div', { class: 'ellipsis' }, (e.dir ? e.dir + '/' : '') + e.name),
        el('div', { class: 'xfer-meta' }, meta),
        note ? el('div', { class: 'xfer-meta bad' }, note) : null,
      ),
      el('span', { class: 'xfer-actions' },
        el('button', { class: 'btn ghost sm', type: 'button', onclick: () => choosePendingFile(e.id) }, 'เลือกไฟล์เดิม'),
        el('button', { class: 'btn ghost sm', type: 'button', title: 'ยกเลิก', onclick: () => cancelPending(e.id) }, '✕'),
      ),
    );
  }));
}

function choosePendingFile(id) {
  pendingPickId = id;
  pendingInput.click();
}

// pickPendingFile accepts the chosen file only when its name and size match the stored entry
// (the last modified time is not compared). Otherwise the entry stays and a message says so.
function pickPendingFile(id, file) {
  const entry = pendingEntries().find((e) => e.id === id);
  if (!entry) return;
  if (!Logic.matchPendingUpload(entry, file)) {
    setPendingNote(id, `ไฟล์ไม่ตรง: ต้องเป็นไฟล์ชื่อ "${entry.name}" ขนาด ${formatBytes(entry.size)} (${entry.size} ไบต์)`);
    toast('ไฟล์ไม่ตรง');
    return;
  }
  setPendingNote(id, '');
  const item = makeUploadItem(file, entry.dir || '', entry.conflict || '');
  item.session = { id: entry.id, key: entry.key };
  item.created = true;
  item.needsQuery = true; // driveUpload asks the server for the offset first
  item.prefixCheck = true; // then compares the file with what was already sent
  uploadBySession.set(entry.id, item);
  state.uploads.unshift(item);
  queue.push(item);
  pump();
  renderTransfers();
  if (!isDesktopLayout()) setView('upload');
}

// cancelPending discards a stored session that no running upload owns: one DELETE, and the
// entry goes away whether or not the request succeeds.
function cancelPending(id) {
  const entry = pendingEntries().find((e) => e.id === id);
  if (!entry) return;
  dropPending(id);
  pendingNotes.delete(id);
  uploadRequest('DELETE', sessionPath(entry), entry.key);
  pendingSig = null;
  renderPending();
}

// ---------- remote transfers (other devices) ----------

const remoteTimers = new Map();

function onRemoteTransfer(p) {
  if (p.deviceId === state.me.id) { // our own transfers are shown as local items, not as remote rows
    onOwnTransfer(p);
    return;
  }
  const key = p.direction + ':' + p.id + ':' + p.deviceId;
  const prev = state.remote.get(key);
  p.order = prev ? prev.order : ++orderSeq; // later events of the same transfer keep their number
  state.remote.set(key, p);
  clearTimeout(remoteTimers.get(key));
  remoteTimers.delete(key);
  // `running`, `interrupted` and `done` stay on screen (`interrupted` until the next event with
  // the same key replaces it, `done` as history); a failed transfer clears itself.
  if (p.state !== 'running' && p.state !== 'interrupted' && p.state !== 'done') {
    remoteTimers.set(key, setTimeout(() => { state.remote.delete(key); renderTransfers(); }, 8000));
  }
  renderTransfersSoon();
}

// A `transfer` event of one of our own resumable uploads (its id is the session id) carries the
// bytes the server has written so far. Use it to move the bar between chunk confirmations; the
// value never lowers the progress already shown.
function onOwnTransfer(p) {
  if (p.direction !== 'upload' || (p.state !== 'running' && p.state !== 'interrupted')) return;
  const item = uploadBySession.get(p.id);
  if (!item || (item.state !== 'running' && item.state !== 'completing')) return;
  item.loaded = Logic.nextProgress(item.loaded, Math.min(p.bytes, item.total));
  renderTransfersSoon();
}

// ---------- transfer list rendering ----------

let raf = 0;
function renderTransfersSoon() {
  if (raf) return;
  raf = requestAnimationFrame(() => { raf = 0; renderTransfers(); });
}

// pruneFinished drops the oldest finished transfers beyond MAX_DONE_KEPT so a long session does
// not grow the list (and the memory behind it) without limit. Running ones are never touched.
function pruneFinished() {
  const done = [
    ...state.uploads.filter((it) => it.state === 'done').map((it) => ({ order: it.order, drop: () => state.uploads.splice(state.uploads.indexOf(it), 1) })),
    ...[...state.remote].filter(([, p]) => p.state === 'done').map(([key, p]) => ({ order: p.order, drop: () => state.remote.delete(key) })),
  ];
  if (done.length <= MAX_DONE_KEPT) return;
  done.sort((a, b) => a.order - b.order);
  for (const d of done.slice(0, done.length - MAX_DONE_KEPT)) d.drop();
}

function transferRow({ order, icon, name, detail, pct, barClass, action }) {
  const fill = el('i', { class: barClass || '' });
  fill.style.width = Math.max(0, Math.min(100, pct)) + '%'; // CSSOM is allowed by our CSP
  return el('li', {},
    el('span', { class: 'xfer-no', title: 'ลำดับที่ ' + order }, '#' + order),
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
  pruneFinished();
  const entries = []; // {state, order, row}: sorted below so running transfers come first

  for (const it of state.uploads) {
    if (it.state === 'cancelled') continue;
    const pct = it.total > 0 ? (it.loaded / it.total) * 100 : (it.state === 'done' ? 100 : 0);
    const hp = hashPercent(it);
    const hashText = hp === null ? '' : `กำลังคำนวณ SHA-256 ${hp}%`;
    const progress = `⬆ ${formatBytes(it.loaded)} / ${formatBytes(it.total)} • ${pct.toFixed(0)}%`;
    let detail;
    let barClass = '';
    if (it.state === 'queued') detail = 'รอคิว • ' + formatBytes(it.total);
    else if (it.state === 'running') {
      detail = `${progress} • ${formatBytes(it.speed)}/s`;
      if (hashText) detail += ' • ' + hashText;
    } else if (it.state === 'reconnecting') {
      const left = it.waitUntil > 0 ? Math.max(1, Math.ceil((it.waitUntil - Date.now()) / 1000)) : 0;
      detail = `${progress} • กำลังเชื่อมต่อใหม่ • ` + (left > 0 ? `ลองใหม่ใน ${left} วินาที` : 'กำลังต่อ…');
      if (hashText) detail += ' • ' + hashText;
    } else if (it.state === 'paused') {
      detail = `⏸ หยุดชั่วคราว • ${it.error} • ${progress}`;
      if (hashText) detail += ' • ' + hashText;
    } else if (it.state === 'completing') {
      detail = hashText ? 'ส่งครบแล้ว • ' + hashText : 'ส่งครบแล้ว กำลังบันทึกไฟล์…';
    } else if (it.state === 'done') { detail = '✓ ส่งสำเร็จ • ' + formatBytes(it.total); barClass = 'done'; }
    else if (it.state === 'mismatch') { detail = '✗ ' + it.error; barClass = 'failed'; }
    else { detail = '✗ ' + it.error; barClass = 'failed'; }
    const pp = prefixPercent(it);
    if (pp !== null) detail = `กำลังเทียบไฟล์กับส่วนที่ส่งไปแล้ว ${pp}%`;
    if (it.notice) detail += ' • ' + it.notice;
    if (it.note) detail += ' • ' + it.note;
    const cancellable = ['queued', 'running', 'reconnecting', 'paused', 'completing'].includes(it.state)
      || (it.state === 'failed' && it.resumable);
    const buttons = [];
    if (it.state === 'paused' || (it.state === 'failed' && it.resumable)) {
      buttons.push(el('button', { class: 'btn ghost sm', type: 'button', onclick: () => resumeUpload(it) }, 'ต่อ'));
    }
    if (cancellable) {
      buttons.push(el('button', { class: 'btn ghost sm', type: 'button', title: 'ยกเลิก', onclick: () => cancelUpload(it) }, '✕'));
    } else if (it.state === 'mismatch') {
      buttons.push(el('button', { class: 'btn ghost sm', type: 'button', onclick: () => resendUpload(it) }, 'ส่งใหม่'));
    }
    const action = buttons.length > 1 ? el('span', {}, ...buttons) : (buttons[0] || null);
    entries.push({
      state: it.state,
      order: it.order,
      row: transferRow({
        order: it.order, icon: '⬆️', name: (it.dir ? it.dir + '/' : '') + it.file.name, detail, pct, barClass, action,
      }),
    });
  }

  for (const p of state.remote.values()) {
    const pct = p.total > 0 ? (p.bytes / p.total) * 100 : 0;
    const arrow = p.direction === 'upload' ? '⬆' : '⬇';
    let detail = `${arrow} ${p.deviceName} • ${formatBytes(p.bytes)}${p.total > 0 ? ' / ' + formatBytes(p.total) : ''}`;
    let barClass = '';
    if (p.state === 'running') detail += ` • ${formatBytes(p.speed)}/s`;
    else if (p.state === 'interrupted') detail += ' • หยุดชั่วคราว';
    else if (p.state === 'done') { detail += ' • เสร็จแล้ว ✓'; barClass = 'done'; }
    else { detail += ' • ไม่สำเร็จ'; barClass = 'failed'; }
    entries.push({
      state: p.state,
      order: p.order,
      row: transferRow({
        order: p.order, icon: p.direction === 'upload' ? '📥' : '📤', name: p.name, detail, pct: p.state === 'done' ? 100 : pct, barClass,
      }),
    });
  }

  entries.sort(Logic.compareTransfers);
  const rows = entries.map((e) => e.row);
  $('transfers-card').hidden = rows.length === 0;
  $('transfers').replaceChildren(...rows);
  renderPending(); // entries move between the two lists as uploads start, finish or are cancelled
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

// ---------- settings and PWA ----------

// Settings page for every role: a menu entry + #pane-settings built once. Each setting is a row
// (settingsRow) so later features can add theirs; with no rows a short placeholder shows.
let settingsList = null;
let settingsEmpty = null;

function settingsRow(node) {
  if (!settingsList) return;
  settingsList.append(el('li', {}, node));
  settingsEmpty.hidden = true;
}

function setupSettings() {
  if (setupSettings.done) return;
  setupSettings.done = true;

  settingsList = el('ul', { class: 'list settings-list' });
  settingsEmpty = el('p', { class: 'mut small' }, 'ยังไม่มีตัวเลือกให้ตั้งค่า');
  const pane = el('section', { id: 'pane-settings', class: 'pane', 'aria-labelledby': 'settings-title' },
    el('div', { class: 'card' }, el('h2', { id: 'settings-title' }, 'ตั้งค่า'), settingsEmpty, settingsList));

  const navBtn = el('button', { class: 'nav', type: 'button', id: 'nav-settings', onclick: () => setView('settings') },
    el('span', { 'aria-hidden': 'true' }, '⚙️'), ' ตั้งค่า');
  navBtn.dataset.go = 'settings';
  const tabBtn = el('button', { type: 'button', id: 'tab-settings', onclick: () => setView('settings') },
    el('span', { 'aria-hidden': 'true' }, '⚙️'), 'ตั้งค่า');
  tabBtn.dataset.go = 'settings';

  $('pane-chat').after(pane);
  $$('.side-nav')[0].append(navBtn);
  $('tabbar').append(tabBtn);

  // Service Workers, installing the app and system notifications need HTTPS or localhost. On the
  // LAN address (plain HTTP) say so, and offer nothing to install.
  if (!window.isSecureContext) {
    settingsRow(el('p', { class: 'mut small', id: 'https-note' }, 'การติดตั้งแอปและการแจ้งเตือนของระบบต้องใช้ HTTPS'));
  }
  setupNotifySetting();
}

// Registers the Service Worker once per page load, after `load`, and only where the browser
// allows it. A failure is ignored on purpose: the page works the same without a Service Worker.
function registerServiceWorker() {
  if (!(window.isSecureContext && 'serviceWorker' in navigator)) return;
  try {
    navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(() => {});
  } catch { /* nothing to report: the app keeps working as a normal web page */ }
}

if (document.readyState === 'complete') registerServiceWorker();
else window.addEventListener('load', registerServiceWorker, { once: true });

// ---------- owner panel ----------

const KIND_LABEL = { physical: 'Wi-Fi/Ethernet', vpn: 'VPN', virtual: 'เสมือน' };

async function setupOwner() {
  if (role() !== 'owner') return; // other roles get no owner DOM and send no admin request
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
  setupOwnerTools();
}

function renderPin(pin) {
  ownerPin = pin; // kept in memory only so that copied reports can be scrubbed of it
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

// ---------- owner: connectivity check and status panel ----------
//
// Everything here is created by setupOwnerTools(), which only an Owner ever reaches, so other
// roles have none of these nodes and send no request to /api/admin/diagnostics, /power or
// /uploads. Rows, the report text and the status icons come from LanShareLogic
// (buildDiagnosticRows / buildReport); text from the server goes in with textContent only.
//
//   menu entry + #pane-diag  "ตรวจปัญหาการเชื่อมต่อ": one GET /api/admin/diagnostics per opening,
//                            "ตรวจใหม่" = ?fresh=1, 30 s AbortController timeout
//   #owner-status            card under the invite card: Power_Guard status (polled every 3 s)
//                            and the room's unfinished uploads with a cancel button each
// A 403 from any of these endpoints tears all of it down (ownerForbidden).

const DIAG_TIMEOUT_MS = 30000;
const OWNER_POLL_MS = 3000;
const OWNER_POLL_TIMEOUT_MS = 10000;
const DIAG_COPIED_MS = 4000; // the confirmation must stay visible for at least 3 s
const POWER_LABEL = { active: 'กำลังทำงาน', idle: 'ว่าง', disabled: 'ปิดอยู่', unavailable: 'ใช้ไม่ได้' };
const REQUEST_ERROR_LABEL = { network: 'ข้อผิดพลาดเครือข่าย', timeout: 'หมดเวลา', server: 'ข้อผิดพลาดของเซิร์ฟเวอร์' };

let ownerPin = ''; // the PIN this Owner was shown; never rendered again, only used to scrub reports
let ownerView = null; // set by setupOwnerTools; setView calls it so the pane can load when it opens

// Secrets this page knows about, to keep them out of a copied report. The device token is an
// HttpOnly cookie that scripts cannot read; any cookie value that is readable is added anyway.
function ownerSecrets() {
  const out = [];
  if (ownerPin) out.push(ownerPin);
  for (const part of document.cookie.split(';')) {
    const v = part.slice(part.indexOf('=') + 1).trim();
    if (v.length >= 16) out.push(v);
  }
  return out;
}

// ownerRequest wraps api() with a timeout and sorts failures into e.category:
// 'forbidden' (403), 'server' (any other HTTP error), 'timeout' or 'network'.
async function ownerRequest(path, opts = {}, timeoutMs = DIAG_TIMEOUT_MS) {
  const ctl = new AbortController();
  let timedOut = false;
  const timer = setTimeout(() => { timedOut = true; ctl.abort(); }, timeoutMs);
  try {
    const body = await api(path, { ...opts, signal: ctl.signal });
    if (timedOut) throw new Error('timeout'); // aborted while the body was being read
    return body;
  } catch (e) {
    if (e.status === 403) e.category = 'forbidden';
    else if (typeof e.status === 'number') e.category = 'server';
    else e.category = timedOut ? 'timeout' : 'network';
    throw e;
  } finally {
    clearTimeout(timer);
  }
}

function requestErrorText(prefix, e) {
  const cat = REQUEST_ERROR_LABEL[e.category] ? e.category : 'server';
  let more;
  if (cat === 'network') more = 'ติดต่อเซิร์ฟเวอร์ไม่ได้ ตรวจว่า Mac ยังเปิดโปรแกรมอยู่และอยู่ใน Wi-Fi เดียวกัน';
  else if (cat === 'timeout') more = 'เซิร์ฟเวอร์ไม่ตอบภายในเวลาที่กำหนด';
  else if (e.status === 401) more = 'เซสชันหมดอายุ ให้โหลดหน้าใหม่แล้วเข้าห้องอีกครั้ง';
  else if (e.status === 503) more = 'เซิร์ฟเวอร์ยังไม่เปิดใช้การตรวจนี้';
  else more = 'เซิร์ฟเวอร์ตอบกลับผิดพลาด (รหัส ' + (e.status || '-') + ')';
  return `${prefix} (${REQUEST_ERROR_LABEL[cat]}): ${more}`;
}

function formatTimeSec(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return new Intl.DateTimeFormat('th-TH', {
    day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(d);
}

function setupOwnerTools() {
  if (role() !== 'owner' || setupOwnerTools.done) return;
  setupOwnerTools.done = true;

  // ----- diagnostics page -----
  const d = {
    results: null, // Check_Results as returned by the server; cleared on 403
    generatedAt: '',
    cached: false,
    stale: false,
    loading: false,
    netBusy: false,
    nets: [],
    open: false,
    copyTimer: 0,
  };

  const live = el('p', { class: 'diag-live small', role: 'status', 'aria-live': 'polite' });
  const meta = el('span', { class: 'mut small' });
  const staleBadge = el('span', { class: 'pill diag-stale', hidden: true }, 'เก่า');
  const refreshBtn = el('button', { class: 'btn sm', type: 'button', onclick: () => load(true) }, 'ตรวจใหม่');
  const copyBtn = el('button', { class: 'btn ghost sm', type: 'button', disabled: true, onclick: () => copyReport() }, 'คัดลอกรายงาน');
  const list = el('ul', { class: 'diag-list' });
  const nic = el('select', { class: 'select', id: 'diag-nic', disabled: true });
  nic.addEventListener('change', () => pickNet(nic.value));
  const netRow = el('div', { class: 'net-row', hidden: true },
    el('label', { htmlFor: 'diag-nic', class: 'mut small' }, 'เครือข่ายที่ใช้ (เปลี่ยนแล้วจะตรวจใหม่ทันที):'), nic);
  const manualLabel = el('label', { htmlFor: 'diag-manual', class: 'mut small' }, 'คัดลอกรายงานอัตโนมัติไม่ได้ เลือกข้อความด้านล่างแล้วคัดลอกเอง');
  const manual = el('textarea', { id: 'diag-manual', class: 'text-input diag-manual', readOnly: true, rows: 10 });
  const manualBox = el('div', { class: 'diag-manualbox', hidden: true }, manualLabel, manual);

  const pane = el('section', { id: 'pane-diag', class: 'pane', 'aria-labelledby': 'diag-title' },
    el('div', { class: 'card diag' },
      el('div', { class: 'pane-head' }, el('h2', { id: 'diag-title' }, 'ตรวจปัญหาการเชื่อมต่อ'),
        el('div', { class: 'diag-tools' }, refreshBtn, copyBtn)),
      el('div', { class: 'diag-metaRow' }, staleBadge, meta),
      live, list, netRow, manualBox));

  const navBtn = el('button', { class: 'nav', type: 'button', id: 'nav-diag', onclick: () => setView('diag') },
    el('span', { 'aria-hidden': 'true' }, '🩺'), ' ตรวจปัญหาการเชื่อมต่อ');
  navBtn.dataset.go = 'diag';
  const tabBtn = el('button', { type: 'button', id: 'tab-diag', onclick: () => setView('diag') },
    el('span', { 'aria-hidden': 'true' }, '🩺'), 'ตรวจปัญหา');
  tabBtn.dataset.go = 'diag';

  $('pane-chat').after(pane);
  $$('.side-nav')[0].append(navBtn);
  $('tabbar').append(tabBtn);

  function say(text, bad) {
    live.textContent = text;
    live.classList.toggle('err', !!bad);
  }

  function sync() {
    const busy = d.loading || d.netBusy;
    refreshBtn.disabled = busy;
    copyBtn.disabled = !d.results;
    nic.disabled = busy || d.nets.length === 0;
    list.setAttribute('aria-busy', String(d.loading));
    staleBadge.hidden = !(d.stale && d.results);
    const when = d.results && d.generatedAt ? formatTimeSec(d.generatedAt) : '';
    meta.textContent = when ? 'ตรวจเมื่อ ' + when + (d.cached ? ' (ผลที่เก็บไว้ไม่เกิน 30 วินาที)' : '') : '';
  }

  function rowNode(r) {
    const body = el('div', { class: 'diag-body' },
      el('div', { class: 'diag-head' }, el('b', { class: 'diag-title' }, r.title), el('span', { class: 'diag-st' }, r.statusText)));
    if (r.detail) body.append(el('p', { class: 'diag-detail small' }, el('span', { class: 'mut' }, 'รายละเอียด: '), r.detail));
    if (r.hint) body.append(el('p', { class: 'diag-hint small' }, el('span', { class: 'mut' }, 'คำแนะนำ: '), r.hint));
    const li = el('li', { class: 'diag-row ' + r.status }, el('span', { class: 'diag-ic', 'aria-hidden': 'true' }, r.icon), body);
    li.dataset.id = r.id;
    return li;
  }

  function render() {
    const rows = Logic.buildDiagnosticRows(d.results || []);
    list.replaceChildren(...rows.map(rowNode));
    sync();
  }

  function renderNets() {
    nic.replaceChildren(...d.nets.map((n) => el('option', { value: n.name, selected: !!n.selected },
      `${n.name} • ${n.ip} (${KIND_LABEL[n.kind] || n.kind})${n.recommended ? ' — แนะนำ' : ''}`)));
    netRow.hidden = d.nets.length === 0;
    sync();
  }

  // Puts keyboard focus back where it was when a control was disabled under it.
  function keepFocus(had, fn) {
    return fn().finally(() => {
      if (had && had !== document.body && had.isConnected && !had.disabled && document.activeElement === document.body) had.focus();
    });
  }

  function summary(rows) {
    const n = { ok: 0, warn: 0, fail: 0, unknown: 0 };
    for (const r of rows) n[r.status]++;
    return `ตรวจเสร็จแล้ว ${rows.length} รายการ: ปกติ ${n.ok}, ควรตรวจสอบ ${n.warn}, ผิดปกติ ${n.fail}, ไม่ทราบ ${n.unknown}`;
  }

  function load(fresh) {
    if (d.loading || d.netBusy) return Promise.resolve();
    return keepFocus(document.activeElement, async () => {
      d.loading = true;
      if (d.results) d.stale = true; // the old list stays on screen, marked as old, until the new one arrives
      render();
      say('กำลังตรวจ…');
      try {
        const body = await ownerRequest('/api/admin/diagnostics' + (fresh ? '?fresh=1' : ''));
        if (!body || !Array.isArray(body.checks)) {
          const bad = new Error('bad body');
          bad.category = 'server';
          throw bad;
        }
        d.results = body.checks;
        d.generatedAt = body.generatedAt || '';
        d.cached = !!body.cached;
        d.stale = false;
        d.loading = false;
        render();
        say(summary(Logic.buildDiagnosticRows(d.results)));
      } catch (e) {
        if (e.category === 'forbidden') return ownerForbidden();
        d.loading = false;
        d.stale = !!d.results;
        render();
        say(requestErrorText('ตรวจไม่สำเร็จ', e) + (d.results ? ' ผลที่แสดงอยู่เป็นผลเก่า' : ''), true);
      } finally {
        d.loading = false;
        sync();
      }
    });
  }

  async function loadNets() {
    try {
      const nets = await ownerRequest('/api/admin/network', {}, OWNER_POLL_TIMEOUT_MS);
      if (Array.isArray(nets)) d.nets = nets;
    } catch (e) {
      if (e.category === 'forbidden') return ownerForbidden();
    }
    if (setupOwnerTools.gone) return;
    renderNets();
  }

  function pickNet(name) {
    if (d.loading || d.netBusy) { renderNets(); return Promise.resolve(); }
    return keepFocus(document.activeElement, async () => {
      d.netBusy = true;
      sync();
      say('กำลังเปลี่ยนเครือข่าย…');
      let ok = false;
      try {
        await ownerRequest('/api/admin/network', jsonBody('POST', { name }));
        ok = true;
      } catch (e) {
        if (e.category === 'forbidden') return ownerForbidden();
        renderNets(); // the list shows the interface that is still selected
        say(requestErrorText('เปลี่ยนเครือข่ายไม่สำเร็จ', e), true);
      } finally {
        d.netBusy = false;
        sync();
      }
      if (!ok) return;
      loadJoinInfo(); // keep the invite card (URL, QR) in step
      loadNetworks();
      await loadNets();
      if (!setupOwnerTools.gone) await load(true);
    });
  }

  async function copyReport() {
    if (!d.results) return;
    const text = Logic.buildReport(d.results, { generatedAt: d.generatedAt, secrets: ownerSecrets() });
    const ok = await copyText(text);
    if (setupOwnerTools.gone) return;
    clearTimeout(d.copyTimer);
    if (ok) {
      manualBox.hidden = true;
      copyBtn.textContent = 'คัดลอกแล้ว';
      say('คัดลอกแล้ว');
      d.copyTimer = setTimeout(() => {
        copyBtn.textContent = 'คัดลอกรายงาน';
        if (live.textContent === 'คัดลอกแล้ว') say('');
      }, DIAG_COPIED_MS);
      return;
    }
    copyBtn.textContent = 'คัดลอกรายงาน';
    manual.value = text;
    manualBox.hidden = false;
    manual.focus();
    manual.select();
    say('คัดลอกรายงานไม่สำเร็จ เลือกข้อความในกล่องด้านล่างแล้วคัดลอกเอง', true);
  }

  function openPage() {
    manualBox.hidden = true;
    loadNets();
    load(false);
  }

  ownerView = (v) => {
    const open = v === 'diag';
    if (open && !d.open) { d.open = true; openPage(); } else if (!open) d.open = false;
  };

  // ----- status panel: Power_Guard and unfinished uploads -----
  const powerText = el('b', { class: 'ost-power', 'aria-live': 'polite' }, 'กำลังตรวจสอบ…');
  const uploadsEmpty = el('p', { class: 'mut small' }, 'ไม่มีอัปโหลดที่ค้างอยู่');
  const uploadsList = el('ul', { class: 'list ost-uploads' });
  const statusCard = el('section', { id: 'owner-status', class: 'card ostat', 'aria-labelledby': 'ost-title' },
    el('h2', { id: 'ost-title' }, 'สถานะเครื่อง'),
    el('p', { class: 'small' }, el('span', { class: 'mut' }, 'การกัน Mac หลับ: '), powerText),
    el('h3', { class: 'ost-h' }, 'อัปโหลดที่ค้างอยู่'),
    uploadsEmpty, uploadsList);
  $('owner').after(statusCard);

  const uploadRows = new Map(); // session id -> {li, text, btn}; updated in place so focus survives polling

  function uploadLine(u) {
    const pct = u.size > 0 ? Math.floor((u.offset * 100) / u.size) : 0;
    const when = u.lastActivityAt ? formatTimeSec(u.lastActivityAt) : '';
    return `${pct}% (${formatBytes(u.offset)} จาก ${formatBytes(u.size)})` + (when ? ` • ใช้งานล่าสุด ${when}` : '');
  }

  function renderUploads(items) {
    const seen = new Set();
    items.forEach((u, i) => {
      seen.add(u.id);
      let row = uploadRows.get(u.id);
      if (!row) {
        const text = el('span', { class: 'grow small' });
        const btn = el('button', { class: 'btn danger sm', type: 'button' }, 'ยกเลิก');
        const li = el('li', { class: 'ost-row' }, text, btn);
        btn.addEventListener('click', () => cancelUpload(u.id, row));
        row = { li, text, btn };
        uploadRows.set(u.id, row);
        uploadsList.append(li);
      }
      row.btn.setAttribute('aria-label', `ยกเลิกอัปโหลดที่ค้างอยู่ลำดับที่ ${i + 1}`);
      const line = uploadLine(u);
      if (row.text.textContent !== line) row.text.textContent = line;
    });
    for (const [id, row] of uploadRows) {
      if (!seen.has(id)) { row.li.remove(); uploadRows.delete(id); }
    }
    uploadsEmpty.hidden = items.length > 0;
  }

  async function cancelUpload(id, row) {
    if (!confirm('ยกเลิกอัปโหลดนี้? ข้อมูลที่ส่งมาบางส่วนจะถูกลบ')) return;
    row.btn.disabled = true;
    try {
      await ownerRequest('/api/admin/uploads/' + encodeURIComponent(id), { method: 'DELETE' }, OWNER_POLL_TIMEOUT_MS);
      toast('ยกเลิกอัปโหลดแล้ว');
    } catch (e) {
      if (e.category === 'forbidden') return ownerForbidden();
      if (e.status === 404) toast('อัปโหลดนี้จบหรือถูกยกเลิกไปแล้ว');
      else toast(requestErrorText('ยกเลิกอัปโหลดไม่สำเร็จ', e));
    }
    if (setupOwnerTools.gone) return;
    row.btn.disabled = false;
    pollStatus();
  }

  let polling = false;
  async function pollStatus() {
    if (polling || setupOwnerTools.gone) return;
    polling = true;
    try {
      const [p, u] = await Promise.allSettled([
        ownerRequest('/api/admin/power', {}, OWNER_POLL_TIMEOUT_MS),
        ownerRequest('/api/admin/uploads', {}, OWNER_POLL_TIMEOUT_MS),
      ]);
      if ([p, u].some((r) => r.status === 'rejected' && r.reason.category === 'forbidden')) return ownerForbidden();
      if (setupOwnerTools.gone) return;
      const label = p.status === 'fulfilled' && p.value ? POWER_LABEL[p.value.status] : '';
      const next = label || 'ไม่ทราบ';
      if (powerText.textContent !== next) powerText.textContent = next; // only on change, so it is announced once
      if (u.status === 'fulfilled' && Array.isArray(u.value)) renderUploads(u.value);
    } finally {
      polling = false;
    }
  }

  const pollTimer = setInterval(() => { if (!document.hidden) pollStatus(); }, OWNER_POLL_MS);
  const onVisible = () => { if (!document.hidden) pollStatus(); };
  document.addEventListener('visibilitychange', onVisible);
  pollStatus();

  // ----- 403: this device is not an Owner (any more) -----
  function ownerForbidden() {
    if (setupOwnerTools.gone) return;
    setupOwnerTools.gone = true;
    clearInterval(pollTimer);
    clearTimeout(d.copyTimer);
    document.removeEventListener('visibilitychange', onVisible);
    ownerView = null;
    d.results = null;
    d.nets = [];
    manual.value = '';
    uploadRows.clear();
    const hadFocus = pane.contains(document.activeElement) || statusCard.contains(document.activeElement);
    if (state.view === 'diag') setView(isDesktopLayout() ? 'files' : 'upload');
    pane.remove();
    navBtn.remove();
    tabBtn.remove();
    statusCard.remove();
    if (hadFocus) {
      const next = $$('[data-go="files"]').find((b) => b.offsetParent !== null);
      if (next) next.focus();
    }
    toast('ฟังก์ชันนี้ใช้ได้เฉพาะ Owner เท่านั้น');
  }
}

boot();
