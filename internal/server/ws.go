package server

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/maros-o/opencouchpad/internal/input"
	"github.com/maros-o/opencouchpad/internal/keymap"
)

// Binary message types (little-endian payloads).
const (
	msgMove   = 0x01 // i16 dx, i16 dy
	msgScroll = 0x02 // i16 dx, i16 dy in 1/120 notch units
)

const readTimeout = 20 * time.Second

type message struct {
	T  string   `json:"t"`
	B  int      `json:"b,omitempty"`
	A  string   `json:"a,omitempty"`
	K  string   `json:"k,omitempty"`
	M  []string `json:"m,omitempty"`
	S  string   `json:"s,omitempty"`
	V  string   `json:"v,omitempty"`
	TS float64  `json:"ts,omitempty"`
}

// session is one connected remote. Everything runs on the read goroutine, so
// events are injected strictly in the order they were sent.
type session struct {
	srv     *Server
	conn    *websocket.Conn
	drv     input.Driver
	layout  string
	buttons map[input.Button]bool
	keys    map[string]bool
	log     *slog.Logger
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	n := s.clients.Add(1)
	defer s.clients.Add(-1)
	sess := &session{
		srv: s, conn: conn, drv: s.cfg.Driver, layout: s.cfg.DefaultLayout,
		buttons: map[input.Button]bool{}, keys: map[string]bool{},
		log: slog.With("client", clientIP(r)),
	}
	sess.log.Info("remote connected", "clients", n)
	defer func() {
		sess.releaseAll()
		conn.Close()
		sess.log.Info("remote disconnected", "clients", s.clients.Load()-1)
	}()

	hello := s.info(r)
	hello["t"] = "hello"
	if err := conn.WriteJSON(hello); err != nil {
		return
	}
	conn.SetReadLimit(64 << 10)
	for {
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		typ, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch typ {
		case websocket.BinaryMessage:
			sess.handleBinary(data)
		case websocket.TextMessage:
			var m message
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			if err := sess.handle(&m); err != nil {
				if errors.Is(err, errClose) {
					return
				}
				sess.log.Debug("input error", "msg", m.T, "err", err)
				conn.WriteJSON(map[string]any{"t": "error", "msg": err.Error()})
			}
		}
	}
}

var errClose = errors.New("close")

func (c *session) handleBinary(d []byte) {
	// A frame may carry several fixed-size records (the client batches under congestion).
	for len(d) >= 5 {
		dx := int32(int16(binary.LittleEndian.Uint16(d[1:])))
		dy := int32(int16(binary.LittleEndian.Uint16(d[3:])))
		switch d[0] {
		case msgMove:
			c.drv.Move(dx, dy)
		case msgScroll:
			c.drv.Scroll(dx, dy)
		default:
			return
		}
		d = d[5:]
	}
}

func (c *session) handle(m *message) error {
	switch m.T {
	case "ping":
		return c.conn.WriteJSON(map[string]any{"t": "pong", "ts": m.TS})
	case "btn":
		b := input.Button(m.B)
		if !b.Valid() {
			return errors.New("bad button")
		}
		switch m.A {
		case "down":
			c.buttons[b] = true
			return c.drv.Button(b, true)
		case "up":
			delete(c.buttons, b)
			return c.drv.Button(b, false)
		default: // click
			if err := c.drv.Button(b, true); err != nil {
				return err
			}
			return c.drv.Button(b, false)
		}
	case "key":
		return c.key(m)
	case "text":
		if m.S == "" {
			return nil
		}
		return c.drv.Type(m.S, c.layout)
	case "layout":
		if keymap.Has(m.V) {
			c.layout = m.V
		} else if m.V == "" || m.V == "auto" {
			c.layout = c.srv.cfg.DefaultLayout
		}
		return nil
	case "bye":
		return errClose
	}
	return nil
}

func (c *session) key(m *message) error {
	if !input.KnownKey(m.K) {
		return input.ErrUnknownKey
	}
	switch m.A {
	case "down":
		c.keys[m.K] = true
		return c.drv.Key(m.K, true, c.layout)
	case "up":
		delete(c.keys, m.K)
		return c.drv.Key(m.K, false, c.layout)
	}
	// tap with optional modifiers: press mods, tap key, release mods in reverse.
	var pressed []string
	defer func() {
		for i := len(pressed) - 1; i >= 0; i-- {
			c.drv.Key(pressed[i], false, c.layout)
		}
	}()
	for _, mod := range m.M {
		switch mod {
		case "Control", "Shift", "Alt", "AltGraph", "Meta":
		default:
			continue
		}
		if c.keys[mod] {
			continue // already held (sticky modifier)
		}
		if err := c.drv.Key(mod, true, c.layout); err != nil {
			return err
		}
		pressed = append(pressed, mod)
	}
	if err := c.drv.Key(m.K, true, c.layout); err != nil {
		return err
	}
	return c.drv.Key(m.K, false, c.layout)
}

// releaseAll lifts everything this client still holds so nothing gets stuck
// when a phone goes to sleep mid-drag.
func (c *session) releaseAll() {
	for b := range c.buttons {
		c.drv.Button(b, false)
	}
	for k := range c.keys {
		c.drv.Key(k, false, c.layout)
	}
}
