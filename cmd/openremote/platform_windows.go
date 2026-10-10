//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// autostart adds/removes a per-user "Run" entry. Input injection must happen
// inside the user's desktop session, so a login item is the right mechanism
// (a Windows service runs in session 0 and cannot move the cursor).
func autostart(install bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !install {
		if err := k.DeleteValue("OpenRemote"); err != nil && err != registry.ErrNotExist {
			return err
		}
		fmt.Println("Autostart removed.")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{`"` + exe + `"`, "--background"}
	for _, a := range os.Args[1:] {
		if a == "--install" || a == "-install" || a == "--background" || a == "-background" {
			continue
		}
		if strings.ContainsAny(a, " \t") {
			a = `"` + a + `"`
		}
		args = append(args, a)
	}
	cmd := strings.Join(args, " ")
	if err := k.SetStringValue("OpenRemote", cmd); err != nil {
		return err
	}
	fmt.Println("OpenRemote will start automatically when you log in:")
	fmt.Println("  " + cmd)
	fmt.Println("Log file:", logPath())
	return nil
}

func detachConsole() {
	syscall.NewLazyDLL("kernel32.dll").NewProc("FreeConsole").Call()
}

func logPath() string {
	dir := os.Getenv("LOCALAPPDATA")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "OpenRemote", "openremote.log")
}

func logOutput(background bool) io.Writer {
	if !background {
		return os.Stderr
	}
	p := logPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if fi, err := os.Stat(p); err == nil && fi.Size() > 5<<20 {
		os.Rename(p, p+".old")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return io.Discard
	}
	return f
}
