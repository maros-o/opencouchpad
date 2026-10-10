// Pass 2: render stage.html frame by frame and encode the demo.
//   node render.mjs            → out/demo.mp4 (preview)
//   node render.mjs --publish  → also docs/demo.gif (for the README)
import { chromium } from 'playwright-core';
import { createServer } from 'node:http';
import { readFile, mkdir, rm } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { extname, join, normalize } from 'node:path';

const ROOT = new URL('../../', import.meta.url).pathname; // repo root
const OUT = join(ROOT, 'tools/demo/out');
const FPS = 30;
const types = { '.html': 'text/html', '.json': 'application/json', '.png': 'image/png', '.svg': 'image/svg+xml', '.js': 'text/javascript' };
const server = createServer(async (req, res) => {
  const path = normalize(join(ROOT, decodeURIComponent(new URL(req.url, 'http://x').pathname)));
  if (!path.startsWith(ROOT)) return res.writeHead(403).end();
  let body;
  try { body = await readFile(path); } catch { return res.writeHead(404).end(); }
  res.writeHead(200, { 'content-type': types[extname(path)] || 'application/octet-stream' }).end(body);
}).listen(0, '127.0.0.1');
await new Promise((r) => server.once('listening', r));

const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || undefined });
const page = await browser.newPage({ viewport: { width: 1600, height: 900 }, deviceScaleFactor: 1.2 });
page.on('pageerror', (e) => { console.error('stage error:', e); process.exit(1); });
await page.goto(`http://127.0.0.1:${server.address().port}/tools/demo/stage.html`);
await page.waitForFunction(() => window.ready === true, null, { timeout: 30000 });
const checks = await page.evaluate(() => window.checks());
console.log('checks', JSON.stringify(checks));
if (!process.argv.includes('--force') && (!checks.dock || !checks.search || !checks.play)) throw new Error('pointer misses a click target, adjust script.mjs or the stage layout');

const only = process.argv.find((a) => a.startsWith('--at='));
if (only) { // single still for quick review: --at=ms[,ms…]
  for (const ms of only.slice(5).split(',')) {
    await page.evaluate((t) => window.render(t), Number(ms));
    await page.screenshot({ path: join(OUT, `still-${ms}.png`) });
  }
  await browser.close(); server.close(); process.exit(0);
}

const frames = join(OUT, 'frames');
await rm(frames, { recursive: true, force: true });
await mkdir(frames, { recursive: true });
const total = Math.round((await page.evaluate(() => window.duration)) / 1000 * FPS);
for (let f = 0; f < total; f++) {
  await page.evaluate((t) => window.render(t), (f * 1000) / FPS);
  await page.screenshot({ path: join(frames, `${String(f).padStart(5, '0')}.png`) });
  if (f % 60 === 0) process.stdout.write(`\rframe ${f}/${total}`);
}
console.log(`\rrendered ${total} frames`);
await browser.close();
server.close();

const ff = (...a) => execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', ...a], { stdio: 'inherit' });
const mp4 = join(OUT, 'demo.mp4');
ff('-framerate', String(FPS), '-i', join(frames, '%05d.png'), '-c:v', 'libx264', '-preset', 'slow', '-crf', '18', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', mp4);
console.log('wrote', mp4);
if (process.argv.includes('--publish')) {
  const docs = join(ROOT, 'docs');
  ff('-framerate', String(FPS), '-i', join(frames, '%05d.png'), '-vf',
    'fps=25,scale=1000:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=192:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=4:diff_mode=rectangle',
    '-loop', '0', join(docs, 'demo.gif'));
  console.log('wrote docs/demo.gif');
}
