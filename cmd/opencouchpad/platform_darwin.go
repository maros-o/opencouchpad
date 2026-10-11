//go:build darwin

package main

import (
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const agentLabel = "io.github.maros-o.opencouchpad"

func agentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist")
}

// autostart adds/removes a LaunchAgent. Input injection must happen inside
// the user's GUI session, so a per-user agent is the right mechanism (a
// LaunchDaemon runs outside it).
func autostart(install bool) error {
	domain := "gui/" + strconv.Itoa(os.Getuid())
	p := agentPath()
	exec.Command("launchctl", "bootout", domain+"/"+agentLabel).Run()
	if !install {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("Autostart removed.")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	args := []string{exe, "--background"}
	for _, a := range os.Args[1:] {
		if a == "--install" || a == "-install" || a == "--background" || a == "-background" {
			continue
		}
		args = append(args, a)
	}
	var argXML string
	for _, a := range args {
		argXML += "\t\t<string>" + html.EscapeString(a) + "</string>\n"
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + agentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
` + argXML + `	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(plist), 0o644); err != nil {
		return err
	}
	// bootout returns before the old agent is gone, and bootstrap fails until it is.
	for i := 0; ; i++ {
		out, err := exec.Command("launchctl", "bootstrap", domain, p).CombinedOutput()
		if err == nil {
			break
		}
		if i == 10 {
			return fmt.Errorf("launchctl bootstrap: %v: %s", err, out)
		}
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Println("OpenCouchpad is running and will start automatically when you log in.")
	fmt.Println("  Agent:", p)
	fmt.Println("  Log:  ", logPath())
	fmt.Println("macOS will ask to allow Accessibility access for opencouchpad. Allow it, then restart it:")
	fmt.Println("  launchctl kickstart -k " + domain + "/" + agentLabel)
	return nil
}

func detachConsole() {}

func logPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "OpenCouchpad", "opencouchpad.log")
}

func logOutput(background bool) io.Writer {
	if !background {
		return os.Stderr
	}
	return openLog(logPath())
}
