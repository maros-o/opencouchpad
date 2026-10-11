# AGENTS.md — OpenCouchpad

Working notes for AI agents / contributors. Keep this file up to date: plan, decisions, progress.

## Goal

Self-hosted, open-source **web-based remote mouse & keyboard** for computers on the local network.
Replace the Android-only "Linux Remote" app: any phone (iPhone, Android, tablet, laptop) opens
`http://remote.local` in a browser and gets a precise, low-latency touchpad + keyboard.

Requirements from the owner:
- Works from **any browser** (iOS Safari, Android Chrome) — no app install.
- Reachable as `remote.local` on Wi-Fi (mDNS).
- Server runs on **Linux (Raspberry Pi, desktops)**, **Windows** and **macOS**. Multiple servers on one LAN.
- **Very optimized, responsive, precise** — mouse is the #1 priority.
- Open-source, self-hosted, **Docker ready**, great README, modern UI.

## Architecture

```
phone browser ──HTTP──▶ opencouchpad (Go, single static binary)
               ◀─WS──▶   ├─ embedded web UI (vanilla JS, no build step)
                         ├─ WebSocket: binary frames for move/scroll, JSON for the rest
                         ├─ input drivers: linux=uinput, windows=SendInput, darwin=CGEvent, dryrun=log
                         ├─ keymap: XKB layout tables (generated) for Linux text typing
                         └─ mDNS responder: <host>.local A record + _opencouchpad._tcp service + peer discovery
```

- **Language:** Go (CGO_ENABLED=0) → trivial cross-compile (linux amd64/arm64/armv7/armv6, windows amd64/arm64, darwin arm64/amd64), tiny Docker image (`scratch`).
- **Linux input:** `/dev/uinput` via raw ioctls (no cgo). Works on X11 *and* Wayland, console too. Separate virtual mouse + keyboard devices. Hi-res wheel (`REL_WHEEL_HI_RES`) for smooth scrolling.
- **Windows input:** `user32!SendInput` via syscall. Text uses `KEYEVENTF_UNICODE` (layout independent). Combos use `VkKeyScanW`.
- **macOS input:** CoreGraphics `CGEventPost` via `github.com/ebitengine/purego` (still no cgo). Needs the Accessibility
  permission (prompted at start; without it events are dropped silently, so the driver returns an error the UI shows).
  Pointer moves are absolute (current position + delta, clamped to the displays) with delta fields set; clicks carry
  `clickState` for double-clicks; held modifiers are tracked and set as flags on every event. Shortcut chars resolve via
  `UCKeyTranslate` on the current layout (read once at start, on the main thread: TIS calls crash elsewhere on macOS 14+),
  text uses Unicode string events, media/brightness keys are `NSEvent` system-defined events. Autostart is a LaunchAgent.
  The web UI swaps shortcuts to Cmd variants (`data-mac` attributes) when the server's os is `darwin`.
  **Not yet tested on real hardware.**
- **Linux text typing:** uinput only sends keycodes, so text → keycodes depends on the active XKB layout.
  `tools/genlayouts.py` uses `xkbcli compile-keymap` + `xkbcommon-keysyms.h` to generate exact tables
  (incl. AltGr levels and dead-key compositions) → `internal/keymap/layouts_gen.go`.
  Layout auto-detected from KDE `kxkbrc`, `/etc/default/keyboard` or xorg conf; overridable by flag/env and per-client in UI settings.
  The sandboxed service (ProtectHome) cannot read `kxkbrc`, so install.sh runs `--detect-layout` as `$SUDO_USER` and saves `OPENCOUCHPAD_LAYOUT`.
- **mDNS:** own minimal responder (`golang.org/x/net/dns/dnsmessage`, `ipv4`), SO_REUSEADDR to coexist with avahi / Windows mDNS.
  Probes for name conflict (`remote` → `remote-2`). Answers using the interface address in the querier's subnet.
  Browses `_opencouchpad._tcp` to list other OpenCouchpad servers → device switcher in UI.
- **Protocol** (WebSocket `/ws`):
  - Binary: `0x01 move  i16 dx, i16 dy` · `0x02 scroll i16 dx, i16 dy` (1/120 notch units)
  - JSON text: `{"t":"btn","b":1-5,"a":"down|up|click"}`, `{"t":"key","k":"Enter","m":["ctrl"],"a":"tap|down|up"}`,
    `{"t":"text","s":"..."}`, `{"t":"ping","ts":n}`, `{"t":"layout","v":"cz"}`, `{"t":"zoom","d":±n}`
  - Server → client: `hello` (name, os, version, layouts, layout), `pong`, `error`.
  - On disconnect the server releases every button/key that client still holds (no stuck keys).
- **Client latency tricks:** send on every touchmove (no rAF batching); coalesce only when `ws.bufferedAmount` grows (congestion);
  sub-pixel remainder accumulation; velocity-based acceleration curve (EMA); kinetic (inertial) two-finger scroll; ping/RTT indicator; auto-reconnect.
- **Auth:** optional PIN (`--pin` / `OPENCOUCHPAD_PIN`) → HMAC cookie; WS checks cookie. Off by default (LAN), README warns.

## Layout

```
cmd/opencouchpad/        main, flags/env config, startup banner + terminal QR
internal/input/        Driver interface; uinput_linux.go, sendinput_windows.go, dryrun.go, keys (names → codes)
internal/keymap/       layout tables (generated) + lookup; Linux text → strokes
internal/server/       HTTP, WS, protocol, auth, peers API, QR endpoint
internal/mdns/         responder + browser
web/                   embedded UI (index.html, css, js, icons, manifest)
tools/genlayouts.py    XKB → Go table generator
deploy/                systemd unit, udev rule, install scripts, windows autostart
Dockerfile, docker-compose.yml, Makefile, .github/workflows
```

## Plan / Progress

Status: **v1 feature-complete** (2026-10-10).

- [x] Plan + AGENTS.md
- [x] Go module, config (flags + `OPENCOUCHPAD_*` env), main, startup banner + terminal QR
- [x] input: Linux uinput (separate mouse + keyboard devices, hi-res wheel, restricted key bits — no power/rfkill)
- [x] input: Windows SendInput (Unicode text, VkKeyScan combos, scan codes, extended keys)
- [x] input: dryrun driver
- [x] input: macOS CGEvent via purego (layout-aware shortcut keys, Unicode text, media keys, double-click state), LaunchAgent `--install`, `deploy/macos/install.sh`, Cmd shortcuts in UI
- [x] keymap generator (`tools/genlayouts.py`) → 34 layouts incl. AltGr + dead keys; auto-detect; tests
- [x] server: static (gzip + ETag), WS protocol, release-on-disconnect, PIN auth + lockout, peers, QR, Host allowlist (anti DNS-rebinding)
- [x] mDNS: A record, probing/conflict rename, DNS-SD service, peer browse (prefers LAN over VPN addrs), goodbye
- [x] web UI: touchpad gestures (move, tap, 2/3-finger tap, tap-drag, optional clean tap-drag, scroll + momentum, pinch opt., scroll strip opt., desktop mouse/wheel)
- [x] web UI: keyboard diff engine, special-keys row w/ auto-repeat, sticky modifiers (held on host), send-text
- [x] web UI: media, shortcuts (Alt+Tab switcher), settings, device switcher, PIN screen, status/RTT, dark/light, landscape
- [x] PWA: manifest, SVG + PNG icons, apple-touch-icon
- [x] Dockerfile (multi-arch, scratch), compose, Makefile, GitHub Actions (test, release binaries, ghcr multi-arch)
- [x] deploy: systemd (hardened), env file, udev rule, install.sh, Windows install.ps1 + `--install` autostart
- [x] README, LICENSE (MIT), CONTRIBUTING, screenshots

### Verification done
- `go test -race ./...` (keymap, mdns, server protocol/auth/host check), `go vet` incl. GOOS=windows and GOOS=darwin.
- Cross-compiled linux amd64/arm64/armv7/armv6, windows amd64/arm64/386, darwin arm64/amd64.
- Windows runtime tested by the owner with a real phone (2026-10-11).
- Headless Chromium (playwright, mobile emulation, CDP touch events): every gesture, keyboard diff, shortcuts,
  clean-drag, modifiers, PIN flow, device switcher — scripts in `test/e2e/`.
- Real `/dev/uinput` on the dev machine (KDE Wayland): devices created, motion accepted.
- mDNS: `avahi-resolve -n <host>.local` and `avahi-browse _opencouchpad._tcp` see the server; two servers discover each other.

### Not verified yet
- macOS runtime (only compiled; no Mac available): purego struct calls, Accessibility prompt, media-key NSEvents,
  mDNS next to mDNSResponder, LaunchAgent install. Docker build (docker not installed).

### Ideas / next
- macOS: pinch-zoom sends Ctrl+wheel (accessibility zoom on a Mac); consider Cmd+/- there. Optional HTTPS (wake lock, clipboard), gyro air-mouse, custom buttons.
- Run `test/e2e` in CI.
- Repo: https://github.com/maros-o/opencouchpad (module `github.com/maros-o/opencouchpad`). Renamed from OpenRemote in v0.2.0 (name taken by an IoT platform).

## Decisions log

- No frontend build step (vanilla ES modules) — keeps Docker/CI simple and the page tiny.
- Tap = immediate click (latency first). "Clean tap-and-drag" setting delays the click 300 ms so drags don't start with a click (libinput-style).
- Modifiers from the toolbar are really held down on the host while armed → Shift/Ctrl+click and Ctrl+scroll work.
- Default port 80 (so `http://remote.local` works); if not explicitly set and 80 fails → 8080.
- Docker requires `--network host` (mDNS) and `--device /dev/uinput`. Windows must run natively (Docker on Windows can't inject host input).
- macOS not supported yet (needs cgo/CGEvent) — roadmap.
