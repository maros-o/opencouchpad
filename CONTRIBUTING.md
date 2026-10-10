# Contributing

Thanks for helping improve OpenCouchpad!

- Run `make dev` to work on the UI without moving your real mouse (dry-run driver), and `make test` before sending a PR.
- Keep the web app dependency-free (vanilla ES modules, no build step) and the binary cgo-free.
- Input latency is the top priority: don't add work to the touch → WebSocket path (no batching to animation frames, no heavy per-event allocations).
- New keyboard layout? Add it to `LAYOUTS` in `tools/genlayouts.py`, run `make layouts`, add a test in `internal/keymap`.
- Read [AGENTS.md](AGENTS.md) for the architecture, protocol and design decisions.
