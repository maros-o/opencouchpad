import { Connection } from './conn.js';
import { Touchpad } from './touchpad.js';
import { KeyboardInput, Modifiers } from './keyboard.js';
import { settings, set as setSetting, reset as resetSettings, onChange } from './settings.js';

const $ = (s, root = document) => root.querySelector(s);
const $$ = (s, root = document) => [...root.querySelectorAll(s)];

const conn = new Connection();
let info = null;

// ---------------------------------------------------------------- layout

function fitViewport() {
  // iOS overlays the keyboard instead of resizing: follow the visual viewport.
  const vv = window.visualViewport;
  const h = vv ? vv.height : window.innerHeight;
  document.documentElement.style.setProperty('--app-h', `${Math.round(h)}px`);
  if (window.scrollY) window.scrollTo(0, 0);
}
window.visualViewport?.addEventListener('resize', fitViewport);
window.visualViewport?.addEventListener('scroll', fitViewport);
window.addEventListener('resize', fitViewport);
fitViewport();

function applyTheme() {
  const root = document.documentElement;
  if (settings.theme === 'auto') delete root.dataset.theme;
  else root.dataset.theme = settings.theme;
  const ink = getComputedStyle(root).getPropertyValue('--ink').trim();
  $('meta[name="theme-color"]').setAttribute('content', ink);
  pad?.glow.refreshColor();
}

// ---------------------------------------------------------------- feedback

function haptic() {
  if (settings.haptics && navigator.vibrate) navigator.vibrate(8);
}

let toastTimer = 0;
function toast(msg) {
  const el = $('#toast');
  el.textContent = msg;
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, 2600);
}

// ---------------------------------------------------------------- input

const modButtons = Object.fromEntries($$('.mod').map((b) => [b.dataset.mod, b]));
const mods = new Modifiers(conn, modButtons);
const kb = new KeyboardInput($('#kb-input'), conn, mods);

function click(b) {
  conn.button(b, 'click');
  haptic();
  mods.consume();
}

const pad = new Touchpad($('#pad'), conn, {
  click,
  button: (b, down) => {
    conn.button(b, down ? 'down' : 'up');
    if (down) haptic();
    else mods.consume();
  },
  onUse: () => {
    if (!$('#pad').classList.contains('used')) {
      $('#pad').classList.add('used');
      try { localStorage.setItem('opencouchpad.used', '1'); } catch { /* ignore */ }
    }
  },
});
try { if (localStorage.getItem('opencouchpad.used')) $('#pad').classList.add('used'); } catch { /* ignore */ }

// Physical-style mouse buttons: press and hold works (drag with another finger).
for (const el of $$('.mbtn')) {
  const real = () => {
    const b = Number(el.dataset.btn);
    if (settings.lefty && b !== 3) return b === 1 ? 2 : 1;
    return b;
  };
  let held = 0;
  el.addEventListener('pointerdown', (e) => {
    e.preventDefault();
    try { el.setPointerCapture(e.pointerId); } catch { /* synthetic pointer */ }
    held = real();
    el.classList.add('down');
    conn.button(held, 'down');
    haptic();
  });
  const up = () => {
    if (!held) return;
    conn.button(held, 'up');
    held = 0;
    el.classList.remove('down');
    mods.consume();
  };
  el.addEventListener('pointerup', up);
  el.addEventListener('pointercancel', up);
  el.addEventListener('lostpointercapture', up);
  el.addEventListener('contextmenu', (e) => e.preventDefault());
}

// Scroll strip on the right edge of the pad.
{
  const strip = $('#scroll-strip');
  let last = null, rem = 0;
  const opt = { passive: false };
  strip.addEventListener('touchstart', (e) => {
    e.preventDefault(); e.stopPropagation();
    last = e.changedTouches[0].clientY; rem = 0;
  }, opt);
  strip.addEventListener('touchmove', (e) => {
    e.preventDefault(); e.stopPropagation();
    const y = e.changedTouches[0].clientY;
    const f = (y - last) * settings.scroll * 1.5 * (settings.natural ? -1 : 1) + rem;
    last = y;
    const i = Math.trunc(f);
    rem = f - i;
    if (i) conn.scroll(0, i);
  }, opt);
  const end = (e) => { e.preventDefault(); e.stopPropagation(); };
  strip.addEventListener('touchend', end, opt);
  strip.addEventListener('touchcancel', end, opt);
}

// Keep the phone keyboard open while tapping toolbar / special keys.
for (const zone of [$('.toolbar'), $('#keys-row')]) {
  zone.addEventListener('mousedown', (e) => { if (kb.open) e.preventDefault(); });
}

$('#kb-btn').addEventListener('click', () => {
  if (kb.open) kb.hide();
  else kb.show();
});
$('#kb-input').addEventListener('focus', () => {
  $('#keys-row').hidden = false;
  $('#kb-btn').classList.add('on');
});
$('#kb-input').addEventListener('blur', () => {
  $('#keys-row').hidden = true;
  $('#kb-btn').classList.remove('on');
});

for (const b of $$('.mod')) b.addEventListener('click', () => { mods.toggle(b.dataset.mod); haptic(); });

/** Key buttons: tap sends once; holding auto-repeats (arrows, Backspace, volume). */
function bindKey(el) {
  const key = el.dataset.key;
  const repeats = el.hasAttribute('data-repeat') || el.closest('#keys-row');
  const send = () => { conn.key(key); haptic(); };
  if (!repeats) {
    el.addEventListener('click', () => { send(); mods.consume(); });
    return;
  }
  let timer = 0, fired = false;
  const stop = () => { clearTimeout(timer); clearInterval(timer); timer = 0; };
  el.addEventListener('pointerdown', () => {
    fired = false;
    stop();
    timer = setTimeout(() => {
      fired = true;
      send();
      timer = setInterval(send, key.startsWith('Audio') || key.startsWith('Bright') ? 140 : 55);
    }, 380);
  });
  el.addEventListener('pointerup', () => {
    stop();
    if (!fired) send();
    mods.consume();
  });
  el.addEventListener('pointercancel', stop);
  el.addEventListener('pointerleave', stop);
  el.addEventListener('contextmenu', (e) => e.preventDefault());
}
$$('[data-key]').forEach(bindKey);

function flash(el) {
  el.classList.add('on');
  setTimeout(() => el.classList.remove('on'), 160);
}

// On a Mac the shortcuts use Cmd. The original labels are kept to switch back
// when the phone moves to another computer.
let isMac = false;
for (const el of $$('[data-mac-kbd]')) {
  el.dataset.kbd = el.querySelector('kbd').textContent;
  el.dataset.name = el.querySelector('b').textContent;
}
function applyOS(os) {
  isMac = os === 'darwin';
  for (const el of $$('[data-mac-kbd]')) {
    el.querySelector('kbd').textContent = isMac ? el.dataset.macKbd : el.dataset.kbd;
    el.querySelector('b').textContent = (isMac && el.dataset.macName) || el.dataset.name;
  }
  $$('.brightness').forEach((el) => { el.hidden = os !== 'linux' && !isMac; });
}

for (const el of $$('[data-combo]')) {
  el.addEventListener('click', () => {
    const combo = (isMac && el.dataset.mac) || el.dataset.combo;
    const parts = combo.split('+').map((p) => (p === 'plus' ? '+' : p));
    const key = parts.pop();
    conn.key(key, 'tap', parts);
    haptic();
    flash(el);
    mods.consume();
  });
}

// Alt+Tab: Alt stays held while you keep tapping, released after a pause.
{
  const el = $('#alt-tab');
  let timer = 0;
  let mod = 'Alt';
  el.addEventListener('click', () => {
    if (!timer) {
      mod = isMac ? 'Meta' : 'Alt';
      conn.key(mod, 'down');
    }
    clearTimeout(timer);
    conn.key('Tab');
    haptic();
    el.classList.add('on');
    timer = setTimeout(() => {
      conn.key(mod, 'up');
      el.classList.remove('on');
      timer = 0;
    }, 1300);
  });
}

// ---------------------------------------------------------------- sheets

let openSheet = null;
function showSheet(name) {
  closeSheet();
  const el = $(`#sheet-${name}`);
  if (!el) return;
  if (name !== 'text') kb.hide();
  openSheet = el;
  el.hidden = false;
  $('#scrim').hidden = false;
  if (name === 'devices') loadDevices();
  if (name === 'settings') renderShare();
  if (name === 'text') setTimeout(() => $('#send-text').focus(), 50);
}
function closeSheet() {
  if (!openSheet) return;
  openSheet.hidden = true;
  openSheet = null;
  $('#scrim').hidden = true;
}
$$('[data-sheet]').forEach((b) => b.addEventListener('click', () => showSheet(b.dataset.sheet)));
$('#device-btn').addEventListener('click', () => showSheet('devices'));
$$('[data-close]').forEach((b) => b.addEventListener('click', closeSheet));
$('#scrim').addEventListener('click', closeSheet);
document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && openSheet) closeSheet(); });

$('#send-text-btn').addEventListener('click', () => {
  const ta = $('#send-text');
  let text = ta.value;
  if (!text) { ta.focus(); return; }
  if ($('#send-enter').checked) text += '\n';
  conn.text(text);
  toast(`Typed ${Array.from(ta.value).length} characters`);
  ta.value = '';
  closeSheet();
});

// ---------------------------------------------------------------- devices

function osName(os) {
  return { linux: 'Linux', windows: 'Windows', darwin: 'macOS' }[os] || os || '';
}

async function loadDevices() {
  const list = $('#devices');
  const here = document.createElement('li');
  const portSuffix = (p) => (p && p !== 80 ? `:${p}` : '');
  const item = (name, sub, href, current) =>
    `<a href="${href}" class="${current ? 'current' : ''}"><span class="os"><svg><use href="#i-devices"/></svg></span>` +
    `<span class="meta"><b>${esc(name)}</b><span>${esc(sub)}</span></span>${current ? '<span class="tag">Connected</span>' : ''}</a>`;
  here.innerHTML = item(info?.name || location.hostname, `${osName(info?.os)}, ${location.host}`, '#', true);
  here.querySelector('a').addEventListener('click', (e) => { e.preventDefault(); closeSheet(); });
  list.replaceChildren(here);
  try {
    const peers = await (await fetch('/api/peers', { cache: 'no-store' })).json();
    for (const p of peers) {
      const li = document.createElement('li');
      const host = `${p.ip}${portSuffix(p.port)}`;
      li.innerHTML = item(p.name, `${osName(p.txt?.os)}, ${p.host}`, `http://${host}/`, false);
      list.append(li);
    }
    $('#devices-note').textContent = peers.length
      ? 'Tap a computer to control it instead.'
      : 'Other computers running OpenCouchpad on this network appear here automatically.';
  } catch { /* offline */ }
}

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

// ---------------------------------------------------------------- settings UI

const rangeFmt = { speed: (v) => `${v.toFixed(1)}×`, accel: (v) => (v ? v.toFixed(1) : 'Off'), scroll: (v) => `${v.toFixed(1)}×` };

function bindSettings() {
  for (const key of ['speed', 'accel', 'scroll']) {
    const input = $(`#set-${key}`);
    const out = $(`output[data-for="${key}"]`);
    const paint = () => {
      const v = Number(input.value);
      out.textContent = rangeFmt[key](v);
      input.style.setProperty('--p', `${((v - input.min) / (input.max - input.min)) * 100}%`);
    };
    input.value = settings[key];
    paint();
    input.addEventListener('input', () => { setSetting(key, Number(input.value)); paint(); });
  }
  for (const key of ['tap', 'cleanDrag', 'lefty', 'natural', 'inertia', 'strip', 'pinch', 'haptics']) {
    const input = $(`#set-${key}`);
    input.checked = settings[key];
    input.addEventListener('change', () => setSetting(key, input.checked));
  }
  const theme = $('#set-theme');
  theme.value = settings.theme;
  theme.addEventListener('change', () => setSetting('theme', theme.value));
  const layout = $('#set-layout');
  layout.addEventListener('change', () => {
    setSetting('layout', layout.value);
    conn.json({ t: 'layout', v: layout.value });
  });
  if (document.fullscreenEnabled) {
    const fs = $('#fullscreen-btn');
    fs.hidden = false;
    fs.addEventListener('click', () => {
      if (document.fullscreenElement) document.exitFullscreen();
      else document.documentElement.requestFullscreen().catch(() => {});
    });
  }
  $('#reset-btn').addEventListener('click', () => {
    resetSettings();
    for (const key of ['speed', 'accel', 'scroll']) {
      $(`#set-${key}`).value = settings[key];
      $(`#set-${key}`).dispatchEvent(new Event('input'));
    }
    for (const key of ['tap', 'cleanDrag', 'lefty', 'natural', 'inertia', 'strip', 'pinch', 'haptics']) $(`#set-${key}`).checked = settings[key];
    theme.value = settings.theme;
    layout.value = settings.layout;
    conn.json({ t: 'layout', v: 'auto' });
    toast('Settings reset');
  });
}

function renderLayouts(hello) {
  const sel = $('#set-layout');
  const detected = hello.layouts.find((l) => l.id === hello.layout);
  const opts = [`<option value="auto">Same as computer (${esc(detected ? detected.name : hello.layout)})</option>`]
    .concat(hello.layouts.map((l) => `<option value="${esc(l.id)}">${esc(l.name)}</option>`));
  sel.innerHTML = opts.join('');
  sel.value = settings.layout;
  if (sel.value !== settings.layout) sel.value = 'auto';
  // Typing on Windows is layout independent, so hide the option there.
  sel.closest('label').hidden = hello.os !== 'linux';
}

function renderShare() {
  if (!info) return;
  const urls = info.urls || [];
  $('#urls').innerHTML = urls.map((u) => `<li>${esc(u.replace(/^http:\/\//, ''))}</li>`).join('');
  const target = urls.find((u) => !u.includes('.local')) || urls[0] || location.origin;
  $('#qr').src = `/api/qr.png?u=${encodeURIComponent(target)}`;
  $('#about').textContent = `OpenCouchpad ${info.version} on ${osName(info.os)}, ${info.driver} input driver.`;
}

onChange((key) => {
  if (key === 'theme') applyTheme();
  if (key === 'strip') $('#scroll-strip').hidden = !settings.strip;
});

// ---------------------------------------------------------------- connection UI

conn.addEventListener('state', (e) => {
  const s = e.detail;
  $('#status').dataset.state = s;
  $('#offline').hidden = s === 'online';
  $('#offline-text').textContent = s === 'offline' ? 'Connection lost. Reconnecting…' : 'Connecting…';
  if (s !== 'online') $('#latency').textContent = '';
});

conn.addEventListener('hello', (e) => {
  const h = e.detail;
  info = { ...info, ...h };
  $('#device-name').textContent = h.name;
  document.title = `${h.name} – OpenCouchpad`;
  $('#meta-mod').textContent = h.os === 'windows' ? 'Win' : h.os === 'darwin' ? 'Cmd' : 'Super';
  applyOS(h.os);
  renderLayouts(h);
  if (settings.layout !== 'auto') conn.json({ t: 'layout', v: settings.layout });
});

conn.addEventListener('rtt', (e) => {
  $('#latency').textContent = `${e.detail} ms`;
});

let lastErr = 0;
conn.addEventListener('servererror', (e) => {
  if (performance.now() - lastErr < 4000) return;
  lastErr = performance.now();
  toast(e.detail);
});

conn.addEventListener('maybeauth', async () => {
  const i = await fetchInfo();
  if (i && i.auth && !i.authed) showPin();
});

document.addEventListener('visibilitychange', () => {
  if (!document.hidden && $('#pin').hidden) conn.connect();
});
window.addEventListener('pageshow', () => { if (info && $('#pin').hidden) conn.connect(); });

// ---------------------------------------------------------------- PIN

async function fetchInfo() {
  try {
    const r = await fetch('/api/info', { cache: 'no-store' });
    return await r.json();
  } catch {
    return null;
  }
}

function showPin() {
  $('#pin').hidden = false;
  $('#pin-for').textContent = `${info?.name || 'This computer'} asks for a PIN before it can be controlled.`;
  setTimeout(() => $('#pin-input').focus(), 50);
}

$('#pin-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const pin = $('#pin-input').value.trim();
  if (!pin) return;
  const r = await fetch('/api/login', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ pin }),
  }).catch(() => null);
  const body = r ? await r.json().catch(() => ({})) : { error: 'Could not reach the computer.' };
  if (r && r.ok) {
    $('#pin').hidden = true;
    $('#pin-input').value = '';
    $('#pin-input').blur();
    conn.connect();
  } else {
    $('#pin-error').textContent = body.error || 'Wrong PIN';
    $('#pin-input').select();
  }
});

// ---------------------------------------------------------------- boot

async function boot() {
  applyTheme();
  bindSettings();
  $('#scroll-strip').hidden = !settings.strip;
  info = await fetchInfo();
  if (info) $('#device-name').textContent = info.name;
  if (info && info.auth && !info.authed) {
    showPin();
    return;
  }
  conn.connect();
}
boot();
