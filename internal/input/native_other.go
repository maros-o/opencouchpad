//go:build !linux && !windows

package input

import "errors"

// NewNative is not implemented on this platform yet (macOS is on the roadmap).
func NewNative() (Driver, error) {
	return nil, errors.New("native input is not supported on this OS yet; use --driver dryrun")
}
