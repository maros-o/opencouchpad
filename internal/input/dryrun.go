package input

import (
	"log/slog"
	"sync/atomic"
)

// DryRun logs input instead of injecting it. Useful for development, UI work
// and running on unsupported platforms.
type DryRun struct {
	Moves, Scrolls, Buttons, Keys, Texts atomic.Int64
}

func (d *DryRun) Name() string { return "dryrun" }

func (d *DryRun) Move(dx, dy int32) error {
	d.Moves.Add(1)
	slog.Debug("move", "dx", dx, "dy", dy)
	return nil
}

func (d *DryRun) Scroll(dx, dy int32) error {
	d.Scrolls.Add(1)
	slog.Debug("scroll", "dx", dx, "dy", dy)
	return nil
}

func (d *DryRun) Button(b Button, down bool) error {
	d.Buttons.Add(1)
	slog.Info("button", "button", b, "down", down)
	return nil
}

func (d *DryRun) Key(name string, down bool, layout string) error {
	if !KnownKey(name) {
		return ErrUnknownKey
	}
	d.Keys.Add(1)
	slog.Info("key", "key", name, "down", down, "layout", layout)
	return nil
}

func (d *DryRun) Type(text, layout string) error {
	d.Texts.Add(1)
	slog.Info("type", "text", text, "layout", layout)
	return nil
}

func (d *DryRun) Close() error { return nil }
