// Pass 1: drive the real web app with synthetic multi-touch and record exactly
// what it sends to the server (pointer deltas, clicks, scroll units, text), plus
// phone screenshots of the states the demo shows. render.mjs turns this into frames.
//
//   /tmp/openremote --driver dryrun --mdns=false --port 8790 --name "Raspberry Pi" &
//   node capture.mjs [http://127.0.0.1:8790/]
import { chromium } from 'playwright-core';
import { mkdirSync, writeFileSync } from 'node:fs';
import { performance } from 'node:perf_hooks';
import { GESTURES, PHONE, TEXT } from './script.mjs';

const URL = process.argv[2] || 'http://127.0.0.1:8790/';
const OUT = new globalThis.URL('./out/', import.meta.url).pathname;
mkdirSync(OUT, { recursive: true });

const browser = await chromium.launch({
  executablePath: process.env.CHROME_PATH || undefined,
  args: ['--disable-frame-rate-limit', '--disable-gpu-vsync'], // touch events as fast as a 120 Hz phone
});
const ctx = await browser.newContext({
  viewport: { width: PHONE.w, height: PHONE.h }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, bypassCSP: true, // for the web font only
  colorScheme: 'dark',
});
await ctx.addInitScript(() => {
  window.__sent = [];
  const orig = WebSocket.prototype.send;
  WebSocket.prototype.send = function (d) {
    const ms = performance.now();
    if (typeof d === 'string') {
      const m = JSON.parse(d);
      if (m.t !== 'ping') window.__sent.push({ ms, ...m });
    } else {
      const v = new DataView(d);
      for (let i = 0; i + 5 <= v.byteLength; i += 5) {
        window.__sent.push({ ms, t: v.getUint8(i) === 1 ? 'move' : 'scroll', x: v.getInt16(i + 1, true), y: v.getInt16(i + 3, true) });
      }
    }
    return orig.call(this, d);
  };
});
const page = await ctx.newPage();
page.on('pageerror', (e) => { console.error('page error:', e); process.exitCode = 1; });
await page.goto(URL);
// The app's font stack includes Nunito; load it so the capture looks like a phone
// with a rounded system font rather than the Linux fallback.
await page.addStyleTag({ content: "@import url('https://fonts.googleapis.com/css2?family=Nunito:wght@400;600;700;800&display=block');" });
await page.waitForFunction(() => document.querySelector('#status').dataset.state === 'online');
await page.evaluate(() => document.fonts.ready);
await page.waitForTimeout(2500); // first ping → latency badge
const shot = (name) => page.screenshot({ path: OUT + name + '.png', animations: 'disabled' });
await shot('main');

const pad = await page.locator('#pad').boundingBox();
const cdp = await ctx.newCDPSession(page);
const touch = (type, pts) => cdp.send('Input.dispatchTouchEvent', {
  type, touchPoints: pts.map((p, i) => ({ x: pad.x + p[0] * pad.width, y: pad.y + p[1] * pad.height, id: i })),
});
const wait = (until) => new Promise((r) => { const tick = () => (performance.now() >= until ? r() : setImmediate(tick)); tick(); });

const gestures = [];
for (const g of GESTURES) {
  await page.evaluate(() => { window.__sent = []; });
  const pageT0 = await page.evaluate(() => performance.now());
  const t0 = performance.now();
  const touches = [];
  let down = false;
  // Sample the path at the real elapsed time, so the gesture keeps its speed
  // even if the browser takes longer than the 8 ms step to accept an event.
  for (let next = 0; next <= g.dur; next += 8) {
    await wait(t0 + next);
    const now = performance.now() - t0;
    next = Math.max(next, Math.floor(now / 8) * 8);
    const pts = g.at(Math.min(now, g.dur)); // null = no fingers
    if (pts && pts.length) {
      await touch(down ? 'touchMove' : 'touchStart', pts);
      down = true;
      touches.push({ t: now, pts });
    } else if (down) {
      await touch('touchEnd', []);
      down = false;
      touches.push({ t: now, pts: [] });
    }
  }
  if (down) await touch('touchEnd', []);
  await page.waitForTimeout(g.tail ?? 300); // let momentum / pending clicks finish
  const sent = (await page.evaluate(() => window.__sent)).map((m) => ({ ...m, ms: m.ms - pageT0 }));
  gestures.push({ id: g.id, touches, sent });
  const sum = sent.reduce((a, m) => {
    if (m.t === 'move') { a.move[0] += m.x; a.move[1] += m.y; } else if (m.t === 'scroll') { a.scroll[0] += m.x; a.scroll[1] += m.y; } else a.msgs.push(m);
    return a;
  }, { move: [0, 0], scroll: [0, 0], msgs: [] });
  console.log(g.id, JSON.stringify({ ...sum, msgs: sum.msgs.map(({ ms, ...m }) => m), records: sent.length }));
}

await shot('main-used'); // touch hint is gone after the first gesture

// Keyboard mode: every key is sent the moment it's typed. The phone keyboard
// covers the bottom of the screen, so the page gets the shorter viewport above it.
const kbBtnMain = await page.locator('#kb-btn').boundingBox();
await page.setViewportSize({ width: PHONE.w, height: PHONE.kbH });
await page.locator('#kb-btn').click();
await page.waitForTimeout(300);
await shot('kb');
const kbBtnKb = await page.locator('#kb-btn').boundingBox();
const keys = [];
for (const ch of TEXT + '\n') {
  await page.evaluate(() => { window.__sent = []; });
  const t0 = await page.evaluate(() => performance.now());
  if (ch === '\n') await page.keyboard.press('Enter'); else await page.keyboard.insertText(ch);
  await page.waitForTimeout(40);
  const sent = (await page.evaluate(() => window.__sent)).map((m) => ({ ...m, ms: m.ms - t0 }));
  keys.push({ ch, sent });
}
console.log('keys', keys.map((k) => `${JSON.stringify(k.ch)}→${k.sent.map((m) => `${m.t}:${JSON.stringify(m.s ?? m.k)}@${m.ms.toFixed(1)}ms`).join(',')}`).join(' '));
if (keys.some((k) => k.sent.length !== 1 || k.sent[0].t !== 'text' || k.sent[0].s !== k.ch)) throw new Error('unexpected keyboard messages');
await page.locator('#kb-btn').click();
await page.setViewportSize({ width: PHONE.w, height: PHONE.h });
await page.waitForTimeout(300);

writeFileSync(OUT + 'capture.json', JSON.stringify({ phone: PHONE, pad, kbBtnMain, kbBtnKb, keys, gestures }, null, 1));
await browser.close();
console.log('wrote', OUT);
