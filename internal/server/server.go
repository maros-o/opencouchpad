// Package server serves the web UI and the WebSocket input protocol.
package server

import (
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/maros-o/opencouchpad/internal/input"
	"github.com/maros-o/opencouchpad/internal/keymap"
	"github.com/maros-o/opencouchpad/internal/mdns"
)

// Config configures the server.
type Config struct {
	Name          string // display name
	OS            string
	Version       string
	PIN           string
	DefaultLayout string
	Driver        input.Driver
	Web           fs.FS
	Peers         func() []mdns.Peer // may be nil
	URLs          func() []string    // addresses phones can use, best first
	AllowedHosts  []string           // extra Host names accepted (e.g. behind a reverse proxy)
}

// Server is the HTTP handler.
type Server struct {
	cfg      Config
	mux      *http.ServeMux
	assets   map[string]*asset
	token    string
	clients  atomic.Int32
	upgrader websocket.Upgrader
	limiter  *loginLimiter
}

type asset struct {
	body  []byte
	gz    []byte
	ctype string
	etag  string
}

const cookieName = "opencouchpad_session"

// New builds a server.
func New(cfg Config) (*Server, error) {
	s := &Server{
		cfg:     cfg,
		mux:     http.NewServeMux(),
		assets:  map[string]*asset{},
		limiter: newLoginLimiter(),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}
	if cfg.PIN != "" {
		m := hmac.New(sha256.New, []byte("opencouchpad-session-v1"))
		m.Write([]byte(cfg.PIN))
		s.token = hex.EncodeToString(m.Sum(nil))
	}
	if err := s.loadAssets(); err != nil {
		return nil, err
	}
	s.mux.HandleFunc("GET /ws", s.handleWS)
	s.mux.HandleFunc("GET /api/info", s.handleInfo)
	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("GET /api/peers", s.handlePeers)
	s.mux.HandleFunc("GET /api/qr.png", s.handleQR)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	s.mux.HandleFunc("GET /", s.handleStatic)
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.allowedHost(r.Host) {
		// Blocks DNS-rebinding: a web page on evil.example re-pointed at this
		// machine's LAN address would otherwise be "same origin" with us.
		http.Error(w, "host not allowed; add it with --allowed-hosts", http.StatusMisdirectedRequest)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.mux.ServeHTTP(w, r)
}

// allowedHost accepts IP literals, single-label names, mDNS/LAN suffixes and
// explicitly configured names.
func (s *Server) allowedHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return true
	}
	for _, suf := range []string{".local", ".lan", ".home", ".home.arpa", ".internal", ".localhost", ".localdomain"} {
		if strings.HasSuffix(host, suf) {
			return true
		}
	}
	for _, a := range s.cfg.AllowedHosts {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "*" || a == host || (strings.HasPrefix(a, ".") && strings.HasSuffix(host, a)) {
			return true
		}
	}
	return false
}

// Clients returns the number of connected remotes.
func (s *Server) Clients() int { return int(s.clients.Load()) }

// loadAssets reads the embedded UI once, precomputing gzip and ETags.
func (s *Server) loadAssets() error {
	return fs.WalkDir(s.cfg.Web, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, ".go") {
			return err
		}
		body, err := fs.ReadFile(s.cfg.Web, p)
		if err != nil {
			return err
		}
		ctype := mime.TypeByExtension(path.Ext(p))
		switch path.Ext(p) {
		case ".webmanifest":
			ctype = "application/manifest+json"
		case ".js":
			ctype = "text/javascript; charset=utf-8"
		}
		if ctype == "" {
			ctype = http.DetectContentType(body)
		}
		sum := sha256.Sum256(body)
		a := &asset{body: body, ctype: ctype, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
		if len(body) > 512 && !strings.HasPrefix(ctype, "image/png") {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			zw.Write(body)
			zw.Close()
			if buf.Len() < len(body) {
				a.gz = buf.Bytes()
			}
		}
		s.assets["/"+p] = a
		return nil
	})
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if p == "/" {
		p = "/index.html"
	}
	a, ok := s.assets[p]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.ctype)
	h.Set("ETag", a.etag)
	// Always revalidate: the UI is tiny and must update together with the server.
	h.Set("Cache-Control", "no-cache")
	h.Set("Vary", "Accept-Encoding")
	if p == "/index.html" {
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self' ws: wss:")
	}
	if r.Header.Get("If-None-Match") == a.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := a.body
	if a.gz != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		body = a.gz
	}
	w.Write(body)
}

func (s *Server) authed(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	c, err := r.Cookie(cookieName)
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.token)) == 1
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) info(r *http.Request) map[string]any {
	urls := []string{}
	if s.cfg.URLs != nil {
		urls = s.cfg.URLs()
	}
	return map[string]any{
		"name":    s.cfg.Name,
		"os":      s.cfg.OS,
		"version": s.cfg.Version,
		"driver":  s.cfg.Driver.Name(),
		"auth":    s.token != "",
		"authed":  s.authed(r),
		"layout":  s.cfg.DefaultLayout,
		"layouts": keymap.List(),
		"urls":    urls,
	}
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.info(r))
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	peers := []mdns.Peer{}
	if s.cfg.Peers != nil {
		peers = s.cfg.Peers()
	}
	writeJSON(w, http.StatusOK, peers)
}

func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("u")
	if u == "" || len(u) > 512 || !(strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")) {
		http.Error(w, "bad url", http.StatusBadRequest)
		return
	}
	png, err := qrcode.Encode(u, qrcode.Medium, 320)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=3600")
	w.Write(png)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.token == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	ip := clientIP(r)
	if wait := s.limiter.blocked(ip); wait > 0 {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "Too many attempts. Try again in " + wait.Round(time.Second).String() + "."})
		return
	}
	var body struct {
		PIN string `json:"pin"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad request"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.PIN), []byte(s.cfg.PIN)) != 1 {
		s.limiter.fail(ip)
		time.Sleep(400 * time.Millisecond)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "Wrong PIN"})
		return
	}
	s.limiter.reset(ip)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: s.token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: 365 * 24 * 3600,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// loginLimiter blocks an IP for a while after repeated wrong PINs.
type loginLimiter struct {
	mu    sync.Mutex
	fails map[string]*failState
}

type failState struct {
	n     int
	until time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{fails: map[string]*failState{}} }

func (l *loginLimiter) blocked(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f, ok := l.fails[ip]; ok {
		return time.Until(f.until)
	}
	return 0
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, ok := l.fails[ip]
	if !ok {
		f = &failState{}
		l.fails[ip] = f
	}
	f.n++
	if f.n >= 5 {
		f.until = time.Now().Add(time.Duration(f.n-4) * 30 * time.Second)
	}
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
