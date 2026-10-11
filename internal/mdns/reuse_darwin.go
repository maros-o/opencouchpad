//go:build darwin

package mdns

import "syscall"

// macOS's mDNSResponder holds 5353 with SO_REUSEPORT, so we need it too.
func reuseControl(network, address string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		if serr == nil {
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
		}
	})
	if err != nil {
		return err
	}
	return serr
}
