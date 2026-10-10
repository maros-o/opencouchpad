import { chromium } from 'playwright-core';


const URL = process.argv[2] || 'http://localhost:8765/';
const out = process.argv[3] || '.';
const browser = await chromium.launch({
  executablePath: process.env.CHROME_PATH || undefined,
});
const ctx = await browser.newContext({
  viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true,
  colorScheme: process.env.SCHEME || 'dark',
});
await ctx.addInitScript(() => {
  window.__sent = [];
  const orig = WebSocket.prototype.send;
  WebSocket.prototype.send = function (d) {
    if (typeof d === 'string') {
      const m = JSON.parse(d);
      if (m.t !== 'ping') window.__sent.push(m);
    } else {
      const v = new DataView(d);
      for (let i = 0; i + 5 <= v.byteLength; i += 5) {
        window.__sent.push({ t: v.getUint8(i) === 1 ? 'move' : 'scroll', x: v.getInt16(i + 1, true), y: v.getInt16(i + 3, true) });
      }
    }
    return orig.call(this, d);
  };
});
const page = await ctx.newPage();
const errors = [];
page.on('pageerror', (e) => errors.push(String(e)));
page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
await page.goto(URL);
await page.waitForFunction(() => document.querySelector('#status').dataset.state === 'online', null, { timeout: 5000 });
await page.waitForTimeout(300);
await page.screenshot({ path: `${out}/01-main.png` });

const cdp = await ctx.newCDPSession(page);
const touch = (type, pts) => cdp.send('Input.dispatchTouchEvent', { type, touchPoints: pts.map((p, i) => ({ x: p[0], y: p[1], id: p[2] ?? i })) });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const take = () => page.evaluate(() => { const s = window.__sent; window.__sent = []; return s; });
const summarize = (s) => {
  const r = {};
  let mx = 0, my = 0, sx = 0, sy = 0;
  for (const m of s) {
    if (m.t === 'move') { mx += m.x; my += m.y; }
    else if (m.t === 'scroll') { sx += m.x; sy += m.y; }
    else { const k = `${m.t}:${m.k || m.b}:${m.a || ''}${m.m ? '+' + m.m.join('+') : ''}`; r[k] = (r[k] || 0) + 1; }
  }
  return { move: [mx, my], scroll: [sx, sy], ...r };
};

const results = {};
// 1. single tap
await take();
await touch('touchStart', [[200, 300]]); await sleep(60); await touch('touchEnd', []);
await sleep(400);
results.tap = summarize(await take());

// 2. slow move right 100px in 20 steps
await touch('touchStart', [[100, 300]]);
for (let i = 1; i <= 20; i++) { await sleep(16); await touch('touchMove', [[100 + i * 5, 300]]); }
await touch('touchEnd', []);
await sleep(400);
results.slowMove = summarize(await take());

// 3. fast move right 100px in 4 steps
await touch('touchStart', [[100, 300]]);
for (let i = 1; i <= 4; i++) { await sleep(8); await touch('touchMove', [[100 + i * 25, 300]]); }
await touch('touchEnd', []);
await sleep(400);
results.fastMove = summarize(await take());

// 4. tiny jitter tap (2px) → click, no move
await touch('touchStart', [[200, 300]]); await sleep(30); await touch('touchMove', [[202, 300]]); await sleep(30); await touch('touchEnd', []);
await sleep(400);
results.jitterTap = summarize(await take());

// 5. two-finger tap
await touch('touchStart', [[150, 300, 0], [220, 300, 1]]); await sleep(80); await touch('touchEnd', []);
await sleep(400);
results.twoFingerTap = summarize(await take());

// 6. three-finger tap
await touch('touchStart', [[150, 300, 0], [200, 300, 1], [250, 300, 2]]); await sleep(80); await touch('touchEnd', []);
await sleep(400);
results.threeFingerTap = summarize(await take());

// 7. two-finger scroll up (fingers move up 120px) then lift slowly (no inertia)
await touch('touchStart', [[150, 400, 0], [220, 400, 1]]);
for (let i = 1; i <= 12; i++) { await sleep(16); await touch('touchMove', [[150, 400 - i * 10, 0], [220, 400 - i * 10, 1]]); }
await sleep(150);
await touch('touchEnd', []);
await sleep(600);
results.scrollNoFling = summarize(await take());

// 8. two-finger flick (inertia expected)
await touch('touchStart', [[150, 400, 0], [220, 400, 1]]);
for (let i = 1; i <= 6; i++) { await sleep(10); await touch('touchMove', [[150, 400 - i * 20, 0], [220, 400 - i * 20, 1]]); }
await touch('touchEnd', []);
await sleep(1500);
results.scrollFling = summarize(await take());

// 9. tap then touch-and-drag
await touch('touchStart', [[200, 300]]); await sleep(50); await touch('touchEnd', []);
await sleep(120);
await touch('touchStart', [[202, 302]]);
for (let i = 1; i <= 10; i++) { await sleep(16); await touch('touchMove', [[202 + i * 5, 302]]); }
await touch('touchEnd', []);
await sleep(400);
results.tapDrag = summarize(await take());

// 10. double tap → two clicks
await touch('touchStart', [[200, 300]]); await sleep(50); await touch('touchEnd', []);
await sleep(100);
await touch('touchStart', [[201, 300]]); await sleep(50); await touch('touchEnd', []);
await sleep(400);
results.doubleTap = summarize(await take());

// 11. two finger scroll, lift one finger, continue moving remaining → no pointer move
await touch('touchStart', [[150, 400, 0], [220, 400, 1]]);
for (let i = 1; i <= 5; i++) { await sleep(16); await touch('touchMove', [[150, 400 - i * 10, 0], [220, 400 - i * 10, 1]]); }
await sleep(100);
await touch('touchEnd', [[150, 350, 0]]);
for (let i = 1; i <= 5; i++) { await sleep(16); await touch('touchMove', [[150 + i * 10, 350, 0]]); }
await touch('touchEnd', []);
await sleep(400);
results.liftOneFinger = summarize(await take());

// 12. Modifiers: Ctrl once + tap → key down, click, key up
await page.tap('[data-mod="Control"]');
await touch('touchStart', [[200, 300]]); await sleep(50); await touch('touchEnd', []);
await sleep(400);
results.ctrlClick = summarize(await take());

// 13. physical left button hold
await page.dispatchEvent('[data-btn="1"]', 'pointerdown', { pointerId: 5, pointerType: 'touch' });
await sleep(50);
await page.dispatchEvent('[data-btn="1"]', 'pointerup', { pointerId: 5, pointerType: 'touch' });
await sleep(200);
results.leftButton = summarize(await take());

// 14. keyboard: simulate textarea diff
await page.evaluate(() => {
  const ta = document.querySelector('#kb-input');
  ta.focus();
  const fire = (v) => { ta.value = v; ta.dispatchEvent(new Event('input')); };
  const base = ta.value;
  fire(base + 'h'); fire(base + 'he'); fire(base + 'hel'); fire(base + 'helo');
  fire(base + 'hel'); // backspace
  fire(base + 'hello world');
  fire(base + 'hello world\n');
});
await sleep(300);
results.keyboard = await take();
await page.screenshot({ path: `${out}/02-keyboard.png` });
await page.evaluate(() => document.querySelector('#kb-input').blur());

// 15. sheets screenshots
for (const s of ['media', 'shortcuts', 'settings', 'devices', 'text']) {
  await page.evaluate((s) => { const b = document.querySelector(`[data-sheet="${s}"]`); if (b) b.click(); else document.querySelector('#device-btn').click(); }, s);
  await sleep(400);
  await page.screenshot({ path: `${out}/03-sheet-${s}.png` });
  await page.evaluate(() => document.querySelector('#scrim').click());
  await sleep(100);
}
await page.click('[data-sheet="shortcuts"]');
await sleep(300);
await page.click('[data-combo="Control+c"]');
await page.click('[data-combo="Control+plus"]');
await page.click('#alt-tab'); await sleep(100); await page.click('#alt-tab');
await sleep(1600);
results.shortcuts = summarize(await take());

// clean drag mode
await page.evaluate(() => document.querySelector('#scrim').click()); await sleep(200);
await page.evaluate(() => { const c = document.querySelector('#set-cleanDrag'); c.checked = true; c.dispatchEvent(new Event('change')); });
await take();
await touch('touchStart', [[200, 300]]); await sleep(50); await touch('touchEnd', []);
await sleep(100);
results.cleanTapEarly = summarize(await take());
await sleep(400);
results.cleanTapLate = summarize(await take());
await touch('touchStart', [[200, 300]]); await sleep(50); await touch('touchEnd', []);
await sleep(120);
await touch('touchStart', [[202, 302]]);
for (let i = 1; i <= 10; i++) { await sleep(16); await touch('touchMove', [[202 + i * 5, 302]]); }
await touch('touchEnd', []);
await sleep(500);
results.cleanTapDrag = summarize(await take());
await touch('touchStart', [[200, 300]]); await sleep(50); await touch('touchEnd', []);
await sleep(100);
await touch('touchStart', [[201, 300]]); await sleep(50); await touch('touchEnd', []);
await sleep(500);
results.cleanDoubleTap = summarize(await take());
await page.dispatchEvent('[data-btn="1"]', 'pointerdown', { pointerId: 5, pointerType: 'touch' });
await page.dispatchEvent('[data-btn="1"]', 'pointerup', { pointerId: 5, pointerType: 'touch' });
await sleep(200);
results.leftButton2 = summarize(await take());

// landscape
await page.setViewportSize({ width: 844, height: 390 });
await page.evaluate(() => document.querySelector('#scrim').click());
await sleep(300);
await page.screenshot({ path: `${out}/04-landscape.png` });

console.log(JSON.stringify(results, null, 1));
console.log('ERRORS', errors);
await browser.close();
