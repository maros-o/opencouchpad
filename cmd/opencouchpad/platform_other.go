//go:build !windows && !darwin

package main

import (
	"errors"
	"io"
	"os"
)

func autostart(bool) error {
	return errors.New("--install/--uninstall are for Windows; on Linux use deploy/linux/install.sh (systemd) or Docker")
}

func detachConsole() {}

func logOutput(bool) io.Writer { return os.Stderr }
