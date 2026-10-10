// WebSocket connection with low-latency binary motion and auto-reconnect.
//
// Motion is sent immediately on every touchmove. Only when the socket's send
// buffer backs up (weak Wi-Fi) do we coalesce deltas, so the cursor never
// "replays" a queue of stale moves after a hiccup.

const MOVE = 1, SCROLL = 2;
const CONGESTED = 512; // bytes waiting in the socket buffer

export class Connection extends EventTarget {
  constructor() {
    super();
    this.ws = null;
    this.state = 'connecting';
    this.retry = 0;
    this.pend = { mx: 0, my: 0, sx: 0, sy: 0 };
    this.flushTimer = 0;
    this.pingTimer = 0;
    this.lastPong = 0;
    this.rtt = null;
    this.hello = null;
  }

  url() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${proto}//${location.host}/ws`;
  }

  connect() {
    clearTimeout(this.reconnectTimer);
    if (this.ws && this.ws.readyState <= 1) return;
    this.setState('connecting');
    const ws = new WebSocket(this.url());
    ws.binaryType = 'arraybuffer';
    this.ws = ws;
    ws.onopen = () => {
      this.retry = 0;
      this.lastPong = performance.now();
      this.startPing();
    };
    ws.onmessage = (e) => {
      if (typeof e.data !== 'string') return;
      let m;
      try { m = JSON.parse(e.data); } catch { return; }
      if (m.t === 'pong') {
        this.lastPong = performance.now();
        this.rtt = Math.round(this.lastPong - m.ts);
        this.dispatchEvent(new CustomEvent('rtt', { detail: this.rtt }));
      } else if (m.t === 'hello') {
        this.hello = m;
        this.setState('online');
        this.dispatchEvent(new CustomEvent('hello', { detail: m }));
      } else if (m.t === 'error') {
        this.dispatchEvent(new CustomEvent('servererror', { detail: m.msg }));
      }
    };
    ws.onclose = (e) => {
      if (this.ws !== ws) return;
      this.stopPing();
      this.ws = null;
      this.setState('offline');
      if (e.code === 1006 && this.retry > 2) this.dispatchEvent(new Event('maybeauth'));
      this.scheduleReconnect();
    };
    ws.onerror = () => ws.close();
  }

  scheduleReconnect() {
    if (document.hidden) return; // reconnect when the page becomes visible
    const delay = Math.min(250 * 2 ** this.retry, 3000);
    this.retry++;
    this.reconnectTimer = setTimeout(() => this.connect(), delay);
  }

  /** Close dead sockets fast: phones often keep a zombie TCP after sleep. */
  startPing() {
    this.stopPing();
    const tick = () => {
      if (!this.open) return;
      if (performance.now() - this.lastPong > 6000) {
        this.ws.close();
        return;
      }
      this.json({ t: 'ping', ts: performance.now() });
    };
    tick();
    this.pingTimer = setInterval(tick, 2000);
  }

  stopPing() { clearInterval(this.pingTimer); }

  setState(s) {
    if (this.state === s) return;
    this.state = s;
    this.dispatchEvent(new CustomEvent('state', { detail: s }));
  }

  get open() { return this.ws && this.ws.readyState === 1; }

  json(obj) {
    if (this.open) this.ws.send(JSON.stringify(obj));
  }

  move(dx, dy) {
    this.pend.mx += dx; this.pend.my += dy;
    this.flush();
  }

  scroll(dx, dy) {
    this.pend.sx += dx; this.pend.sy += dy;
    this.flush();
  }

  flush() {
    if (!this.open) { this.pend = { mx: 0, my: 0, sx: 0, sy: 0 }; return; }
    if (this.ws.bufferedAmount > CONGESTED) {
      if (!this.flushTimer) this.flushTimer = setTimeout(() => { this.flushTimer = 0; this.flush(); }, 8);
      return;
    }
    const p = this.pend;
    const recs = [];
    // Split into int16-sized chunks (only matters for huge deltas).
    const push = (type, x, y) => {
      while (x || y) {
        const cx = Math.max(-32768, Math.min(32767, x));
        const cy = Math.max(-32768, Math.min(32767, y));
        recs.push([type, cx, cy]);
        x -= cx; y -= cy;
      }
    };
    push(MOVE, p.mx, p.my);
    push(SCROLL, p.sx, p.sy);
    this.pend = { mx: 0, my: 0, sx: 0, sy: 0 };
    if (!recs.length) return;
    const buf = new DataView(new ArrayBuffer(recs.length * 5));
    recs.forEach(([t, x, y], i) => {
      buf.setUint8(i * 5, t);
      buf.setInt16(i * 5 + 1, x, true);
      buf.setInt16(i * 5 + 3, y, true);
    });
    this.ws.send(buf.buffer);
  }

  button(b, action) { this.json({ t: 'btn', b, a: action }); }

  key(k, action = 'tap', mods = []) {
    const m = { t: 'key', k, a: action };
    if (mods.length) m.m = mods;
    this.json(m);
  }

  text(s) { if (s) this.json({ t: 'text', s }); }
}
