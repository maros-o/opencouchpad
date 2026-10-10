// Phone keyboard → computer keyboard.
//
// Mobile keyboards (Gboard, iOS, Samsung, SwiftKey) rarely send usable key
// events; they edit text, often a whole word at a time (autocorrect, swipe,
// predictions). So we watch a hidden textarea and diff its contents: removed
// characters become Backspace presses, added text is typed. A few invisible
// sentinel characters stay in the field so Backspace works even when "empty".

const SENTINEL = '​​​​';
const MOD_KEYS = ['Control', 'Alt', 'Shift', 'Meta'];

/** Sticky modifier keys: tap = next action only, tap again = locked, tap again = off. */
export class Modifiers {
  constructor(conn, buttons) {
    this.conn = conn;
    this.state = Object.fromEntries(MOD_KEYS.map((m) => [m, 'off']));
    this.buttons = buttons; // mod name -> element
    conn.addEventListener('hello', () => this.resync());
  }

  toggle(mod) {
    const s = this.state[mod];
    const next = s === 'off' ? 'once' : s === 'once' ? 'lock' : 'off';
    this.set(mod, next);
  }

  set(mod, next) {
    const prev = this.state[mod];
    this.state[mod] = next;
    // The key is really held on the computer while active, so Shift+click,
    // Ctrl+click, Ctrl+scroll and Alt+drag all work naturally.
    if (prev === 'off' && next !== 'off') this.conn.key(mod, 'down');
    if (prev !== 'off' && next === 'off') this.conn.key(mod, 'up');
    const b = this.buttons[mod];
    if (b) b.dataset.state = next;
  }

  /** Any modifier currently held? */
  get active() { return MOD_KEYS.some((m) => this.state[m] !== 'off'); }

  /** Release one-shot modifiers after an action. */
  consume() {
    for (const m of MOD_KEYS) if (this.state[m] === 'once') this.set(m, 'off');
  }

  /** After a reconnect the server released everything; press locked keys again. */
  resync() {
    for (const m of MOD_KEYS) if (this.state[m] !== 'off') this.conn.key(m, 'down');
  }
}

export class KeyboardInput {
  /**
   * @param {HTMLTextAreaElement} ta
   * @param {import('./conn.js').Connection} conn
   * @param {Modifiers} mods
   */
  constructor(ta, conn, mods) {
    this.ta = ta;
    this.conn = conn;
    this.mods = mods;
    this.composing = false;
    this.reset();
    ta.addEventListener('input', () => this.onInput());
    ta.addEventListener('compositionstart', () => { this.composing = true; });
    ta.addEventListener('compositionend', () => {
      this.composing = false;
      this.onInput();
    });
    ta.addEventListener('keydown', (e) => this.onKeyDown(e));
    ta.addEventListener('select', () => this.caretToEnd());
  }

  reset() {
    this.ta.value = SENTINEL;
    this.last = SENTINEL;
    this.caretToEnd();
  }

  caretToEnd() {
    const n = this.ta.value.length;
    if (this.ta.selectionStart !== n || this.ta.selectionEnd !== n) {
      try { this.ta.setSelectionRange(n, n); } catch { /* not focused */ }
    }
  }

  onInput() {
    const now = Array.from(this.ta.value);
    const old = Array.from(this.last);
    let p = 0;
    while (p < old.length && p < now.length && old[p] === now[p]) p++;
    const deleted = old.length - p;
    const inserted = now.slice(p).join('').replace(/​/g, '');
    this.last = this.ta.value;

    for (let i = 0; i < deleted; i++) this.conn.key('Backspace');
    if (inserted) this.type(inserted);
    if (deleted || inserted) this.mods.consume();

    if (!this.composing && (now.length < 3 || now.length > 40 || inserted.includes('\n'))) this.reset();
  }

  type(text) {
    if (this.mods.active) {
      // With Ctrl/Alt/Super held, characters are shortcuts: press them as keys.
      for (const ch of text) this.conn.key(ch === '\n' ? 'Enter' : ch);
    } else {
      this.conn.text(text);
    }
  }

  onKeyDown(e) {
    // Hardware keyboards (and a few soft ones) deliver real key events for
    // keys that don't edit text. Printable keys are left to the diff above.
    if (e.isComposing || e.keyCode === 229) return;
    const k = e.key;
    const named = /^(Arrow|Page|F\d|Home$|End$|Escape$|Tab$|Delete$|Insert$|Media|Audio)/.test(k);
    const combo = (e.ctrlKey || e.metaKey || e.altKey) && k.length === 1;
    if (!named && !combo) return;
    e.preventDefault();
    const m = [];
    if (e.ctrlKey) m.push('Control');
    if (e.altKey) m.push('Alt');
    if (e.shiftKey && !combo) m.push('Shift');
    if (e.metaKey) m.push('Meta');
    this.conn.key(combo ? k.toLowerCase() : k, 'tap', m);
    this.mods.consume();
  }

  get open() { return document.activeElement === this.ta; }

  show() {
    this.reset();
    this.ta.focus({ preventScroll: true });
    this.caretToEnd();
  }

  hide() { this.ta.blur(); }
}
