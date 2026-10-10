// Command openremote turns any phone browser into a touchpad and keyboard
// for this computer.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/maros-o/openremote/internal/input"
	"github.com/maros-o/openremote/internal/keymap"
	"github.com/maros-o/openremote/internal/mdns"
	"github.com/maros-o/openremote/internal/server"
	"github.com/maros-o/openremote/web"
)

var version = "dev"

type config struct {
	name, host, listen, pin, layout, driver, logLevel string
	allowedHosts                                      string
	port                                              int
	portSet, mdns, showVersion, background            bool
	install, uninstall                                bool
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func parseFlags() config {
	var c config
	hostname, _ := os.Hostname()
	if i := strings.IndexByte(hostname, '.'); i > 0 {
		hostname = hostname[:i]
	}
	defPort, portEnvSet := os.LookupEnv("OPENREMOTE_PORT")
	if !portEnvSet {
		defPort = "80"
	}
	port, _ := strconv.Atoi(defPort)

	flag.StringVar(&c.name, "name", env("OPENREMOTE_NAME", hostname), "display name shown in the UI and to other remotes")
	flag.StringVar(&c.host, "host", env("OPENREMOTE_HOST", "remote"), "mDNS host name: <host>.local")
	flag.IntVar(&c.port, "port", port, "HTTP port (default 80, falls back to 8080 if 80 is unavailable)")
	flag.StringVar(&c.listen, "listen", env("OPENREMOTE_LISTEN", ""), "listen address (default: all interfaces)")
	flag.StringVar(&c.pin, "pin", env("OPENREMOTE_PIN", ""), "require this PIN before a phone can control the computer")
	flag.StringVar(&c.allowedHosts, "allowed-hosts", env("OPENREMOTE_ALLOWED_HOSTS", ""), "extra comma-separated host names to accept, e.g. for a reverse proxy (.local, .lan and IPs always work)")
	flag.StringVar(&c.layout, "layout", env("OPENREMOTE_LAYOUT", "auto"), "keyboard layout of this computer for typing on Linux (e.g. us, de, cz, cz(qwerty)); auto = detect")
	flag.StringVar(&c.driver, "driver", env("OPENREMOTE_DRIVER", "native"), "input driver: native or dryrun (log only)")
	flag.BoolVar(&c.mdns, "mdns", envBool("OPENREMOTE_MDNS", true), "advertise <host>.local via mDNS and discover other remotes")
	flag.StringVar(&c.logLevel, "log-level", env("OPENREMOTE_LOG_LEVEL", "info"), "debug, info, warn or error")
	flag.BoolVar(&c.showVersion, "version", false, "print version and exit")
	flag.BoolVar(&c.background, "background", false, "run without a console window (Windows) and log to a file")
	flag.BoolVar(&c.install, "install", false, "start OpenRemote automatically at login (Windows) with the given flags")
	flag.BoolVar(&c.uninstall, "uninstall", false, "remove the autostart entry (Windows)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "OpenRemote %s — control this computer from any phone browser.\n\nUsage: openremote [flags]\n\nFlags (each also settable via OPENREMOTE_<NAME> env var):\n", version)
		flag.PrintDefaults()
	}
	flag.Parse()
	c.portSet = portEnvSet
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			c.portSet = true
		}
	})
	return c
}

func main() {
	c := parseFlags()
	if c.showVersion {
		fmt.Println("openremote", version, runtime.GOOS+"/"+runtime.GOARCH)
		return
	}
	if c.install || c.uninstall {
		if err := autostart(c.install); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if c.background {
		detachConsole()
	}

	var level slog.Level
	level.UnmarshalText([]byte(c.logLevel))
	slog.SetDefault(slog.New(slog.NewTextHandler(logOutput(c.background), &slog.HandlerOptions{Level: level})))

	if err := run(c); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func run(c config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Input driver.
	var drv input.Driver
	if c.driver == "dryrun" {
		drv = &input.DryRun{}
	} else {
		var err error
		if drv, err = input.NewNative(); err != nil {
			return err
		}
	}
	defer drv.Close()

	layout := c.layout
	if layout == "" || layout == "auto" {
		layout = keymap.Detect()
		if layout == "" {
			layout = keymap.Default
		}
	} else if !keymap.Has(layout) {
		slog.Warn("unknown keyboard layout, using fallback", "layout", layout, "using", keymap.Get(layout).ID)
		layout = keymap.Get(layout).ID
	}

	// Listener (port 80 → 8080 fallback unless the port was chosen explicitly).
	ln, err := net.Listen("tcp", net.JoinHostPort(c.listen, strconv.Itoa(c.port)))
	if err != nil && !c.portSet {
		slog.Warn("port unavailable, falling back to 8080", "port", c.port, "err", err)
		c.port = 8080
		ln, err = net.Listen("tcp", net.JoinHostPort(c.listen, "8080"))
	}
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	// mDNS.
	var resp *mdns.Responder
	if c.mdns {
		resp, err = mdns.Start(ctx, mdns.Config{
			Host: c.host, Instance: c.name, Port: c.port,
			TXT: map[string]string{"os": runtime.GOOS, "version": version, "path": "/"},
		})
		if err != nil {
			slog.Warn("mDNS disabled", "err", err)
			resp = nil
		}
	}

	urls := func() []string { return publicURLs(resp, c.port) }
	var peers func() []mdns.Peer
	if resp != nil {
		peers = resp.Peers
	}
	srv, err := server.New(server.Config{
		Name: c.name, OS: runtime.GOOS, Version: version, PIN: c.pin,
		DefaultLayout: layout, Driver: drv, Web: web.FS, Peers: peers, URLs: urls,
		AllowedHosts: strings.FieldsFunc(c.allowedHosts, func(r rune) bool { return r == ',' || r == ' ' }),
	})
	if err != nil {
		return err
	}
	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}

	banner(c, drv, layout, urls())

	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	select {
	case <-ctx.Done():
		slog.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
		time.Sleep(100 * time.Millisecond) // let the mDNS goodbye go out
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// publicURLs lists addresses a phone can open, best first.
func publicURLs(resp *mdns.Responder, port int) []string {
	suffix := ""
	if port != 80 {
		suffix = ":" + strconv.Itoa(port)
	}
	var out []string
	if resp != nil {
		out = append(out, "http://"+resp.Host()+suffix)
	}
	for _, ip := range lanIPs() {
		out = append(out, "http://"+ip+suffix)
	}
	return out
}

func lanIPs() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 || isVirtual(ifi.Name) {
			continue
		}
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				out = append(out, n.IP.String())
			}
		}
	}
	return out
}

func isVirtual(name string) bool {
	for _, p := range []string{"docker", "br-", "veth", "virbr", "vmnet", "vboxnet", "tailscale", "zt", "wg", "tun", "lxc", "cni", "flannel", "vEthernet"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func banner(c config, drv input.Driver, layout string, urls []string) {
	slog.Info("OpenRemote started", "version", version, "name", c.name, "driver", drv.Name(), "layout", layout, "pin", c.pin != "")
	if c.background {
		return
	}
	fmt.Println()
	fmt.Println("  ╭─ OpenRemote ─────────────────────────────────────")
	fmt.Println("  │  Open on your phone (same Wi-Fi):")
	for _, u := range urls {
		fmt.Println("  │    " + u)
	}
	if c.pin == "" {
		fmt.Println("  │  ⚠  No PIN set: anyone on this network can control this computer.")
		fmt.Println("  │     Use --pin 1234 (or OPENREMOTE_PIN) on shared networks.")
	}
	fmt.Println("  ╰──────────────────────────────────────────────────")
	// QR for the first IP URL: works even where .local names don't resolve.
	target := ""
	for _, u := range urls {
		if !strings.Contains(u, ".local") {
			target = u
			break
		}
	}
	if target != "" {
		if q, err := qrcode.New(target, qrcode.Low); err == nil {
			fmt.Println("  Scan to open " + target + ":")
			for _, line := range strings.Split(q.ToSmallString(false), "\n") {
				fmt.Println("  " + line)
			}
		}
	}
}
