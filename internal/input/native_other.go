//go:build !linux && !windows && !darwin

package input

import "errors"

// NewNative is not implemented on this platform yet.
func NewNative() (Driver, error) {
	return nil, errors.New("native input is not supported on this OS yet; use --driver dryrun")
}
