'use strict';
const $ = s => document.querySelector(s);
const GAP = () => parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--gap')) || 6;
const ROWH = () => parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--rowh')) || 220;

const state = { owner: 'all', kind: 'all', cursor: '', items: [], done: false, loading: false, owners: [], person: 0, album: 0, secret: false, family: false };

function api(path, opts) {
  return fetch(path, opts).then(r => r.ok ? r.json() : Promise.reject(new Error(r.status + ' ' + r.statusText)));
}

/* ---- 会话失效：任何 401 都回到登录页，而不是静默失败 ---- */
const rawFetch = window.fetch.bind(window);
let sessionLost = false;
window.fetch = async function (...args) {
  const r = await rawFetch(...args);
  // 只有「已经进入应用之后」的 401 才算会话失效；
  // 登录页首次探测 /api/me 本来就是 401，不能当成过期。
  if (r.status === 401 && appStarted) {
    const u = String(args[0] || '');
    if (u.indexOf('/api/login') < 0 && u.indexOf('/api/setup') < 0) onSessionLost();
  }
  return r;
};
function onSessionLost() {
  if (sessionLost) return;
  sessionLost = true;
  appStarted = false;
  $('#authTitle').textContent = '登录已过期';
  $('#authSub').textContent = '请重新登录后继续';
  $('#authErr').textContent = '';
  $('#authUser').style.display = 'block';
  $('#authPass2').style.display = 'none';
  $('#authPass').placeholder = '密码';
  $('#auth').classList.add('show');
}

/* ---- 顶栏空间 ---- */
async function initSpaces() {
  const cfg = await api('/api/config');
  state.owners = cfg.owners;
  const box = $('#spaces');
  box.innerHTML = '<button class="on" data-o="all">全部空间</button>' +
    cfg.owners.map(o => '<button data-o="' + o.owner + '">' + esc(o.label) + '</button>').join('');
  box.addEventListener('click', e => {
    const b = e.target.closest('button'); if (!b) return;
    box.querySelectorAll('button').forEach(x => x.classList.toggle('on', x === b));
    state.owner = b.dataset.o; reset();
  });
}

function esc(s) { return String(s).replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c])); }
function fmtNum(n) { return n >= 1000 ? (n / 1000).toFixed(n >= 10000 ? 0 : 1) + 'k' : String(n); }
function dur(s) { s = Math.round(s || 0); return String(Math.floor(s / 60)).padStart(2, '0') + ':' + String(s % 60).padStart(2, '0'); }
function aspectOf(a) {
  let r = (a.w > 0 && a.h > 0) ? a.w / a.h : (a.kind === 'video' ? 1.777 : 1.5);
  if (!isFinite(r) || r <= 0) r = 1.5;
  // 极端比例安全钳制：避免超长竖图或超长横图把整行压崩或撑爆
  return Math.min(3.2, Math.max(0.4, r));
}

/* ---- 拉数据 ---- */
function reset() {
  state.cursor = ''; state.items = []; state.done = false;
  $('#stream').innerHTML = '';
  // 如果正好有一个请求在飞，不能把这次刷新丢掉，标记一下等它结束再来
  if (state.loading) { state.again = true; return; }
  load();
}

async function load() {
  if (state.loading) { state.again = true; return; }
  if (state.done) return;
  state.loading = true;
  try {
    const q = new URLSearchParams({ owner: state.owner, kind: state.kind, limit: '120' });
    if (state.person) q.set('person', state.person);
    if (state.album) q.set('album', state.album);
    if (state.folder) q.set('folder', state.folder);
    if (state.secret) q.set('secret', '1');
    if (state.family) { q.set('owner', 'family'); q.delete('secret'); }
    if (state.cursor) q.set('before', state.cursor);
    const r = await api('/api/timeline?' + q);
    const fresh = r.items || [];
    const wasEmpty = state.items.length === 0;
    state.items = state.items.concat(fresh);
    state.cursor = r.next; state.done = !r.next;
    // 只有第一页整体重建；之后只追加，避免把已加载的上千张缩略图销毁重下
    if (wasEmpty) renderAll(); else appendItems(fresh);
    if (!state.items.length) showEmpty();
    // 加载完还停在底部（内容不够一屏）就继续接力，否则滑到底会卡住
    chain++;
    if (!state.done && chain < 8 && innerHeight + window.scrollY >
        document.documentElement.scrollHeight - 900) {
      setTimeout(() => { if (!state.done && !state.loading) load(); }, 180);
    } else {
      chain = 0;
    }
  } catch (e) {
    console.error(e);
  } finally {
    state.loading = false;
    if (state.again) { state.again = false; reset(); }
  }
}

function showEmpty() {
  $('#stream').innerHTML = '<div class="empty"><b>还没有媒体</b>' +
    '把照片放进 <code>/mnt/sda1/lwm/手机照片与视频备份/</code> 后点右上角重新扫描</div>';
}

/* ---- 渲染 ---- */
function renderAll() {
  state.monthItems = new Map();
  const groups = new Map();
  for (const it of state.items) {
    const ym = it.year_month || '未知';
    if (!groups.has(ym)) groups.set(ym, []);
    groups.get(ym).push(it);
  }
  const html = [];
  for (const [ym, list] of groups) {
    state.monthItems.set(ym, list);
    const vids = list.filter(x => x.kind === 'video').length;
    html.push('<section class="m" data-ym="' + ym + '"><div class="month-h"><h2>' + labelOf(ym) + '</h2>' +
        '<button class="msel" data-ym="' + ym + '">☐ 全选本月</button></div>' +
      '<div class="month-sub" data-n="' + list.length + '" data-v="' + vids + '">' + list.length + ' 项' + (vids ? ' · ' + vids + ' 段视频' : '') + '</div>' +
      '<div class="rows">' + rowsHTML(list) + '</div></section>');
  }
  $('#stream').innerHTML = html.join('');
  bindCells();
  buildRail();
  updateStatus();
}

function labelOf(ym) {
  if (!/^\d{4}-\d{2}$/.test(ym)) return ym;
  return ym.slice(0, 4) + ' 年 ' + parseInt(ym.slice(5, 7), 10) + ' 月';
}

function rowsHTML(list) {
  const H = ROWH(), G = GAP();
  const streamEl = $('#stream');
  let W = streamEl ? streamEl.clientWidth : 0;
  if (!W || W < 320) {
    W = Math.max(320, window.innerWidth - (window.innerWidth <= 760 ? 24 : 52));
  }
  const rows = []; let cur = [], sum = 0;
  for (const it of list) {
    cur.push(it); sum += aspectOf(it);
    if (sum * H + (cur.length - 1) * G >= W) { rows.push({ items: cur, sum }); cur = []; sum = 0; }
  }
  if (cur.length) rows.push({ items: cur, sum, last: true });

  const minH = Math.round(H * 0.70);
  const maxH = Math.round(H * 1.35);

  return rows.map(row => {
    let h;
    if (row.last) {
      const totalW = row.sum * H + (row.items.length - 1) * G;
      h = totalW > W ? (W - (row.items.length - 1) * G) / row.sum : H;
    } else {
      h = (W - (row.items.length - 1) * G) / row.sum;
    }
    // 限制单行高度在合理区间，绝不出现巨型黑洞空隙
    h = Math.min(maxH, Math.max(minH, h));

    const cells = row.items.map(it => {
      const asp = aspectOf(it);
      const flex = row.last ? 'flex:0 0 auto;width:' + (asp * h).toFixed(2) + 'px'
                            : 'flex-grow:' + asp.toFixed(3);
      const badge = it.kind === 'video'
        ? '<div class="badge"><svg viewBox="0 0 24 24"><path d="M8 5v14l11-7z"/></svg>' + dur(it.duration) + '</div>' : '';
      return '<div class="cell" data-id="' + it.id + '"' + (it.kind === 'video' ? ' data-vid="' + it.id + '"' : '') + ' style="' + flex + '">' +
        '<img class="lqip" loading="lazy" src="/t/' + it.id + '/m.jpg?v=' + it.mtime + '" alt="">' +
        '<img class="ph" loading="lazy" decoding="async" src="/t/' + it.id + '/t.jpg?v=' + it.mtime + '" alt="' + esc(it.name) + '">' +
        '<div class="chk"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.4" stroke-linecap="round" stroke-linejoin="round"><path d="M4.5 12.5l5 5 10-11"/></svg></div>' +
        badge + '</div>';
    }).join('');
    return '<div class="row" style="height:' + h.toFixed(2) + 'px">' + cells + '</div>';
  }).join('');
}

/** 只把新的一页追加到末尾，已有月份合并排版，避免产生空隙半行 */
function appendItems(list) {
  if (!list || !list.length) return;
  const stream = $('#stream');
  if (!state.monthItems) state.monthItems = new Map();
  const groups = new Map();
  for (const it of list) {
    const ym = it.year_month || '未知';
    if (!groups.has(ym)) groups.set(ym, []);
    groups.get(ym).push(it);
  }
  for (const [ym, arr] of groups) {
    const last = stream.lastElementChild;
    const vids = arr.filter(x => x.kind === 'video').length;
    if (last && last.classList && last.classList.contains('m') && last.dataset.ym === ym) {
      const rows = last.querySelector('.rows');
      let mList = state.monthItems.get(ym) || [];
      mList = mList.concat(arr);
      state.monthItems.set(ym, mList);
      if (rows) rows.innerHTML = rowsHTML(mList);
      const sub = last.querySelector('.month-sub');
      if (sub) {
        const prev = parseInt(sub.dataset.n || '0', 10) + arr.length;
        const pv = parseInt(sub.dataset.v || '0', 10) + vids;
        sub.dataset.n = prev; sub.dataset.v = pv;
        sub.textContent = prev + ' 项' + (pv ? ' · ' + pv + ' 段视频' : '');
      }
    } else {
      state.monthItems.set(ym, arr);
      stream.insertAdjacentHTML('beforeend',
        '<section class="m" data-ym="' + ym + '"><div class="month-h"><h2>' + labelOf(ym) + '</h2>' +
        '<button class="msel" data-ym="' + ym + '">☐ 全选本月</button></div>' +
        '<div class="month-sub" data-n="' + arr.length + '" data-v="' + vids + '">' +
        arr.length + ' 项' + (vids ? ' · ' + vids + ' 段视频' : '') + '</div>' +
        '<div class="rows">' + rowsHTML(arr) + '</div></section>');
    }
  }
  bindCells();
  buildRail();
  updateStatus();
}
function bindCells() {
  document.querySelectorAll('.cell').forEach(el => {
    const id = +el.dataset.id;
    let lp = null;
    el.onclick = () => { if (selMode) toggleSel(id); else openViewer(id); };
    el.addEventListener('pointerdown', () => {
      lp = setTimeout(() => { setSelMode(true); if (!selSet.has(id)) toggleSel(id); }, 480);
    });
    ['pointerup', 'pointerleave', 'pointercancel'].forEach(ev =>
      el.addEventListener(ev, () => clearTimeout(lp)));
    const ph = el.querySelector('.ph');
    ph.onerror = () => el.classList.add('fail');

    // 视频：悬停时循环播放取帧缩略图
    // 先用游离的 Image 预加载，加载完成才挂到 DOM——否则一个卡住的请求会让整个
    // 文档的 load 事件永远不结束，标签页就一直转圈，看起来像页面卡死。
    if (el.dataset.vid) {
      let timer = null, idx = 0, gen = 0;
      const cache = el._frameCache || (el._frameCache = []);
      const preload = (n, cb) => {
        if (cache[n] !== undefined) return cb();
        const im = new Image();
        im.onload = () => { cache[n] = im.src; cb(); };
        im.onerror = () => { cache[n] = ''; };
        im.src = '/v/' + el.dataset.vid + '/' + n + '.jpg';
      };
      const show = g => {
        const n = idx % 4;
        idx++;
        preload(n, () => {
          if (g !== gen || !cache[n]) return;
          let strip = el.querySelector('.strip');
          if (!strip) {
            strip = document.createElement('img');
            strip.className = 'strip';
            strip.alt = '';
            el.appendChild(strip);
          }
          strip.src = cache[n];
        });
      };
      el.addEventListener('mouseenter', () => {
        gen++;
        idx = 0;
        show(gen);
        timer = setInterval(() => show(gen), 700);
      });
      el.addEventListener('mouseleave', () => {
        gen++;
        clearInterval(timer);
        timer = null;
        const s = el.querySelector('.strip');
        if (s) s.remove();
      });
    }
  });
  renderSel();
}

/* ---- 状态栏 ---- */
function updateStatus() {
  const n = state.items.length;
  const v = state.items.filter(x => x.kind === 'video').length;
  // 显示精确数量（带千分位），方便和手机里的张数对照
  const fmt = x => x.toLocaleString('zh-CN');
  $('#status').textContent = n
    ? fmt(n) + ' 项 · ' + fmt(v) + ' 视频' + (state.done ? '' : ' …')
    : '';
}

/* ---- 无限滚动：只有用户真正往下滑时才加载 ---- */
let sentinel = null;
let chain = 0;
let userScrolled = false;
addEventListener('wheel', () => { userScrolled = true; }, { passive: true });
addEventListener('touchmove', () => { userScrolled = true; }, { passive: true });
addEventListener('scroll', () => {
  if (window.scrollY > 40) userScrolled = true;
  if (!userScrolled) return;
  if (state.done || state.loading) return;
  const nearBottom = innerHeight + window.scrollY >
        document.documentElement.scrollHeight - 900;
  if (nearBottom) load();
}, { passive: true });

function ensureSentinel() { /* 不再用哨兵自动加载，保留空实现兼容旧调用 */ }

/* ---- 光轴 ---- */
function buildRail() {
  const secs = [...document.querySelectorAll('section.m')];
  const rail = $('#rail');
  if (secs.length < 3) { rail.style.display = 'none'; return; }
  rail.style.display = 'flex';
  const max = Math.max(...secs.map(s => s.querySelectorAll('.cell').length), 1);
  $('#dens').innerHTML = secs.map(s => {
    const c = s.querySelectorAll('.cell').length;
    return '<div class="seg" style="flex:' + c + ';opacity:' + (0.35 + 0.65 * c / max) + '"></div>';
  }).join('');
  syncRail();
}
function syncRail() {
  const rail = $('#rail'); if (rail.style.display === 'none') return;
  const max = document.documentElement.scrollHeight - innerHeight;
  const p = max > 0 ? scrollY / max : 0;
  $('#thumb').style.top = (p * (rail.clientHeight - 46)) + 'px';
}
addEventListener('scroll', syncRail, { passive: true });

let dragging = false;
function railSeek(clientY, live) {
  const rail = $('#rail'), r = rail.getBoundingClientRect();
  let p = Math.max(0, Math.min(1, (clientY - r.top) / r.height));
  $('#thumb').style.top = (p * (rail.clientHeight - 46)) + 'px';
  const secs = [...document.querySelectorAll('section.m')];
  const idx = Math.min(secs.length - 1, Math.floor(p * secs.length));
  if (secs[idx]) {
    $('#bigdateT').textContent = labelOf(secs[idx].dataset.ym);
    $('#bigdate').classList.add('show');
    // 拖动过程中就让页面跟着走（原来只有松手才滚，看起来"只有时间在变"）
    if (live) {
      const top = Math.max(0, secs[idx].offsetTop - 70);
      if (window.scrollTo) window.scrollTo({ top: top, behavior: 'auto' });
      else window.scrollTo(0, top);
    }
  }
  return { secs, idx };
}
$('#rail').addEventListener('pointerdown', e => { dragging = true; $('#rail').setPointerCapture(e.pointerId); railSeek(e.clientY, true); });
$('#rail').addEventListener('pointermove', e => { if (dragging) railSeek(e.clientY, true); });
$('#rail').addEventListener('pointerup', e => {
  if (!dragging) return; dragging = false;
  const { secs, idx } = railSeek(e.clientY, true);
  if (secs[idx]) scrollTo({ top: Math.max(0, secs[idx].offsetTop - 70), behavior: 'smooth' });
  setTimeout(() => $('#bigdate').classList.remove('show'), 360);
});

/* ---- 查看器 ---- */
let vIdx = -1;
function openViewer(id) {
  vIdx = state.items.findIndex(x => x.id === id);
  if (vIdx < 0) return;
  $('#viewer').classList.add('show');
  document.body.style.overflow = 'hidden';
  try { history.replaceState(null, '', '#/p/' + id); } catch (e) {}
  showAsset();
}
function showAsset() {
  const a = state.items[vIdx];
  if (!a) return;
  const stage = $('#vStage');
  stage.innerHTML = '';
  if (a.kind === 'video') {
    // 先放海报，避免加载期间一片黑
    const poster = document.createElement('img');
    poster.src = '/t/' + a.id + '/p.jpg?v=' + a.mtime;
    poster.className = 'v-poster';
    poster.alt = '';
    stage.appendChild(poster);

    const v = document.createElement('video');
    v.src = '/m/' + a.id + '?v=' + a.mtime;
    v.controls = true; v.autoplay = true; v.playsInline = true;
    v.preload = 'metadata';
    v.style.display = 'none';
    v.addEventListener('loadeddata', () => {
      poster.remove();
      v.style.display = '';
      capturePoster('/m/' + a.id, a.id).catch(() => {});
    });
    v.addEventListener('error', () => {
      poster.remove();
      v.remove();
      const box = document.createElement('div');
      box.className = 'v-fail';
      box.innerHTML = '<b>浏览器无法播放这个视频</b>' +
        '<span>编码不被浏览器支持（常见于 MPEG-4 Part 2 / HEVC / 部分 MOV）。' +
        '文件本身没问题，用系统播放器或手机可以直接打开。</span>' +
        '<div class="v-fail-btns">' +
        '<a class="btn" href="/m/' + a.id + '" download="' + esc(a.name) + '">下载原文件</a>' +
        '<a class="btn" href="/m/' + a.id + '" target="_blank" rel="noopener">新标签页打开</a>' +
        '</div>';
      stage.appendChild(box);
    });
    stage.appendChild(v);
  } else {
    const img = document.createElement('img');
    img.src = '/m/' + a.id + '?v=' + a.mtime;
    img.alt = a.name;
    enableZoom(stage, img);
    stage.appendChild(img);
  }
  $('#vCount').textContent = (vIdx + 1) + ' / ' + state.items.length + ' · #' + a.id;
  const bits = [];
  if (a.taken_at) bits.push('<b>' + new Date(a.taken_at * 1000).toLocaleString('zh-CN') + '</b>');
  if (a.camera) bits.push(esc(a.camera));
  if (a.lens) bits.push(esc(a.lens));
  if (a.aperture) bits.push(esc(a.aperture));
  if (a.iso) bits.push('ISO ' + a.iso);
  if (a.focal) bits.push(esc(a.focal));
  if (a.w) bits.push(a.w + ' × ' + a.h);
  if (a.size) bits.push(fmtB(a.size));
  $('#vInfo').innerHTML = '<div class="row2">' + bits.join(' ') + '</div>' +
    '<div class="path" title="' + esc(a.name + ' · ' + a.rel_path) + '">' + esc(a.rel_path) + '</div>';
}
function closeViewer() {
  $('#viewer').classList.remove('show');
  $('#vStage').innerHTML = '';
  document.body.style.overflow = '';
  try { history.replaceState(null, '', location.pathname + location.search); } catch (e) {}
}
function step(d) {
  vIdx = (vIdx + d + state.items.length) % state.items.length;
  showAsset();
}
$('#vClose').onclick = closeViewer;
$('#vPrev').onclick = () => step(-1);
$('#vNext').onclick = () => step(1);
$('#vInfoBtn').onclick = () => $('#vInfo').classList.toggle('show');
addEventListener('keydown', e => {
  if (!$('#viewer').classList.contains('show')) return;
  if (e.key === 'Escape') closeViewer();
  if (e.key === 'ArrowLeft') step(-1);
  if (e.key === 'ArrowRight') step(1);
  if (e.key === 'i') $('#vInfo').classList.toggle('show');
});

/* ---- 过滤 / 扫描 ---- */
$('#pills').addEventListener('click', e => {
  const b = e.target.closest('button'); if (!b) return;
  $('#pills').querySelectorAll('button').forEach(x => x.classList.toggle('on', x === b));
  state.kind = b.dataset.k; reset();
});
$('#btnScan').onclick = async () => {
  const b = $('#btnScan'); b.style.opacity = '.4';
  $('#status').textContent = '扫描中…';
  try {
    const r = await api('/api/scan', { method: 'POST' });
    $('#status').textContent = '新增 ' + r.added + ' · 更新 ' + r.updated;
    reset();
  } catch (e) { $('#status').textContent = '扫描失败'; }
  finally { b.style.opacity = '1'; }
};
addEventListener('resize', () => { clearTimeout(window.__rt); window.__rt = setTimeout(renderAll, 200); });

/* ---- P2 分块上传（4MB/块，3 并发，IndexedDB 断点续传）---- */
const UP = { items: [], running: false, totalBytes: 0, doneBytes: 0, lastT: 0, lastB: 0, speed: 0 };

function fmtB(b){ return b >= 1048576 ? (b/1048576).toFixed(1)+' MB' : b >= 1024 ? (b/1024).toFixed(0)+' KB' : b+' B'; }
function fmtS(v){ return v >= 1048576 ? (v/1048576).toFixed(1)+' MB/s' : (v/1024).toFixed(0)+' KB/s'; }

let toastTimer = null;
function toast(msg){
  const t = $('#toast'); if (!t) return;
  t.textContent = msg; t.classList.add('show');
  clearTimeout(toastTimer); toastTimer = setTimeout(() => t.classList.remove('show'), 2400);
}

function curOwner(){ return state.owner === 'all' ? 'lwm' : state.owner; }

function idb(){ return new Promise((res, rej) => { const r = indexedDB.open('lumina', 1);
  r.onupgradeneeded = () => { r.result.createObjectStore('up', { keyPath: 'id' }); };
  r.onsuccess = () => res(r.result); r.onerror = () => rej(r.error); }); }
async function idbPut(v){ const db = await idb(); return new Promise((res, rej) => {
  const t = db.transaction('up', 'readwrite'); t.objectStore('up').put(v);
  t.oncomplete = () => res(); t.onerror = () => rej(t.error); }); }
async function idbDel(id){ const db = await idb(); return new Promise((res, rej) => {
  const t = db.transaction('up', 'readwrite'); t.objectStore('up').delete(id);
  t.oncomplete = () => res(); t.onerror = () => rej(t.error); }); }
async function idbAll(){ const db = await idb(); return new Promise((res, rej) => {
  const t = db.transaction('up', 'readonly'); const q = t.objectStore('up').getAll();
  q.onsuccess = () => res(q.result || []); q.onerror = () => rej(q.error); }); }

function updateHud(){
  const pct = UP.totalBytes ? Math.min(100, UP.doneBytes / UP.totalBytes * 100) : 0;
  $('#hudBar').style.width = pct + '%';
  $('#hudSpeed').textContent = fmtS(UP.speed);
  $('#hudCount').textContent = fmtB(UP.doneBytes) + ' / ' + fmtB(UP.totalBytes);
  let eta = '—';
  if (UP.speed > 0 && UP.totalBytes > UP.doneBytes) eta = '剩余 ' + Math.ceil((UP.totalBytes - UP.doneBytes) / UP.speed) + ' 秒';
  else if (UP.totalBytes > 0 && UP.doneBytes >= UP.totalBytes) eta = '完成';
  $('#hudEta').textContent = eta;
  if (typeof updateMobileBar === 'function') updateMobileBar();
}

let speedTimer = null;
function tickSpeed(){
  const now = performance.now();
  if (!UP.lastT){ UP.lastT = now; UP.lastB = UP.doneBytes; return; }
  const dt = (now - UP.lastT) / 1000;
  if (dt >= 0.5){ UP.speed = (UP.doneBytes - UP.lastB) / dt; UP.lastT = now; UP.lastB = UP.doneBytes; updateHud(); }
}

async function addFiles(list){
  // 必须先固定成数组：调用方可能在 await 期间清空 input，导致 FileList 失效
  const files = Array.from(list || []);
  UP.doneFlash = false;
  let added = 0;
  for (const f of files){
    const ok = /^(image|video)\//.test(f.type) ||
      /\.(jpe?g|png|heic|heif|webp|gif|bmp|tiff?|dng|raw|cr2|nef|arw|mp4|mov|m4v|3gp|mkv|avi)$/i.test(f.name);
    if (!ok || f.size === 0) continue;
    const item = { id: 'u' + Date.now().toString(36) + Math.random().toString(36).slice(2, 7),
      name: f.name, size: f.size, owner: (uploadOwner || curOwner()), file: f, sid: '',
      // 文件本身的修改时间（手机上的拍摄/保存时间），不是上传时间
      mtime: Math.floor((f.lastModified || Date.now()) / 1000),
      dir: (f.webkitRelativePath || '').split('/').slice(0, -1).join('/') || '' };
    UP.items.push(item);
    added++;
    // 持久化失败（隐私模式/存储满）不能中断整个选择，否则只会传第一张
    try { await idbPut(item); } catch (e) { console.warn('idb 写入失败', e); }
  }
  if (!added) return;
  UP.totalBytes = UP.items.reduce((a, i) => a + i.size, 0);
  UP.doneBytes = 0; UP.lastT = 0; UP.lastB = 0;
  $('#hud').classList.add('show');
  updateHud();
  if (!speedTimer) speedTimer = setInterval(tickSpeed, 500);
  kick();
}

async function uploadOne(item){
  if (!item.sid){
    const r = await api('/api/upload/init', { method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ owner: item.owner, name: item.name, size: item.size, mtime: item.mtime || 0,
      folder: (item.dir || '').split('/')[0] || '',
      sub: (item.dir || '').split('/').slice(1).join('/'),
      secret: !!window.__uploadSecret }) });
    item.sid = r.sid; item.total = r.total; item.chunk = r.chunk;
    await idbPut(item);
  }
  const st = await api('/api/upload/' + item.sid);
  item.total = st.total; item.chunk = st.chunk;
  const have = new Set(st.received || []);
  $('#hudName').textContent = item.name;

  let next = 0, failed = 0;
  const CONC = 3;
  async function worker(){
    while (true){
      const i = next++;
      if (i >= item.total) return;
      const start = i * item.chunk, end = Math.min(item.size, start + item.chunk);
      if (have.has(i)){ UP.doneBytes += (end - start); updateHud(); continue; }
      const blob = item.file.slice(start, end);
      let tries = 0;
      while (true){
        try {
          const res = await fetch('/api/upload/' + item.sid + '/' + i, { method: 'PUT', body: blob });
          if (!res.ok) throw new Error('HTTP ' + res.status);
          break;
        } catch (e){
          if (++tries >= 3){ failed++; throw e; }
          await new Promise(r => setTimeout(r, 300 * tries));
        }
      }
      UP.doneBytes += (end - start);
      tickSpeed(); updateHud();
    }
  }
  await Promise.all(Array.from({ length: CONC }, worker));
  if (failed) throw new Error('部分分片失败');
  return await api('/api/upload/' + item.sid + '/complete', { method: 'POST' });
}

async function kick(){
  if (UP.running) return;
  UP.running = true;
  $('#hud').classList.add('show');
  const failed = [];
  while (UP.items.length){
    const item = UP.items[0];
    try {
      const r = await uploadOne(item);
      if (r && r.dedup) toast('已存在，秒传：' + item.name);
      // 视频：用浏览器（能解 H.264）截一帧当封面，路由器解不了
      if (r && r.asset && /^video\//.test(item.file.type || '')) {
        capturePoster(item.file, r.asset).catch(() => {});
      }
    } catch (e){
      console.error(e);
      failed.push(item.name);
    }
    UP.items.shift();
    await idbDel(item.id);
    updateHud();
    // 进度条一走完就刷新列表，不用等全部传完
    try { reset(); } catch (e) {}
  }
  if (failed.length) {
    toast('有 ' + failed.length + ' 个文件没传成功：' + failed.slice(0, 3).join('、'));
  }
  UP.running = false;
  if (speedTimer){ clearInterval(speedTimer); speedTimer = null; }
  UP.speed = 0; updateHud();
  setTimeout(() => $('#hud').classList.remove('show'), 1600);

  if (!failed.length) {
    // 全部成功：关掉上传面板，直接回到时间轴
    UP.doneFlash = true;
    if ($('#mUpload').classList.contains('show')) {
      $('#mUpload').classList.remove('show');
      await new Promise(r => setTimeout(r, 180));
    }
    renderUploadQueue();
    reset();
    try { window.scrollTo({ top: 0, behavior: 'smooth' }); } catch (e) { window.scrollTo(0, 0); }
    toast('上传完成，已回到相册首页');
  } else {
    // 有失败：留在面板上，方便看是哪几个
    toast('有 ' + failed.length + ' 个文件没传成功');
  }
}

// 上传按钮：先选目标（普通 / 秘密），再打开上传面板
$('#btnUpload').onclick = async () => {
  const pick = await uiChoose('上传到哪里？', [
    { key: 'normal', label: '普通相册' },
    { key: 'secret', label: '秘密相册（仅验证后可看）' }
  ]);
  if (!pick) return;
  if (pick === 'secret') {
    window.__uploadSecret = true;
    toast('上传目标：秘密相册');
  } else {
    window.__uploadSecret = false;
    toast('上传目标：普通相册');
  }
  openMobileUpload();
};
$('#fileInput').onchange = e => { const fs = Array.from(e.target.files || []); e.target.value = ''; addFiles(fs); };
document.addEventListener('dragstart', e => {
  // 查看器里拖拽图片是想平移，不是上传
  if (e.target && e.target.closest && e.target.closest('#viewer')) e.preventDefault();
});
document.addEventListener('dragover', e => { e.preventDefault(); document.body.classList.add('dragging'); });
document.addEventListener('dragleave', e => { if (!e.relatedTarget) document.body.classList.remove('dragging'); });
document.addEventListener('drop', e => {
  e.preventDefault(); document.body.classList.remove('dragging');
  if (e.dataTransfer && e.dataTransfer.files.length) addFiles(e.dataTransfer.files);
});
document.addEventListener('paste', e => {
  const fs = e.clipboardData && e.clipboardData.files;
  if (fs && fs.length) addFiles(fs);
});

/* ---- 鉴权 ---- */
let authMode = 'login';

function hideAuth(){ $('#auth').classList.remove('show'); }

async function bootAuth(){
  let st = { needs_setup: true };
  try { st = await (await fetch('/api/setup-state')).json(); } catch (e) {}
  authMode = st.needs_setup ? 'setup' : 'login';
  $('#auth').classList.add('show');
  $('#authErr').textContent = '';
  if (st.needs_setup){
    $('#authTitle').textContent = '欢迎使用 拾光相册';
    $('#authSub').textContent = '首次使用：为 lwm 和 cr 各设一个密码（至少 4 位）。管理员账号会自动创建。';
    $('#authUser').style.display = 'none';
    $('#authPass').placeholder = 'lwm 的密码';
    $('#authPass2').style.display = 'block';
    $('#authPass2').placeholder = 'cr 的密码';
    return;
  }
  $('#authTitle').textContent = '拾光相册';
  $('#authSub').textContent = '输入账号和密码';
  $('#authUser').style.display = 'block';
  $('#authPass2').style.display = 'none';
  $('#authPass').placeholder = '密码';
  $('#authUser').value = localStorage.getItem('lumina_user') || '';
  if ($('#authUser').value) { setTimeout(() => $('#authPass').focus(), 60); }
  try {
    const me = await (await fetch('/api/me')).json();
    if (me && me.user){ hideAuth(); startApp(); }
  } catch (e) {}
}

$('#authGo').onclick = async () => {
  const errEl = $('#authErr');
  errEl.textContent = '';
  const pw = $('#authPass').value;
  try {
    if (authMode === 'setup'){
      const pw2 = $('#authPass2').value;
      if (pw.length < 4 || pw2.length < 4) { errEl.textContent = '密码至少 4 位'; return; }
      const r = await fetch('/api/setup', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ users: [{ name: 'lwm', password: pw }, { name: 'cr', password: pw2 }] }) });
      if (!r.ok) { errEl.textContent = '初始化失败：' + (await r.text()); return; }
      authMode = 'login';
      $('#authTitle').textContent = '拾光相册';
      $('#authSub').textContent = '设置完成，请用账号密码登录';
      $('#authUser').style.display = 'block';
      $('#authPass2').style.display = 'none';
      $('#authPass').placeholder = '密码';
      $('#authPass').value = '';
      return;
    }
    if (authMode === 'register'){
      const rn = $('#authUser').value.trim();
      const space = $('#authSpace').value;
      if (rn.length < 2) { errEl.textContent = '账号至少 2 位'; return; }
      if (pw.length < 4) { errEl.textContent = '密码至少 4 位'; return; }
      const rr = await fetch('/api/register', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: rn, password: pw, space }) });
      if (!rr.ok){
        let msg = '注册失败';
        try { msg = (await rr.json()).error || msg; } catch (e) {}
        errEl.textContent = msg;
        return;
      }
      errEl.textContent = '';
      openLogin();
      $('#authUser').value = rn;
      $('#authPass').value = '';
      $('#authSub').textContent = '注册成功，请登录';
      return;
    }
    const name = $('#authUser').value.trim();
    if (!name) { errEl.textContent = '请输入账号'; return; }
    const r = await fetch('/api/login', { method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, password: pw }) });
    if (!r.ok){ errEl.textContent = '账号或密码不正确'; return; }
    localStorage.setItem('lumina_user', name);
    hideAuth();
    startApp();
  } catch (e){ errEl.textContent = '网络错误：' + e.message; }
};
$('#authUser').onkeydown = e => { if (e.key === 'Enter') $('#authPass').focus(); };
$('#authPass').onkeydown = e => { if (e.key === 'Enter') $('#authGo').click(); };
$('#authPass2').onkeydown = e => { if (e.key === 'Enter') $('#authGo').click(); };

/* ---- P3 多选与批量操作 ---- */
let selMode = false;
const selSet = new Set();

function setSelMode(on){
  selMode = on;
  document.body.classList.toggle('selmode', on);
  if (!on) selSet.clear();
  renderSel();
}
function toggleSel(id){
  if (selSet.has(id)) selSet.delete(id); else selSet.add(id);
  renderSel();
}
function renderSel(){
  document.querySelectorAll('.cell').forEach(c => c.classList.toggle('sel', selSet.has(+c.dataset.id)));
  $('#selCount').textContent = selSet.size;
  $('#batchbar').classList.toggle('show', selMode);
  $('#btnSelect').style.color = selMode ? 'var(--a1)' : '';
  syncMonthSel();
}

/** 同步每个月份标题上「全选本月」的勾选状态 */
function syncMonthSel(){
  document.querySelectorAll('.msel').forEach(b => {
    const ym = b.dataset.ym;
    const list = (state.monthItems && state.monthItems.get(ym)) || [];
    const all = list.length > 0 && list.every(it => selSet.has(it.id));
    b.textContent = all ? '☑ 全选本月' : '☐ 全选本月';
    b.style.color = all ? 'var(--a1)' : '';
  });
}

/** 点月份标题：整月全选 / 取消 */
function selMonth(ym){
  const list = (state.monthItems && state.monthItems.get(ym)) || [];
  if (!list.length) return;
  const all = list.every(it => selSet.has(it.id));
  if (all) list.forEach(it => selSet.delete(it.id));
  else list.forEach(it => selSet.add(it.id));
  setSelMode(true);
  renderSel();
}

/** 全选当前已加载的全部照片/视频 */
function selectAllItems(){
  for (const it of state.items) selSet.add(it.id);
  setSelMode(true);
  renderSel();
  toast('已选 ' + selSet.size + ' 项');
}
async function batch(action, ids){
  if (!ids.length) return;
  const r = await api('/api/' + action, { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }) });
  return r;
}

$('#btnSelect').onclick = () => { setSelMode(!selMode); if (selMode) toast('已进入多选，点击照片选择'); };
$('#bCancel').onclick = () => setSelMode(false);
if ($('#bAll')) $('#bAll').onclick = () => selectAllItems();
// 月份标题的勾选框用事件委托（capture 阶段），避免每渲染一次就重绑
document.addEventListener('click', e => {
  const b = e.target.closest && e.target.closest('.msel');
  if (!b) return;
  e.preventDefault(); e.stopPropagation();
  selMonth(b.dataset.ym);
}, true);
$('#bTrash').onclick = async () => {
  const ids = [...selSet];
  if (!ids.length) return;
  const r = await batch('trash', ids);
  toast('已移入回收站 ' + (r && r.moved || 0) + ' 项');
  setSelMode(false); reset();
};
$('#bFav').onclick = async () => {
  const ids = [...selSet]; if (!ids.length) return;
  await api('/api/batch/fav', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids, value: 1 }) });
  toast('已收藏 ' + ids.length + ' 项'); setSelMode(false);
};
$('#bPush').onclick = async () => {
  const ids = [...selSet]; if (!ids.length) return;
  const r = await batch('batch/push', ids);
  toast('已推送到家庭共享 ' + (r && r.pushed || 0) + ' 项（硬链接，不占额外空间）');
  setSelMode(false); reset();
};
$('#bZip').onclick = async () => {
  const ids = [...selSet]; if (!ids.length) return;
  toast('正在打包 ' + ids.length + ' 项…');
  try {
    const res = await fetch('/api/zip', { method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ids }) });
    if (!res.ok) throw new Error('HTTP ' + res.status);
    const blob = await res.blob();
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = 'lumina-' + new Date().toISOString().slice(0, 10) + '.zip';
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 5000);
    toast('已下载 ' + (blob.size / 1048576).toFixed(1) + ' MB');
  } catch (e){ toast('打包失败：' + e.message); }
  setSelMode(false);
};

/* ---- P3 回收站 ---- */
function daysLeft(ts){ return Math.max(0, Math.ceil((ts - Date.now() / 1000) / 86400)); }

const trashSel = new Set();

function syncTrashBar(){
  const n = trashSel.size;
  const total = document.querySelectorAll('#trashList .item').length;
  $('#trashCount').textContent = n ? ('已选 ' + n + ' 项') : '未选择';
  $('#trashRestore').disabled = n === 0;
  $('#trashPurge').disabled = n === 0;
  const all = $('#trashAll');
  all.checked = total > 0 && n === total;
  all.indeterminate = n > 0 && n < total;
  document.querySelectorAll('#trashList .item').forEach(el => {
    const on = trashSel.has(+el.dataset.id);
    el.classList.toggle('trash-checked', on);
    const cb = el.querySelector('.trash-cb');
    if (cb) cb.checked = on;
  });
}

async function openTrash(){
  const r = await api('/api/trash');
  const box = $('#trashList');
  trashSel.clear();
  if (!r.items.length){
    box.innerHTML = '<div class="empty">回收站是空的</div>';
  } else {
    box.innerHTML = r.items.map(it =>
      '<div class="item" data-id="' + it.id + '">' +
        '<input type="checkbox" class="trash-cb" data-id="' + it.id + '">' +
        '<img src="/t/' + it.id + '/m.jpg" alt="">' +
        '<div class="m"><div class="nm">' + esc(it.name) + '</div>' +
        '<div class="sub">' + fmtB(it.size) + ' · ' + daysLeft(it.purge_at) + ' 天后清理</div></div>' +
        '<button class="ibtn" data-act="restore" title="恢复">↩</button>' +
        '<button class="ibtn" data-act="purge" title="彻底删除">✕</button>' +
      '</div>').join('');
  }
  syncTrashBar();
  $('#trashDrawer').classList.add('show');
}

$('#trashAll').onchange = e => {
  trashSel.clear();
  if (e.target.checked) {
    document.querySelectorAll('#trashList .item').forEach(el => trashSel.add(+el.dataset.id));
  }
  syncTrashBar();
};

$('#trashRestore').onclick = async () => {
  const ids = [...trashSel];
  if (!ids.length) return;
  const r = await api('/api/restore', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }) });
  toast('已恢复 ' + (r.restored || 0) + ' 项');
  await openTrash();
  reset();
};

$('#trashPurge').onclick = async () => {
  const ids = [...trashSel];
  if (!ids.length) return;
  if (!(await uiConfirm('彻底删除 ' + ids.length + ' 项', '删除后无法恢复，确定？', '删除', true))) return;
  const r = await api('/api/purge', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }) });
  toast('已彻底删除 ' + (r.purged || 0) + ' 项');
  await openTrash();
};
$('#btnTrash').onclick = openTrash;
$('#trashClose').onclick = () => $('#trashDrawer').classList.remove('show');
$('#trashList').onclick = async e => {
  const cb = e.target.closest('.trash-cb');
  if (cb){
    const id = +cb.dataset.id;
    if (cb.checked) trashSel.add(id); else trashSel.delete(id);
    syncTrashBar();
    return;
  }
  const b = e.target.closest('button'); if (!b) return;
  const id = +b.closest('.item').dataset.id;
  if (b.dataset.act === 'restore'){
    await api('/api/restore', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ids: [id] }) });
    toast('已恢复'); openTrash(); reset();
  } else {
    if (!(await uiConfirm('彻底删除', '删除后无法恢复，确定？', '删除', true))) return;
    await fetch('/api/trash/' + id, { method: 'DELETE' });
    toast('已彻底删除'); openTrash();
  }
};

/* ---- P3 幻灯片 ---- */
let ssTimer = null, ssIdx = 0, ssFlip = false, ssList = [];
// 幻灯片要把整个时间轴都取下来，而不是只播当前加载的这一页
async function fetchAllPhotos(){
  const all = [];
  let before = '';
  for (let guard = 0; guard < 60; guard++){
    const q = new URLSearchParams({ owner: state.owner, kind: 'photo', limit: '200' });
    if (state.person) q.set('person', state.person);
    if (state.album) q.set('album', state.album);
    if (state.folder) q.set('folder', state.folder);
    if (before) q.set('before', before);
    const r = await api('/api/timeline?' + q);
    if (!r.items || !r.items.length) break;
    for (const it of r.items) all.push(it);
    if (!r.next) break;
    before = r.next;
  }
  return all;
}

async function startSlideshow(){
  toast('正在收集全部照片…');
  let list = [];
  try { list = await fetchAllPhotos(); } catch (e) { list = []; }
  if (!list.length) list = state.items.filter(x => x.kind === 'photo');
  ssList = list;
  if (!ssList.length) return toast('没有可播放的照片');
  ssIdx = 0; ssFlip = false;
  $('#slideshow').classList.add('show');
  drawSlide();
  ssTimer = setInterval(() => { ssIdx = (ssIdx + 1) % ssList.length; drawSlide(); }, 5000);
  if ('wakeLock' in navigator) navigator.wakeLock.request('screen').catch(() => {});
  toast('幻灯片：共 ' + ssList.length + ' 张');
}
function drawSlide(){
  const it = ssList[ssIdx]; if (!it) return;
  const a = $('#ssA'), b = $('#ssB');
  const target = ssFlip ? a : b, other = ssFlip ? b : a;
  target.querySelector('img').src = '/m/' + it.id;
  target.classList.add('on'); other.classList.remove('on');
  ssFlip = !ssFlip;
  $('#ssCap').innerHTML = '<b>' + labelOf(it.year_month) + ' · ' + esc(it.owner) + '</b>' +
    (it.camera ? esc(it.camera) + ' · ' : '') + (ssIdx + 1) + ' / ' + ssList.length;
}
function stopSlideshow(){
  $('#slideshow').classList.remove('show');
  clearInterval(ssTimer); ssTimer = null;
}
$('#btnSlideshow').onclick = startSlideshow;
$('#ssClose').onclick = stopSlideshow;

/* ---- P3 查看器动作 ---- */
$('#vFav').onclick = async () => {
  const a = state.items[vIdx]; if (!a) return;
  $('#vFav').classList.toggle('faved');
  await api('/api/batch/fav', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids: [a.id], value: $('#vFav').classList.contains('faved') ? 1 : 0 }) });
  toast('已更新收藏');
};
$('#vShare').onclick = async () => {
  const a = state.items[vIdx]; if (!a) return;
  const r = await api('/api/share', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: a.id, hours: 24 * 7 }) });
  const url = location.origin + r.url;
  try { await navigator.clipboard.writeText(url); toast('分享链接已复制（7 天有效）'); }
  catch (e){ uiPrompt('分享链接（7 天有效）', { message: '复制失败，可手动复制下面的链接', value: url }); }
};
$('#vDown').onclick = () => {
  const a = state.items[vIdx]; if (!a) return;
  const link = document.createElement('a');
  link.href = '/m/' + a.id; link.download = a.name; link.click();
};
$('#vRotate').onclick = async () => {
  const a = state.items[vIdx]; if (!a) return;
  if (a.kind !== 'photo') { toast('视频不支持旋转'); return; }
  const btn = $('#vRotate');
  if (btn.disabled) return;
  btn.disabled = true;
  try {
    const r = await api('/api/rotate/' + a.id, { method: 'POST',
      headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ dir: 1 }) });
    // 服务器已经把像素转好并写回原文件了，这里同步本地状态
    a.w = r.w; a.h = r.h; a.mtime = r.mtime;
    showAsset();                       // 大图用新的 mtime 重新拉
    const ph = document.querySelector('.cell[data-id="' + a.id + '"] .ph');
    if (ph) ph.src = '/t/' + a.id + '/t.jpg?v=' + r.mtime;
    toast('已旋转并保存');
  } catch (err) {
    toast('旋转失败：' + err.message);
  } finally {
    btn.disabled = false;
  }
};
$('#vDel').onclick = async () => {
  const a = state.items[vIdx]; if (!a) return;
  if (!(await uiConfirm('移入回收站', '30 天内可以恢复。', '移入', false))) return;
  await api('/api/trash', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids: [a.id] }) });
  closeViewer(); toast('已移入回收站'); reset();
};

$('#btnUser').onclick = () => $('#userMenu').classList.toggle('show');
// 顶栏账号菜单：显示当前账号 + 更换账号 / 注册 / 改密 / 退出
fetch('/api/me').then(r => r.ok ? r.json() : null).then(me => {
  if (me && me.user) {
    $('#mWho').textContent = '当前账号：' + me.user;
    const ini = (me.user || 'U')[0].toUpperCase();
    $('#userInitial').textContent = ini;
  }
}).catch(() => {});

$('#mSwitch').onclick = async () => {
  $('#userMenu').classList.remove('show');
  await fetch('/api/logout', { method: 'POST' });
  localStorage.removeItem('lumina_user');
  location.reload();
};
$('#mReg').onclick = () => { $('#userMenu').classList.remove('show'); openRegister(); };
$('#mOut').onclick = async () => {
  await fetch('/api/logout', { method: 'POST' });
  location.reload();
};
$('#mPwd').onclick = async () => {
  $('#userMenu').classList.remove('show');
  const oldPw = await uiPrompt('修改密码', { message: '请输入当前密码', placeholder: '当前密码' });
  if (!oldPw) return;
  const newPw = await uiPrompt('修改密码', { message: '新密码至少 4 位', placeholder: '新密码' });
  if (!newPw) return;
  const r = await fetch('/api/password', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ old: oldPw, new: newPw }) });
  toast(r.ok ? '密码已修改' : '修改失败：' + (await r.text()));
};
document.addEventListener('click', e => {
  if (!e.target.closest('#userMenu') && !e.target.closest('#btnUser'))
    $('#userMenu').classList.remove('show');
});
async function setUserChip(){
  try {
    const me = await (await fetch('/api/me')).json();
    if (me && me.user) $('#userInitial').textContent = me.user.toUpperCase();
  } catch (e) {}
}

/* ---- P4 离线足迹地图 ---- */
let LAND = null;
async function drawMap(places){
  const cv = $('#mapCanvas');
  if (!cv) return;
  if (!LAND){
    try { LAND = await (await fetch('/land.json')).json(); } catch (e){ LAND = []; }
  }
  const w = cv.clientWidth || 360, h = cv.clientHeight || 190;
  const dpr = Math.min(2, window.devicePixelRatio || 1);
  cv.width = w * dpr; cv.height = h * dpr;
  const ctx = cv.getContext('2d');
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);

  // 视窗：按数据拟合，至少 18 度跨度，避免过度放大
  let lon0 = 73, lon1 = 135, lat0 = 18, lat1 = 54;
  if (places && places.length){
    lon0 = Math.min(...places.map(p => p.lon)); lon1 = Math.max(...places.map(p => p.lon));
    lat0 = Math.min(...places.map(p => p.lat)); lat1 = Math.max(...places.map(p => p.lat));
  }
  const padLon = Math.max(2, (lon1 - lon0) * 0.25), padLat = Math.max(2, (lat1 - lat0) * 0.25);
  lon0 -= padLon; lon1 += padLon; lat0 -= padLat; lat1 += padLat;
  if (lon1 - lon0 < 18){ const c = (lon0 + lon1) / 2; lon0 = c - 9; lon1 = c + 9; }
  if (lat1 - lat0 < 14){ const c = (lat0 + lat1) / 2; lat0 = c - 7; lat1 = c + 7; }

  const px = lon => (lon - lon0) / (lon1 - lon0) * w;
  const py = lat => (lat1 - lat) / (lat1 - lat0) * h;

  // 陆地
  ctx.beginPath();
  for (const ring of LAND){
    let started = false;
    for (let i = 0; i < ring.length; i += 2){
      const x = px(ring[i] / 10), y = py(ring[i + 1] / 10);
      if (!started){ ctx.moveTo(x, y); started = true; } else ctx.lineTo(x, y);
    }
    ctx.closePath();
  }
  ctx.fillStyle = 'rgba(255,255,255,.045)';
  ctx.fill();
  ctx.strokeStyle = 'rgba(255,255,255,.10)';
  ctx.lineWidth = 0.6;
  ctx.stroke();

  // 点位
  const maxC = Math.max(1, ...(places || []).map(p => p.count));
  for (const p of (places || [])){
    const x = px(p.lon), y = py(p.lat);
    const r = 3 + Math.sqrt(p.count / maxC) * 4;
    const g = ctx.createRadialGradient(x, y, 0, x, y, r * 3.2);
    g.addColorStop(0, 'rgba(94,234,212,.55)');
    g.addColorStop(1, 'rgba(94,234,212,0)');
    ctx.fillStyle = g;
    ctx.beginPath(); ctx.arc(x, y, r * 3.2, 0, Math.PI * 2); ctx.fill();
    ctx.fillStyle = '#5EEAD4';
    ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2); ctx.fill();
    ctx.fillStyle = 'rgba(242,243,245,.9)';
    ctx.font = '11px system-ui, sans-serif';
    ctx.fillText(p.city, x + r + 4, y + 3.5);
  }

  if (!places || !places.length){
    ctx.fillStyle = 'rgba(155,161,172,.75)';
    ctx.font = '12px system-ui, sans-serif';
    ctx.fillText('还没有带 GPS 坐标的照片', 14, h - 16);
  }
}

/* ---- P4 智能抽屉 ---- */
let smartTab = 'dupes';
async function openSmart(tab){
  smartTab = tab || smartTab;
  $('#smartDrawer').classList.add('show');
  document.querySelectorAll('#smartTabs button').forEach(b => b.classList.toggle('on', b.dataset.t === smartTab));
  const box = $('#smartList');
  box.innerHTML = '<div class="empty">分析中…</div>';
  try {
    if (smartTab === 'dupes'){
      const r = await api('/api/dupes');
      if (!r.groups.length) return box.innerHTML = '<div class="empty">没有发现相似照片</div>';
      box.innerHTML = r.groups.map(g =>
        '<div class="dupgrp"><div class="thumbs">' +
        g.items.slice(0, 4).map(it => '<img src="/t/' + it.id + '/m.jpg" data-id="' + it.id + '">').join('') +
        '</div><div class="meta">' + g.items.length + ' 张相似 · 可释放 ' +
        fmtB(g.wasted) + '</div></div>').join('');
    } else if (smartTab === 'events'){
      const r = await api('/api/events');
      box.innerHTML = r.events.length ? r.events.map(e =>
        '<div class="evrow"><img src="/t/' + e.cover + '/m.jpg"><div class="m"><div class="nm">' +
        esc(e.title) + '</div><div class="sub">' + e.count + ' 项</div></div></div>').join('')
        : '<div class="empty">还没有可聚合的事件</div>';
    } else if (smartTab === 'places'){
      const r = await api('/api/places');
      box.innerHTML = '<canvas id="mapCanvas"></canvas>' + (r.places.length ? r.places.map(p =>
        '<div class="plrow"><img src="/t/' + p.cover + '/m.jpg"><div class="m"><div class="nm">' +
        esc(p.city) + '</div><div class="sub">' + p.count + ' 张</div></div></div>').join('')
        : '<div class="empty">没有带 GPS 的照片</div>');
      drawMap(r.places);
    } else {
      const r = await api('/api/people');
      box.innerHTML = r.people.length ? r.people.map(p =>
        '<div class="plrow" data-pid="' + p.id + '"><img src="/f/' + p.cover + '.jpg" style="border-radius:50%">' +
        '<div class="m"><div class="nm">' + (p.name ? esc(p.name) : '未命名') + '</div>' +
        '<div class="sub">' + p.count + ' 张照片</div></div>' +
        '<button class="ibtn" data-act="name" title="命名">✎</button></div>').join('')
        : '<div class="empty">还没有检测到人物<br><span style="font-size:11px;color:#5C6270">在电脑上运行 tools/face_agent.py 后刷新</span></div>';
    }
  } catch (e){ box.innerHTML = '<div class="empty">加载失败</div>'; }
}
$('#btnSmart').onclick = () => openSmart('dupes');
$('#smartClose').onclick = () => $('#smartDrawer').classList.remove('show');
$('#smartList').onclick = async e => {
  const b = e.target.closest('button[data-act="name"]');
  if (b) {
    const pid = b.closest('.plrow').dataset.pid;
    const name = await uiPrompt('给这个人起个名字', { placeholder: '名字' });
    if (name === null) return;
    await api('/api/people/' + pid + '/name', { method: 'POST',
      headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name }) });
    toast('已命名');
    openSmart('people');
    return;
  }
  const row = e.target.closest('.plrow[data-pid]');
  if (row && smartTab === 'people') {
    $('#smartDrawer').classList.remove('show');
    setFilter('person', +row.dataset.pid, '人物相册');
  }
};
$('#smartTabs').onclick = e => {
  const b = e.target.closest('button'); if (!b) return;
  openSmart(b.dataset.t);
};

/* ---- 相册与过滤 ---- */
function setFilter(kind, id, label) {
  state.person = kind === 'person' ? id : 0;
  state.album = kind === 'album' ? id : 0;
  state.folder = kind === 'folder' ? id : '';
  const chip = $('#filterChip');
  if (!kind) {
    chip.classList.remove('show');
    chip.innerHTML = '';
  } else {
    chip.innerHTML = '<span>' + esc(label) + '</span><button title="清除过滤">✕</button>';
    chip.classList.add('show');
    chip.querySelector('button').onclick = () => setFilter('', 0, '');
  }
  reset();
}

async function refreshAlbums() {
  const r = await api('/api/albums');
  const box = $('#albumList');
  box.innerHTML = r.albums.length ? r.albums.map(a =>
    '<div class="albumrow" data-id="' + a.id + '">' +
      '<img src="/t/' + a.cover + '/m.jpg" alt="">' +
      '<div class="m"><div class="nm">' + esc(a.name) + '</div><div class="sub">' + a.count + ' 项</div></div>' +
      '<button class="ibtn" data-act="rename" title="重命名">✎</button>' +
      '<button class="ibtn" data-act="del" title="删除相册">✕</button>' +
    '</div>').join('')
    : '<div class="empty">还没有相册<br><span style="font-size:11px;color:#5C6270">在照片上点「多选」后可加入相册</span></div>';
  return r.albums;
}

$('#btnAlbum').onclick = async () => {
  await refreshAlbums();
  $('#albumDrawer').classList.add('show');
};
$('#albumClose').onclick = () => $('#albumDrawer').classList.remove('show');

/* ---- 文件夹分区多选 ---- */
const fSel = new Set();
let fAll = [];

function fSyncBar(){
  const n = fSel.size;
  const cnt = $('#fSelCount'), del = $('#fDel'), sec = $('#fSec'), fam = $('#fFam');
  if (cnt) cnt.textContent = n ? ('已选 ' + n + ' 个分区') : '未选';
  if (del) del.disabled = n === 0;
  if (sec) sec.disabled = n === 0;
  if (fam) fam.disabled = n === 0;
  document.querySelectorAll('#folderList .fchk').forEach(c => {
    c.classList.toggle('on', fSel.has(c.dataset.chk));
  });
}

function fToggle(name){
  if (fSel.has(name)) fSel.delete(name); else fSel.add(name);
  fSyncBar();
}

async function fBatchDelete(){
  // 关键：带上分区参数，否则「共享里删文件夹」会把普通相册的同一文件夹一起删掉
  const fq = state.family ? '?owner=family' : (state.secret ? '?secret=1' : '');

  const names = [...fSel];
  if (!names.length) return;
  const ok = await uiConfirm('删除 ' + names.length + ' 个分区？',
    names.slice(0, 6).join('、') + (names.length > 6 ? ' 等' : '') +
    '\n里面的照片会移入回收站，30 天内可以恢复。');
  if (!ok) return;
  let moved = 0, fail = 0;
  for (const n of names) {
    try {
      const r = await api('/api/folders/' + encodeURIComponent(n) + fq, { method: 'DELETE' });
      moved += (r.moved || 0);
    } catch (e) { fail++; }
  }
  fSel.clear();
  toast('已删除 ' + (names.length - fail) + ' 个分区，' + moved + ' 项移入回收站' +
        (fail ? '，失败 ' + fail + ' 个' : ''));
  await refreshFolders();
  reset();
}

/** 把选中的文件夹整个推送到家庭共享空间（服务器做硬链接，不占额外空间） */
async function fBatchFamily(){
  const names = [...fSel];
  if (!names.length) return;
  let pushed = 0, total = 0, fail = 0;
  for (const n of names) {
    try {
      const r = await api('/api/family/folder', { method: 'POST',
        body: JSON.stringify({ name: n }) });
      pushed += (r.pushed || 0);
      total += (r.total || 0);
    } catch (e) { fail++; }
  }
  fSel.clear();
  toast('已推送 ' + pushed + ' / ' + total + ' 张到家庭共享' + (fail ? '（失败 ' + fail + ' 个分区）' : ''));
  await refreshFolders();
  reset();
}

async function fBatchSecret(){
  const names = [...fSel];
  if (!names.length) return;
  const to = !state.secret;
  let moved = 0, fail = 0;
  for (const n of names) {
    try {
      const r = await api('/api/secret/folder', { method: 'POST',
        body: JSON.stringify({ name: n, secret: to }) });
      moved += (r.moved || 0);
    } catch (e) { fail++; }
  }
  fSel.clear();
  toast('已把 ' + (names.length - fail) + ' 个分区' + (to ? '移入' : '移出') +
        '秘密相册，' + moved + ' 项' + (fail ? '，失败 ' + fail + ' 个' : ''));
  await refreshFolders();
  reset();
}

async function refreshFolders() {
  const fq = state.family ? '?owner=family' : (state.secret ? '?secret=1' : '');
  const r = await api('/api/folders' + fq);
  fAll = r.folders.map(f => f.name);
  fSel.clear();
  const box = $('#folderList');
  box.innerHTML = r.folders.length ? r.folders.map(f =>
    '<div class="albumrow" data-folder="' + esc(f.name) + '">' +
      '<div class="fchk" data-chk="' + esc(f.name) + '"></div>' +
      (f.cover ? '<img src="/t/' + f.cover + '/m.jpg" alt="">' : '<div class="noface" style="width:52px;height:52px;border-radius:8px;background:#0B0D12;border:1px solid var(--hair2);display:grid;place-items:center;flex:none">🔒</div>') +
      '<div class="m"><div class="nm">' + esc(f.name) + '</div><div class="sub">' + f.count + ' 项</div></div>' +
      '<button class="ibtn" data-sec="' + esc(f.name) + '" title="' + (state.secret ? '移出秘密相册' : '移入秘密相册') + '">🔒</button>' +
      '<button class="ibtn" data-del="' + esc(f.name) + '" title="删除分区">✕</button>' +
    '</div>').join('')
    : '<div class="empty">还没有文件夹<br><span style="font-size:11px;color:#5C6270">在电脑上打开本页，用「选择整个文件夹」上传，就会在这里生成分区</span></div>';
  fSyncBar();
  box.onclick = async e => {
    const chk = e.target.closest('[data-chk]');
    if (chk) { fToggle(chk.dataset.chk); return; }
    const sec = e.target.closest('[data-sec]');
    if (sec){
      const fname = sec.dataset.sec;
      try {
        const to = !state.secret;
        const rr = await api('/api/secret/folder', { method: 'POST',
          body: JSON.stringify({ name: fname, secret: to }) });
        toast('已把分区「' + fname + '」' + (to ? '移入' : '移出') + '秘密相册，' + (rr.moved || 0) + ' 项');
        await refreshFolders();
        reset();
      } catch (err) { toast('操作失败：' + err.message); }
      return;
    }
    const del = e.target.closest('[data-del]');
    if (del){
      const name = del.dataset.del;
      const ok = await uiConfirm('删除分区「' + name + '」？',
        '分区里的照片会移入回收站，30 天内可以恢复。');
      if (!ok) return;
      try {
        const r = await api('/api/folders/' + encodeURIComponent(name), { method: 'DELETE' });
        toast('已删除分区，' + r.moved + ' 项移入回收站');
        await refreshFolders();
        reset();
      } catch (err) { toast('删除失败：' + err.message); }
      return;
    }
    const row = e.target.closest('[data-folder]');
    if (!row) return;
    const name = row.dataset.folder;
    $('#folderDrawer').classList.remove('show');
    setFilter('folder', name, name);
  };
}

$('#fSelAll').onclick = () => { fAll.forEach(n => fSel.add(n)); fSyncBar(); };
$('#fSelNone').onclick = () => { fSel.clear(); fSyncBar(); };
$('#fDel').onclick = () => fBatchDelete();
$('#fSec').onclick = () => fBatchSecret();
$('#fFam').onclick = () => fBatchFamily();

$('#btnFolder').onclick = async () => {
  await refreshFolders();
  $('#folderDrawer').classList.add('show');
};
$('#folderClose').onclick = () => $('#folderDrawer').classList.remove('show');

$('#albumNew').onclick = async () => {
  const name = await uiPrompt('新建相册', { placeholder: '相册名称' });
  if (!name || !name.trim()) return;
  const ids = selMode ? [...selSet] : [];
  await api('/api/albums', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: name.trim(), ids }) });
  toast('已创建相册');
  if (selMode) setSelMode(false);
  await refreshAlbums();
};

$('#albumList').onclick = async e => {
  const row = e.target.closest('.albumrow');
  if (!row) return;
  const id = +row.dataset.id;
  const name = row.querySelector('.nm').textContent;
  const b = e.target.closest('button');
  if (b && b.dataset.act === 'rename') {
    const nn = await uiPrompt('重命名相册', { value: name });
    if (!nn || !nn.trim()) return;
    await api('/api/albums/' + id + '/rename', { method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: nn.trim() }) });
    await refreshAlbums();
    toast('已重命名');
    return;
  }
  if (b && b.dataset.act === 'del') {
    if (!(await uiConfirm('删除相册', '「' + name + '」里的照片不会被删除。', '删除', true))) return;
    await fetch('/api/albums/' + id, { method: 'DELETE' });
    if (state.album === id) setFilter('', 0, '');
    await refreshAlbums();
    toast('已删除相册');
    return;
  }
  $('#albumDrawer').classList.remove('show');
  setFilter('album', id, '相册 · ' + name);
};

$('#bAlbum').onclick = async () => {
  const ids = [...selSet];
  if (!ids.length) return;
  const name = await uiPrompt('加入相册', { message: '输入已有相册名会并入，输入新名称会新建', placeholder: '相册名称' });
  if (!name || !name.trim()) return;
  const list = (await api('/api/albums')).albums;
  const hit = list.find(a => a.name === name.trim());
  const payload = JSON.stringify({ ids });
  const opts = { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: payload };
  if (hit) await api('/api/albums/' + hit.id + '/add', opts);
  else await api('/api/albums', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: name.trim(), ids }) });
  toast('已加入相册 ' + name.trim());
  setSelMode(false);
};

/* ---- 应用内对话框：不用 prompt/confirm，避免阻塞渲染线程 ---- */
function askDialog(opts) {
  return new Promise(resolve => {
    const m = $('#modal'), inp = $('#minput'), ok = $('#mok'), cancel = $('#mcancel');
    $('#mtitle').textContent = opts.title || '';
    $('#mmsg').textContent = opts.message || '';
    $('#mmsg').style.display = opts.message ? 'block' : 'none';
    if (opts.input) {
      inp.style.display = 'block';
      inp.value = opts.value || '';
      inp.placeholder = opts.placeholder || '';
      setTimeout(() => { inp.focus(); inp.select(); }, 30);
    } else {
      inp.style.display = 'none';
    }
    ok.textContent = opts.okText || '确定';
    ok.className = 'primary' + (opts.danger ? ' danger' : '');
    m.classList.add('show');
    const finish = v => {
      m.classList.remove('show');
      ok.onclick = null; cancel.onclick = null; inp.onkeydown = null; m.onclick = null;
      resolve(v);
    };
    ok.onclick = () => finish(opts.input ? inp.value.trim() : true);
    cancel.onclick = () => finish(opts.input ? null : false);
    inp.onkeydown = e => {
      if (e.key === 'Enter') ok.click();
      if (e.key === 'Escape') cancel.click();
    };
    m.onclick = e => { if (e.target === m) finish(opts.input ? null : false); };
  });
}
const uiPrompt = (title, o) => askDialog(Object.assign({ title, input: true }, o || {}));
const uiConfirm = (title, message, okText, danger) => askDialog({ title, message, okText, danger });



/* ---- 查看器缩放：滚轮 / 双指 / 双击 ---- */
function enableZoom(stage, img) {
  let scale = 1, tx = 0, ty = 0, dragging = false, sx = 0, sy = 0;
  const apply = () => {
    img.style.transform = 'translate(' + tx + 'px,' + ty + 'px) scale(' + scale + ')';
    img.style.cursor = scale > 1 ? 'grab' : 'zoom-in';
  };
  const reset = () => { scale = 1; tx = 0; ty = 0; apply(); };
  img.style.transition = 'transform .12s ease-out';
  img.style.transformOrigin = 'center center';
  img.style.touchAction = 'none';
  // 关键：图片默认可拖拽，一拖就触发浏览器 drag 事件，
  // 页面「拖进来上传」的逻辑被误触发，看起来像拖一下就弹出上传。
  img.draggable = false;
  img.style.userSelect = 'none';

  stage.addEventListener('wheel', e => {
    if (!stage.contains(img)) return;
    e.preventDefault();
    const step = e.deltaY < 0 ? 1.18 : 1 / 1.18;
    scale = Math.min(20, Math.max(1, scale * step));   // 上限 20 倍
    if (scale === 1) { tx = 0; ty = 0; }
    apply();
  }, { passive: false });

  img.addEventListener('dblclick', e => {
    e.preventDefault();
    if (scale > 1) reset();
    else { scale = 2.5; apply(); }
  });

  img.addEventListener('pointerdown', e => {
    if (scale <= 1) return;
    dragging = true; sx = e.clientX - tx; sy = e.clientY - ty;
    img.setPointerCapture(e.pointerId);
    img.style.cursor = 'grabbing';
  });
  img.addEventListener('pointermove', e => {
    if (!dragging) return;
    tx = e.clientX - sx; ty = e.clientY - sy;
    apply();
  });
  img.addEventListener('pointerup', e => {
    dragging = false;
    img.style.cursor = scale > 1 ? 'grab' : 'zoom-in';
    try { img.releasePointerCapture(e.pointerId); } catch (err) {}
  });

  // 双指缩放
  let pinch = 0;
  stage.addEventListener('touchstart', e => {
    if (e.touches.length === 2) {
      pinch = Math.hypot(e.touches[0].clientX - e.touches[1].clientX,
                         e.touches[0].clientY - e.touches[1].clientY);
    }
  }, { passive: true });
  stage.addEventListener('touchmove', e => {
    if (e.touches.length === 2 && pinch > 0) {
      e.preventDefault();
      const d = Math.hypot(e.touches[0].clientX - e.touches[1].clientX,
                           e.touches[0].clientY - e.touches[1].clientY);
      scale = Math.min(20, Math.max(1, scale * (d / pinch)));
      pinch = d;
      if (scale === 1) { tx = 0; ty = 0; }
      apply();
    }
  }, { passive: false });
  stage.addEventListener('touchend', () => { pinch = 0; }, { passive: true });
}

/* ---- 视频封面：用浏览器截帧（路由器解不了 H.264）---- */
const posterPosted = new Set();
function capturePoster(src, assetId) {
  return new Promise(resolve => {
    if (posterPosted.has(assetId)) return resolve(false);
    posterPosted.add(assetId);
    const isFile = typeof src !== 'string';
    const url = isFile ? URL.createObjectURL(src) : src;
    const v = document.createElement('video');
    v.muted = true; v.playsInline = true; v.preload = 'metadata';
    const finish = ok => {
      if (isFile) { try { URL.revokeObjectURL(url); } catch (e) {} }
      v.removeAttribute('src');
      resolve(ok);
    };
    const timer = setTimeout(() => finish(false), 12000);
    v.onloadedmetadata = () => {
      const t = Math.min(1, (isFinite(v.duration) ? v.duration : 2) * 0.15);
      try { v.currentTime = t; } catch (e) { finish(false); }
    };
    v.onseeked = () => {
      try {
        const vw = v.videoWidth || 1280, vh = v.videoHeight || 720;
        const w = Math.min(1280, vw), h = Math.round(w * vh / vw);
        const c = document.createElement('canvas');
        c.width = w; c.height = h;
        c.getContext('2d').drawImage(v, 0, 0, w, h);
        c.toBlob(async blob => {
          clearTimeout(timer);
          if (!blob) { finish(false); return; }
          try {
            const r = await fetch('/api/poster/' + assetId, { method: 'POST', body: blob });
            if (r.ok) {
              // 缩略图地址加时间戳，强制浏览器丢掉旧的占位灰块
              const cell = document.querySelector('.cell[data-id="' + assetId + '"]');
              if (cell) {
                const ph = cell.querySelector('.ph');
                if (ph) {
                  ph.src = '/t/' + assetId + '/t.jpg?f=' + Date.now();
                  cell.classList.remove('fail');
                  ph.style.display = '';
                }
              }
            }
            finish(r.ok);
          } catch (e) { finish(false); }
        }, 'image/jpeg', 0.85);
      } catch (e) { clearTimeout(timer); finish(false); }
    };
    v.onerror = () => { clearTimeout(timer); finish(false); };
    v.src = url;
  });
}
let uploadOwner = '';

function renderUploadQueue(){
  const box = $('#upQueue');
  if (!box) return;
  if (!UP.items.length){
    box.innerHTML = UP.doneFlash
      ? '<div class="up-empty">已全部上传完成</div>'
      : '<div class="up-empty">还没有选择文件</div>';
    return;
  }
  const n = Math.min(UP.items.length, 60);
  let html = '';
  for (let i = 0; i < n; i++){
    const it = UP.items[i];
    html += '<div class="up-row"><span class="nm">' + esc(it.name) + '</span><span class="sz">' + fmtB(it.size) + '</span></div>';
  }
  if (UP.items.length > n) html += '<div class="up-empty">还有 ' + (UP.items.length - n) + ' 个文件…</div>';
  box.innerHTML = html;
}

function updateMobileBar(){
  const pct = UP.totalBytes ? Math.min(100, UP.doneBytes / UP.totalBytes * 100) : 0;
  if ($('#upBar')) $('#upBar').style.width = pct + '%';
  if ($('#upCount')) $('#upCount').textContent = fmtB(UP.doneBytes) + ' / ' + fmtB(UP.totalBytes);
  if ($('#upSpeed')){
    let t = UP.speed > 0 ? fmtS(UP.speed) : '—';
    if (UP.speed > 0 && UP.totalBytes > UP.doneBytes){
      t += ' · 剩余 ' + Math.ceil((UP.totalBytes - UP.doneBytes) / UP.speed) + ' 秒';
    }
    $('#upSpeed').textContent = t;
  }
}

async function openMobileUpload(){
  window.__uploadSecret = window.__uploadSecret || false;
  $('#mUpload').classList.add('show');
  $('#upUrl').textContent = location.origin + '/#/upload';
  try {
    const cfg = await api('/api/config');
    const sel = $('#upSpace');
    sel.innerHTML = cfg.owners.map(o => '<option value="' + o.owner + '">' + esc(o.label) + '</option>').join('');
    if (!cfg.owners.some(o => o.owner === uploadOwner)) uploadOwner = cfg.owners.length ? cfg.owners[0].owner : 'lwm';
    sel.value = uploadOwner;
    sel.onchange = () => { uploadOwner = sel.value; };
  } catch (e) {}
  // 安卓/iOS 的浏览器都不支持选择文件夹（点了只会弹出普通图片选择器），直接隐藏；
  // 桌面浏览器才真正可用。
  const isMobile = /Android|iPhone|iPad|iPod|Mobile/i.test(navigator.userAgent);
  const probe = document.createElement('input');
  const canDir = !isMobile && ('webkitdirectory' in probe);
  $('#upDir').style.display = canDir ? '' : 'none';
  $('#upDirHint').style.display = canDir ? 'none' : 'block';
  renderUploadQueue();
  updateMobileBar();
  if ('wakeLock' in navigator) navigator.wakeLock.request('screen').catch(() => {});
}

$('#upClose').onclick = () => { $('#mUpload').classList.remove('show'); reset(); window.scrollTo(0, 0); };
$('#upPick').onclick = () => $('#fileInput').click();
$('#upDir').onclick = () => $('#dirInput').click();
$('#upCam').onclick = () => $('#camInput').click();
$('#dirInput').onchange = e => { const fs = Array.from(e.target.files || []); e.target.value = ''; addFiles(fs); };
$('#camInput').onchange = e => { const fs = Array.from(e.target.files || []); e.target.value = ''; addFiles(fs); };
$('#upCopy').onclick = async () => {
  try { await navigator.clipboard.writeText(location.origin + '/#/upload'); toast('链接已复制'); }
  catch (e) { toast(location.origin); }
};

/* ---- 启动 ---- */
let appStarted = false;
async function startApp(){
  if (appStarted) return;
  appStarted = true;
  await setUserChip();
  await initSpaces();
  await load();
  ensureSentinel();
  // 只在哨兵被整段替换掉（innerHTML 重建）时才补回去；
  // 回调本身绝不能再制造新的改动，否则就是死循环。
  const obs = new MutationObserver(() => {
    const stream = $('#stream');
    if (!sentinel || sentinel.parentNode !== stream) ensureSentinel();
  });
  obs.observe($('#stream'), { childList: true });

  // 恢复上次未完成的上传
  try {
    const pending = await idbAll();
    if (pending.length){
      UP.items = pending;
      UP.totalBytes = pending.reduce((a, i) => a + i.size, 0);
      UP.doneBytes = 0;
      $('#hud').classList.add('show');
      updateHud();
      toast('发现 ' + pending.length + ' 个未完成上传，正在续传');
      kick();
    }
  } catch (e) { console.warn(e); }

  if (location.hash === '#/upload') openMobileUpload();

  const m = location.hash.match(/#\/p\/(\d+)/);
  if (m) {
    const id = +m[1];
    if (state.items.some(x => x.id === id)) openViewer(id);
    else { const r = await api('/api/asset/' + id).catch(() => null); if (r) { state.items.push(r); openViewer(id); } }
  }
}

bootAuth();

// ---------- 注册 ----------
function openRegister(){
  $('#auth').classList.add('show');
  authMode = 'register';
  $('#authTitle').textContent = '注册新账号';
  $('#authSub').textContent = '填账号密码，并选择要加入的相册空间';
  $('#authUser').style.display = 'block';
  $('#authUser').value = '';
  $('#authPass').value = '';
  $('#authPass').placeholder = '密码（至少 4 位）';
  $('#authPass2').style.display = 'none';
  $('#authSpace').style.display = 'block';
  $('#authGo').textContent = '注册';
  $('#authToReg').textContent = '已有账号？去登录';
  $('#authErr').textContent = '';
}
function openLogin(){
  authMode = 'login';
  $('#authTitle').textContent = '拾光相册';
  $('#authSub').textContent = '输入账号和密码';
  $('#authUser').style.display = 'block';
  $('#authPass2').style.display = 'none';
  $('#authSpace').style.display = 'none';
  $('#authPass').placeholder = '密码';
  $('#authGo').textContent = '进入';
  $('#authToReg').textContent = '注册新账号';
  $('#authErr').textContent = '';
}
$('#authToReg').onclick = () => {
  if (authMode === 'register') openLogin(); else openRegister();
};

// ---------- Ctrl + 滚轮缩放（调整每行高度 = 每行照片数） ----------
(function () {
  let timer = null;
  document.addEventListener('wheel', (e) => {
    if (!e.ctrlKey) return;
    e.preventDefault();
    const root = document.documentElement;
    let h = ROWH();
    h *= (e.deltaY > 0 ? 0.90 : 1.10);      // 向下变小（更多照片），向上变大
    h = Math.max(90, Math.min(420, h));
    root.style.setProperty('--rowh', h.toFixed(0) + 'px');
    localStorage.setItem('lumina_rowh', h.toFixed(0));
    clearTimeout(timer);
    timer = setTimeout(() => {
      try { renderAll(); } catch (err) { console.warn('zoom rerender failed', err); }
      const tip = document.createElement('div');
      tip.textContent = h.toFixed(0) + 'px';
      tip.style.cssText = 'position:fixed;left:50%;bottom:32px;transform:translateX(-50%);' +
        'background:rgba(18,21,28,.92);color:#5EEAD4;padding:8px 18px;border-radius:20px;' +
        'font-size:13px;z-index:900;pointer-events:none;border:1px solid rgba(94,234,212,.3)';
      document.body.appendChild(tip);
      setTimeout(() => tip.remove(), 900);
    }, 160);
  }, { passive: false });

  // 恢复上次的缩放
  const saved = localStorage.getItem('lumina_rowh');
  if (saved) document.documentElement.style.setProperty('--rowh', saved + 'px');
})();

// ---------- 秘密相册（网页端） ----------
async function secretUnlock(){
  if (state.secret) return true;
  const pw = await uiPrompt('进入秘密相册', {
    message: '请输入当前账号的密码', placeholder: '密码', type: 'password'
  });
  if (!pw) return false;
  try {
    const r = await fetch('/api/login', { method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: localStorage.getItem('lumina_user') || '', password: pw }) });
    if (!r.ok) { toast('密码不对'); return false; }
    return true;
  } catch (e) { toast('验证失败：' + e.message); return false; }
}

$('#btnSecret').onclick = async () => {
  if (state.secret) {
    state.secret = false;
    $('#btnSecret').style.color = '';
    toast('已回到普通相册');
    reset();
    return;
  }
  if (!await secretUnlock()) return;
  state.secret = true;
  state.folder = '';
  state.album = 0;
  $('#btnSecret').style.color = 'var(--a1)';
  toast('秘密相册');
  reset();
};

$('#btnFamily').onclick = () => {
  if (state.family) {
    state.family = false;
    $('#btnFamily').style.color = '';
    $('#btnFamily').style.background = '';
    toast('已回到我的照片');
    reset();
    return;
  }
  // 进共享就退出秘密，两个分区不混
  state.secret = false;
  $('#btnSecret').style.color = '';
  state.family = true;
  state.folder = '';
  state.album = 0;
  $('#btnFamily').style.color = '#06231F';
  $('#btnFamily').style.background = 'var(--a1)';
  toast('家庭共享');
  reset();
};

$('#bSecret').onclick = async () => {
  const ids = [...selSet].map(Number);
  if (!ids.length) return;
  const to = !state.secret;
  try {
    const r = await api('/api/secret/move', { method: 'POST',
      body: JSON.stringify({ ids, secret: to }) });
    toast((to ? '已移入秘密相册 ' : '已移出秘密相册 ') + (r.moved || 0) + ' 项');
    setSelMode(false);
    reset();
  } catch (e) { toast('操作失败：' + e.message); }
};

/** 简单二选一：返回选中项的 key，取消返回 null。 */
function uiChoose(title, options){
  return new Promise(resolve => {
    const m = document.createElement('div');
    m.style.cssText = 'position:fixed;inset:0;z-index:950;background:rgba(0,0,0,.55);' +
      'display:grid;place-items:center;backdrop-filter:blur(6px)';
    const box = document.createElement('div');
    box.style.cssText = 'min-width:280px;padding:18px;border-radius:16px;background:#12151C;' +
      'border:1px solid rgba(255,255,255,.14);box-shadow:0 30px 80px -20px #000';
    const h = document.createElement('div');
    h.textContent = title;
    h.style.cssText = 'font-size:15px;color:#F2F3F5;margin-bottom:14px';
    box.appendChild(h);
    options.forEach(op => {
      const b = document.createElement('button');
      b.textContent = op.label;
      b.style.cssText = 'display:block;width:100%;margin-top:8px;padding:12px;border-radius:10px;' +
        'background:rgba(255,255,255,.05);border:1px solid rgba(255,255,255,.10);' +
        'color:#F2F3F5;font-size:13px;cursor:pointer;text-align:left';
      b.onmouseenter = () => b.style.background = 'rgba(94,234,212,.12)';
      b.onmouseleave = () => b.style.background = 'rgba(255,255,255,.05)';
      b.onclick = () => { m.remove(); resolve(op.key); };
      box.appendChild(b);
    });
    const c = document.createElement('button');
    c.textContent = '取消';
    c.style.cssText = 'display:block;width:100%;margin-top:14px;padding:10px;border-radius:10px;' +
      'background:transparent;border:none;color:#9BA1AC;font-size:13px;cursor:pointer';
    c.onclick = () => { m.remove(); resolve(null); };
    box.appendChild(c);
    m.appendChild(box);
    m.onclick = e => { if (e.target === m) { m.remove(); resolve(null); } };
    document.body.appendChild(m);
  });
}

let resizeTimer = null;
let lastWidth = window.innerWidth;
window.addEventListener('resize', () => {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(() => {
    if (Math.abs(window.innerWidth - lastWidth) > 30) {
      lastWidth = window.innerWidth;
      if (state.items && state.items.length > 0 && !state.loading) {
        renderAll();
      }
    }
  }, 250);
});
