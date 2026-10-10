// Touchpad gesture engine.
//
//  one finger move      → pointer (velocity-based acceleration, sub-pixel exact)
//  tap                  → left click        two-finger tap   → right click
//  three-finger tap     → middle click      double-tap + hold → drag
//  two-finger move      → scroll (+ momentum)   pinch (optional) → Ctrl + wheel
//
// Latency: deltas are sent from inside the touch handler, never deferred to
// an animation frame. A tiny 3 px dead zone at touch start keeps taps from
// nudging the cursor; the buffered distance is replayed once motion starts so
// no movement is lost.

import { settings } from './settings.js';

const TAP_MS = 240;          // max press duration for a tap
const TAP_SLOP = 10;         // max finger travel (px) for a tap
const START_DEADZONE = 3;    // px before pointer motion starts
const DRAG_WINDOW = 300;     // ms after a tap in which touching again starts a drag
const DRAG_RADIUS = 48;      // px between tap and the drag touch
const SCROLL_DECIDE = 6;     // px of two-finger travel before scrolling starts
const PINCH_DECIDE = 36;     // px of distance change before pinch starts

export class Touchpad {
  /**
   * @param {HTMLElement} el
   * @param {import('./conn.js').Connection} conn
   * @param {{click:(b:number)=>void, button:(b:number, down:boolean)=>void, onUse?:()=>void}} hooks
   */
  constructor(el, conn, hooks) {
    this.el = el;
    this.conn = conn;
    this.hooks = hooks;
    this.pts = new Map();
    this.g = null;
    this.lastTap = { t: -1e9, x: 0, y: 0 };
    this.rem = { x: 0, y: 0, sx: 0, sy: 0 };
    this.vel = 0;
    this.inertia = null;
    this.pendingClick = 0;
    this.suspendedClick = false;
    this.glow = new Glow(el.querySelector('canvas'));

    const opt = { passive: false };
    el.addEventListener('touchstart', (e) => this.start(e), opt);
    el.addEventListener('touchmove', (e) => this.move(e), opt);
    el.addEventListener('touchend', (e) => this.end(e), opt);
    el.addEventListener('touchcancel', (e) => this.end(e), opt);
    el.addEventListener('contextmenu', (e) => e.preventDefault());
    this.mouseSupport();
  }

  // ---- touch lifecycle -------------------------------------------------

  start(e) {
    e.preventDefault();
    this.stopInertia();
    const now = e.timeStamp;
    for (const t of e.changedTouches) {
      this.pts.set(t.identifier, { x: t.clientX, y: t.clientY, sx: t.clientX, sy: t.clientY, travel: 0 });
    }
    const n = this.pts.size;
    if (!this.g) {
      const p = this.pts.values().next().value;
      this.g = {
        start: now, lastT: now, max: n, mode: n === 1 ? 'pending' : 'two', x0: p.x, y0: p.y,
        buf: { x: 0, y: 0 }, maxTravel: 0, sub: null, samples: [],
        dragCandidate: n === 1 && settings.tap && now - this.lastTap.t < DRAG_WINDOW &&
          Math.hypot(p.x - this.lastTap.x, p.y - this.lastTap.y) < DRAG_RADIUS,
      };
      this.vel = 0;
      this.rem.x = this.rem.y = 0;
      this.hooks.onUse?.();
      if (this.pendingClick) {
        // "Clean drag" mode: the previous tap's click is still waiting.
        clearTimeout(this.pendingClick);
        this.pendingClick = 0;
        if (this.g.dragCandidate) this.suspendedClick = true;
        else this.hooks.click(1);
      }
    }
    const g = this.g;
    g.max = Math.max(g.max, n);
    if (n > 1) this.flushSuspended();
    if (n === 2 && (g.mode === 'pending' || g.mode === 'move')) g.mode = 'two';
    else if (n >= 3 && g.mode !== 'drag') {
      this.endSub();
      g.mode = 'three';
    }
    if (g.mode === 'two' && n === 2) this.initTwo();
    this.glow.update(this.pts);
  }

  move(e) {
    e.preventDefault();
    const g = this.g;
    if (!g) return;
    const now = e.timeStamp;
    const dt = now - g.lastT;
    g.lastT = now;
    let dx = 0, dy = 0, k = 0;
    for (const t of e.changedTouches) {
      const p = this.pts.get(t.identifier);
      if (!p) continue;
      const mx = t.clientX - p.x, my = t.clientY - p.y;
      p.x = t.clientX; p.y = t.clientY;
      p.travel = Math.max(p.travel, Math.hypot(p.x - p.sx, p.y - p.sy));
      dx += mx; dy += my; k++;
    }
    if (!k) return;
    this.glow.update(this.pts);

    switch (g.mode) {
      case 'pending':
        g.buf.x += dx; g.buf.y += dy;
        if (Math.hypot(g.buf.x, g.buf.y) >= START_DEADZONE) {
          if (g.dragCandidate) {
            g.mode = 'drag';
            this.suspendedClick = false; // the tap becomes the start of a drag
            this.hooks.button(1, true);
          } else {
            g.mode = 'move';
          }
          this.pointer(g.buf.x, g.buf.y, dt);
        }
        break;
      case 'move':
      case 'drag':
        if (this.pts.size === 1) this.pointer(dx, dy, dt);
        break;
      case 'two':
        this.two(now);
        break;
    }
  }

  end(e) {
    e.preventDefault();
    const g = this.g;
    for (const t of e.changedTouches) {
      const p = this.pts.get(t.identifier);
      if (!p) continue;
      if (g) g.maxTravel = Math.max(g.maxTravel, p.travel);
      this.pts.delete(t.identifier);
    }
    this.glow.update(this.pts);
    if (!g) return;
    const n = this.pts.size;
    if (n > 0) {
      // Lifting one finger of a multi-finger gesture must not turn the
      // remaining finger into a pointer jump.
      if (g.mode === 'two' || g.mode === 'three') {
        if (g.mode === 'two') this.endSub(e.timeStamp);
        g.mode = 'cooldown';
      }
      return;
    }
    this.finish(e.timeStamp);
  }

  finish(now) {
    const g = this.g;
    this.g = null;
    for (const p of this.pts.values()) g.maxTravel = Math.max(g.maxTravel, p.travel);
    this.pts.clear();
    if (g.mode === 'drag') {
      this.hooks.button(1, false);
      return;
    }
    if (g.mode === 'two') this.endSub(now);
    const dur = now - g.start;
    const tap = settings.tap && g.maxTravel < TAP_SLOP && !g.sub &&
      dur < (g.max > 1 ? TAP_MS + 80 : TAP_MS);
    if (!tap) {
      this.flushSuspended();
      return;
    }
    if (g.max === 1) {
      if (this.suspendedClick) {
        this.suspendedClick = false;
        this.hooks.click(1);
        this.hooks.click(1);
      } else if (settings.cleanDrag) {
        this.pendingClick = setTimeout(() => { this.pendingClick = 0; this.hooks.click(1); }, DRAG_WINDOW);
      } else {
        this.hooks.click(1);
      }
      this.lastTap = { t: now, x: g.x0, y: g.y0 };
    } else if (g.max === 2) {
      this.flushSuspended();
      this.hooks.click(2);
    } else {
      this.hooks.click(3);
    }
  }

  flushSuspended() {
    if (!this.suspendedClick) return;
    this.suspendedClick = false;
    this.hooks.click(1);
  }

  // ---- pointer motion ---------------------------------------------------

  pointer(dx, dy, dt) {
    const dist = Math.hypot(dx, dy);
    if (!dist) return;
    // Finger speed in px/ms, smoothed: single-event speeds are noisy.
    const v = dist / Math.max(dt, 4);
    this.vel = this.vel * 0.55 + v * 0.45;
    const boost = Math.min(Math.max((this.vel - 0.25) * 0.9, 0), 3);
    const gain = settings.speed * (1 + settings.accel * boost);
    const fx = dx * gain + this.rem.x;
    const fy = dy * gain + this.rem.y;
    const ix = Math.trunc(fx), iy = Math.trunc(fy);
    this.rem.x = fx - ix; this.rem.y = fy - iy;
    if (ix || iy) this.conn.move(ix, iy);
  }

  // ---- two-finger scroll / pinch ---------------------------------------

  twoPoints() {
    const it = this.pts.values();
    const a = it.next().value, b = it.next().value;
    return { cx: (a.x + b.x) / 2, cy: (a.y + b.y) / 2, d: Math.hypot(a.x - b.x, a.y - b.y) };
  }

  initTwo() {
    const g = this.g;
    const t = this.twoPoints();
    g.c = t; g.d0 = t.d; g.travel = 0; g.sub = g.sub || null;
    this.rem.sx = this.rem.sy = 0;
  }

  two(now) {
    const g = this.g;
    if (this.pts.size !== 2 || !g.c) return;
    const t = this.twoPoints();
    const dcx = t.cx - g.c.cx, dcy = t.cy - g.c.cy, dd = t.d - g.c.d;
    g.c = t;
    g.travel += Math.hypot(dcx, dcy);

    if (!g.sub) {
      const pinch = t.d - g.d0;
      if (settings.pinch && Math.abs(pinch) > PINCH_DECIDE && Math.abs(pinch) > g.travel * 1.2) {
        g.sub = 'pinch';
        this.conn.key('Control', 'down');
      } else if (g.travel > SCROLL_DECIDE) {
        g.sub = 'scroll';
      } else {
        return;
      }
    }
    if (g.sub === 'scroll') {
      const k = settings.scroll * (settings.natural ? -1 : 1);
      this.scrollBy(dcx * k, dcy * k, now);
    } else if (g.sub === 'pinch') {
      this.scrollBy(0, -dd * 3, now, false);
    }
  }

  scrollBy(fx, fy, now, track = true) {
    fx += this.rem.sx; fy += this.rem.sy;
    const ix = Math.trunc(fx), iy = Math.trunc(fy);
    this.rem.sx = fx - ix; this.rem.sy = fy - iy;
    if (ix || iy) this.conn.scroll(ix, iy);
    if (track && this.g) {
      const s = this.g.samples;
      s.push({ t: now, x: ix, y: iy });
      while (s.length && now - s[0].t > 100) s.shift();
    }
  }

  endSub(now) {
    const g = this.g;
    if (!g || !g.sub) return;
    if (g.sub === 'pinch') {
      this.conn.key('Control', 'up');
    } else if (g.sub === 'scroll' && settings.inertia && now !== undefined) {
      this.startInertia(g.samples, now);
    }
    g.samples = [];
    g.subDone = g.sub;
  }

  // ---- momentum ---------------------------------------------------------

  startInertia(samples, now) {
    if (samples.length < 2 || now - samples[samples.length - 1].t > 50) return;
    const span = Math.max(now - samples[0].t, 16);
    let vx = 0, vy = 0;
    for (const s of samples) { vx += s.x; vy += s.y; }
    vx /= span; vy /= span; // units per ms
    if (Math.hypot(vx, vy) < 0.4) return;
    let last = performance.now();
    const rem = { x: 0, y: 0 };
    const step = (ts) => {
      const dt = Math.min(ts - last, 50);
      last = ts;
      const f = Math.pow(0.9965, dt);
      vx *= f; vy *= f;
      if (Math.hypot(vx, vy) < 0.03) { this.inertia = null; return; }
      const fx = vx * dt + rem.x, fy = vy * dt + rem.y;
      const ix = Math.trunc(fx), iy = Math.trunc(fy);
      rem.x = fx - ix; rem.y = fy - iy;
      if (ix || iy) this.conn.scroll(ix, iy);
      this.inertia = requestAnimationFrame(step);
    };
    this.inertia = requestAnimationFrame(step);
  }

  stopInertia() {
    if (this.inertia) cancelAnimationFrame(this.inertia);
    this.inertia = null;
  }

  // ---- desktop mouse on the pad (laptop as a remote) ---------------------

  mouseSupport() {
    let down = null;
    this.el.addEventListener('pointerdown', (e) => {
      if (e.pointerType !== 'mouse') return;
      try { this.el.setPointerCapture(e.pointerId); } catch { /* synthetic pointer */ }
      down = { x: e.clientX, y: e.clientY, t: e.timeStamp, last: e.timeStamp, moved: 0, btn: e.button };
      this.vel = 0;
      this.hooks.onUse?.();
    });
    this.el.addEventListener('pointermove', (e) => {
      if (e.pointerType !== 'mouse' || !down) return;
      const dt = e.timeStamp - down.last;
      down.last = e.timeStamp;
      down.moved += Math.hypot(e.movementX, e.movementY);
      this.pointer(e.movementX, e.movementY, dt);
    });
    this.el.addEventListener('pointerup', (e) => {
      if (e.pointerType !== 'mouse' || !down) return;
      if (down.moved < 4) this.hooks.click(down.btn === 2 ? 2 : down.btn === 1 ? 3 : 1);
      down = null;
    });
    this.el.addEventListener('wheel', (e) => {
      e.preventDefault();
      const k = e.deltaMode === 1 ? 40 : 1;
      this.conn.scroll(Math.round(e.deltaX * k), Math.round(e.deltaY * k));
    }, { passive: false });
  }
}

/** Amber contact glow under each finger, drawn off the input path. */
class Glow {
  constructor(canvas) {
    this.c = canvas;
    this.ctx = canvas.getContext('2d');
    this.dots = new Map(); // id -> {x, y, a, live}
    this.raf = 0;
    this.color = getComputedStyle(document.documentElement).getPropertyValue('--glow').trim() || '242,181,68';
    const ro = new ResizeObserver(() => this.resize());
    ro.observe(canvas);
    matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => this.refreshColor());
  }

  refreshColor() {
    this.color = getComputedStyle(document.documentElement).getPropertyValue('--glow').trim();
  }

  resize() {
    const r = this.c.getBoundingClientRect();
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    this.c.width = Math.round(r.width * dpr);
    this.c.height = Math.round(r.height * dpr);
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    this.rect = r;
  }

  update(pts) {
    if (!this.rect) this.resize();
    const r = this.c.getBoundingClientRect();
    this.rect = r;
    for (const d of this.dots.values()) d.live = false;
    for (const [id, p] of pts) {
      const d = this.dots.get(id) || { a: 0 };
      d.x = p.x - r.left; d.y = p.y - r.top; d.live = true;
      this.dots.set(id, d);
    }
    if (!this.raf) this.raf = requestAnimationFrame(() => this.draw());
  }

  draw() {
    this.raf = 0;
    const { ctx } = this;
    ctx.clearRect(0, 0, this.c.width, this.c.height);
    let busy = false;
    for (const [id, d] of this.dots) {
      d.a += d.live ? (1 - d.a) * 0.5 : -0.12;
      if (d.a <= 0.01) { this.dots.delete(id); continue; }
      busy = true;
      const g = ctx.createRadialGradient(d.x, d.y, 0, d.x, d.y, 58);
      g.addColorStop(0, `rgba(${this.color}, ${0.42 * d.a})`);
      g.addColorStop(0.35, `rgba(${this.color}, ${0.16 * d.a})`);
      g.addColorStop(1, `rgba(${this.color}, 0)`);
      ctx.fillStyle = g;
      ctx.beginPath();
      ctx.arc(d.x, d.y, 58, 0, Math.PI * 2);
      ctx.fill();
      ctx.fillStyle = `rgba(${this.color}, ${0.9 * d.a})`;
      ctx.beginPath();
      ctx.arc(d.x, d.y, 5, 0, Math.PI * 2);
      ctx.fill();
    }
    if (busy) this.raf = requestAnimationFrame(() => this.draw());
  }
}
