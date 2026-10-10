package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"

	"github.com/maros-o/openremote/internal/input"
)

type recorder struct {
	input.DryRun
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(e string) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *recorder) Button(b input.Button, down bool) error {
	r.add("btn")
	return nil
}

func (r *recorder) Key(name string, down bool, layout string) error {
	d := "up"
	if down {
		d = "down"
	}
	r.add(name + ":" + d)
	return r.DryRun.Key(name, down, layout)
}

func newTestServer(t *testing.T, pin string) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	s, err := New(Config{
		Name: "test", OS: "linux", Version: "dev", PIN: pin, DefaultLayout: "us",
		Driver: rec,
		Web:    fstest.MapFS{"index.html": {Data: []byte("<!doctype html>" + strings.Repeat("x", 1000))}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts, rec
}

func dial(t *testing.T, ts *httptest.Server, cookie string) (*websocket.Conn, *http.Response, error) {
	h := http.Header{}
	if cookie != "" {
		h.Set("Cookie", cookie)
	}
	return websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", h)
}

func TestStaticGzipAndETag(t *testing.T) {
	ts, _ := newTestServer(t, "")
	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Header.Get("Content-Encoding") != "gzip" || res.Header.Get("ETag") == "" {
		t.Fatalf("headers: %v", res.Header)
	}
	req.Header.Set("If-None-Match", res.Header.Get("ETag"))
	res, _ = http.DefaultTransport.RoundTrip(req)
	if res.StatusCode != http.StatusNotModified {
		t.Fatalf("want 304, got %d", res.StatusCode)
	}
}

func TestWSProtocol(t *testing.T) {
	ts, rec := newTestServer(t, "")
	c, _, err := dial(t, ts, "")
	if err != nil {
		t.Fatal(err)
	}
	var hello map[string]any
	if err := c.ReadJSON(&hello); err != nil || hello["t"] != "hello" {
		t.Fatalf("hello: %v %v", hello, err)
	}
	// two batched moves + one scroll in a single frame
	c.WriteMessage(websocket.BinaryMessage, []byte{1, 5, 0, 0xfb, 0xff, 1, 1, 0, 1, 0, 2, 0, 0, 120, 0})
	c.WriteJSON(map[string]any{"t": "key", "k": "c", "m": []string{"Control"}})
	c.WriteJSON(map[string]any{"t": "btn", "b": 1, "a": "down"})
	c.WriteJSON(map[string]any{"t": "key", "k": "Shift", "a": "down"})
	c.WriteJSON(map[string]any{"t": "ping", "ts": 42})
	var pong map[string]any
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := c.ReadJSON(&pong); err != nil || pong["t"] != "pong" || pong["ts"] != float64(42) {
		t.Fatalf("pong: %v %v", pong, err)
	}
	if rec.Moves.Load() != 2 || rec.Scrolls.Load() != 1 {
		t.Fatalf("moves=%d scrolls=%d", rec.Moves.Load(), rec.Scrolls.Load())
	}
	want := "Control:down c:down c:up Control:up btn Shift:down"
	if got := strings.Join(rec.snapshot(), " "); got != want {
		t.Fatalf("events:\n got %s\nwant %s", got, want)
	}
	// Disconnect must release the held button and key.
	c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(rec.snapshot()) < 8 {
		time.Sleep(10 * time.Millisecond)
	}
	tail := strings.Join(rec.snapshot()[6:], " ")
	if tail != "btn Shift:up" {
		t.Fatalf("release on disconnect: %q", tail)
	}
}

func TestAuth(t *testing.T) {
	ts, _ := newTestServer(t, "1234")
	if _, res, err := dial(t, ts, ""); err == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatal("expected 401 without cookie")
	}
	res, _ := http.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"pin":"0000"}`))
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong pin: %d", res.StatusCode)
	}
	res, _ = http.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"pin":"1234"}`))
	if res.StatusCode != http.StatusOK || len(res.Cookies()) != 1 {
		t.Fatalf("login: %d", res.StatusCode)
	}
	ck := res.Cookies()[0]
	c, _, err := dial(t, ts, ck.Name+"="+ck.Value)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestAllowedHost(t *testing.T) {
	s := &Server{cfg: Config{AllowedHosts: []string{"remote.example.com", ".corp.net"}}}
	for host, want := range map[string]bool{
		"remote.local": true, "remote.local:8080": true, "192.168.1.5": true, "[fe80::1]:80": true,
		"remote": true, "localhost:8080": true, "pi.home.arpa": true, "remote.example.com": true,
		"a.corp.net": true, "evil.example": false, "remote.local.evil.com": false,
	} {
		if got := s.allowedHost(host); got != want {
			t.Errorf("allowedHost(%q)=%v want %v", host, got, want)
		}
	}
}
