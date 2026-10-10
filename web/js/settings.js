// Per-phone preferences, kept in localStorage.
const KEY = 'opencouchpad.settings.v1';

export const defaults = {
  speed: 1.6,      // base pointer gain (computer px per finger px)
  accel: 1.0,      // extra gain for fast swipes
  scroll: 2.5,     // scroll units per finger px (120 units = one wheel notch)
  natural: true,   // content follows fingers
  inertia: true,   // momentum after a two-finger flick
  tap: true,       // tap to click
  cleanDrag: false, // delay tap clicks so tap-and-drag doesn't start with a click
  lefty: false,    // swap buttons
  strip: false,    // scroll strip on the right edge
  pinch: false,    // pinch → Ctrl + wheel
  haptics: true,
  layout: 'auto',  // computer keyboard layout override (Linux typing)
  theme: 'auto',
};

function load() {
  try {
    return { ...defaults, ...JSON.parse(localStorage.getItem(KEY) || '{}') };
  } catch {
    return { ...defaults };
  }
}

export const settings = load();
const listeners = new Set();

export function set(key, value) {
  settings[key] = value;
  try { localStorage.setItem(KEY, JSON.stringify(settings)); } catch { /* private mode */ }
  listeners.forEach((fn) => fn(key, value));
}

export function reset() {
  for (const k of Object.keys(defaults)) set(k, defaults[k]);
}

export function onChange(fn) { listeners.add(fn); }
