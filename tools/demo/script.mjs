// The demo's storyboard: finger paths (normalized to the touchpad, 0..1) and copy.
// capture.mjs plays them on the real app; render.mjs draws them and the result.

// iPhone 15 viewport between status bar and Safari bar; kbH = above the on-screen keyboard (300 px)
export const PHONE = { w: 393, h: 716, kbH: 498 };
export const TEXT = 'rainy day jazz';

const ease = (t) => (t < 0.5 ? 4 * t * t * t : 1 - (-2 * t + 2) ** 3 / 2); // in-out cubic
const lerp = (a, b, t) => a + (b - a) * t;
const bez = (p0, p1, p2, t) => [0, 1].map((i) => (1 - t) ** 2 * p0[i] + 2 * (1 - t) * t * p1[i] + t * t * p2[i]);
const glide = (p0, p1, p2, from, dur) => (ms) => (ms >= from && ms <= from + dur ? [bez(p0, p1, p2, ease((ms - from) / dur))] : undefined);
const tap = (p, from) => (ms) => (ms >= from && ms < from + 80 ? [p] : undefined);
const seq = (...parts) => (ms) => { for (const p of parts) { const r = p(ms); if (r) return r; } return null; };

export const GESTURES = [
  {
    // One finger glides down to the dock, lifts; then a tap opens the app.
    id: 'dock',
    dur: 2000,
    tail: 400,
    at: seq(glide([0.26, 0.22], [0.28, 0.66], [0.74, 0.70], 0, 1400), tap([0.62, 0.50], 1680)),
  },
  {
    // Two-finger flick upwards → the grid scrolls and keeps gliding.
    id: 'scroll',
    dur: 1000,
    tail: 2000,
    at(ms) {
      if (ms > 220) return null;
      const t = (ms / 220) ** 1.4; // still accelerating at lift-off, like a real flick
      return [[0.40, lerp(0.78, 0.40, t)], [0.62, lerp(0.80, 0.42, t)]];
    },
  },
  {
    // Two strokes up to the search field (lift and re-place, as on a laptop), tap.
    id: 'search',
    dur: 2000,
    tail: 400,
    at: seq(
      glide([0.50, 0.88], [0.52, 0.60], [0.55, 0.32], 0, 650),
      glide([0.48, 0.64], [0.485, 0.58], [0.49, 0.52], 820, 500),
      tap([0.50, 0.50], 1700),
    ),
  },
  {
    // Down-left to the first result, tap to play.
    id: 'play',
    dur: 1500,
    tail: 400,
    at: seq(glide([0.66, 0.30], [0.50, 0.32], [0.30, 0.52], 0, 850), tap([0.40, 0.50], 1180)),
  },
];
