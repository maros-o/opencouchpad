// Package mdns is a small multicast DNS responder and service browser.
//
// It publishes <host>.local (A record) so phones can open http://remote.local,
// advertises a DNS-SD service (_opencouchpad._tcp) and discovers other
// OpenCouchpad servers on the LAN. It shares UDP 5353 with avahi / the Windows
// mDNS stack via SO_REUSEADDR.
package mdns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"
)

// ServiceType is the DNS-SD service type used by OpenCouchpad.
const ServiceType = "_opencouchpad._tcp.local."

const (
	ttlHost    = 120
	ttlService = 4500
	cacheFlush = 0x8000
)

var (
	groupAddr = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	servicesQ = mustName("_services._dns-sd._udp.local.")
	serviceN  = mustName(ServiceType)
)

func mustName(s string) dnsmessage.Name {
	n, err := dnsmessage.NewName(s)
	if err != nil {
		panic(err)
	}
	return n
}

// Config describes what to publish.
type Config struct {
	Host     string            // desired host label, e.g. "remote" → remote.local
	Instance string            // human readable service instance name
	Port     int               // HTTP port
	TXT      map[string]string // extra TXT key/values
}

// Peer is another OpenCouchpad server discovered on the network.
type Peer struct {
	Instance string            `json:"name"`
	Host     string            `json:"host"`
	IP       string            `json:"ip"`
	Port     int               `json:"port"`
	TXT      map[string]string `json:"txt"`
	expires  time.Time
	virtual  bool
}

// Responder publishes records and browses for peers.
type Responder struct {
	cfg      Config
	conn     *ipv4.PacketConn
	ifaces   []net.Interface
	sendMu   sync.Mutex
	mu       sync.Mutex
	host     string // final host label after conflict resolution
	probing  string // label currently being probed
	conflict chan struct{}
	peers    map[string]*Peer
	ready    bool
}

// Start opens the socket, resolves name conflicts, announces and serves until
// ctx is cancelled. It returns once the host name is claimed.
func Start(ctx context.Context, cfg Config) (*Responder, error) {
	lc := net.ListenConfig{Control: reuseControl}
	pc, err := lc.ListenPacket(ctx, "udp4", "0.0.0.0:5353")
	if err != nil {
		return nil, fmt.Errorf("mdns listen: %w", err)
	}
	r := &Responder{cfg: cfg, conn: ipv4.NewPacketConn(pc), peers: map[string]*Peer{}}
	r.conn.SetMulticastTTL(255)
	r.conn.SetMulticastLoopback(true)
	r.joinAll()
	if len(r.ifaces) == 0 {
		pc.Close()
		return nil, errors.New("mdns: no multicast-capable network interface")
	}
	go r.readLoop()

	if err := r.claim(ctx); err != nil {
		pc.Close()
		return nil, err
	}
	go r.run(ctx)
	return r, nil
}

// Host returns the claimed host name, e.g. "remote.local".
func (r *Responder) Host() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.host + ".local"
}

// Peers returns currently known OpenCouchpad servers (excluding this one).
func (r *Responder) Peers() []Peer {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	out := []Peer{}
	for k, p := range r.peers {
		if now.After(p.expires) {
			delete(r.peers, k)
			continue
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}

func (r *Responder) joinAll() {
	ifs, _ := net.Interfaces()
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(ipv4Addrs(ifi)) == 0 {
			continue
		}
		if err := r.conn.JoinGroup(&ifi, groupAddr); err != nil {
			slog.Debug("mdns: join failed", "iface", ifi.Name, "err", err)
			continue
		}
		r.ifaces = append(r.ifaces, ifi)
	}
}

func ipv4Addrs(ifi net.Interface) []*net.IPNet {
	addrs, _ := ifi.Addrs()
	var out []*net.IPNet
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
			out = append(out, n)
		}
	}
	return out
}

// virtual reports interfaces that are almost never what a phone talks to.
func virtual(name string) bool {
	for _, p := range []string{"docker", "br-", "veth", "virbr", "vmnet", "vboxnet", "tailscale", "zt", "wg", "tun", "lxc", "cni", "flannel", "vEthernet"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// addrsFor picks the address(es) to answer with: the one on the querier's
// subnet when possible, otherwise all non-virtual interface addresses.
func (r *Responder) addrsFor(src net.IP) (net.IP, []net.IP, *net.Interface) {
	var all []net.IP
	for i := range r.ifaces {
		for _, n := range ipv4Addrs(r.ifaces[i]) {
			if src != nil && n.Contains(src) {
				return n.IP.To4(), nil, &r.ifaces[i]
			}
			if !virtual(r.ifaces[i].Name) {
				all = append(all, n.IP.To4())
			}
		}
	}
	return nil, all, nil
}

func (r *Responder) isOwnIP(ip net.IP) bool {
	for _, ifi := range r.ifaces {
		for _, n := range ipv4Addrs(ifi) {
			if n.IP.Equal(ip) {
				return true
			}
		}
	}
	return false
}

func (r *Responder) hostName() dnsmessage.Name {
	r.mu.Lock()
	defer r.mu.Unlock()
	return mustName(r.host + ".local.")
}

func (r *Responder) instanceName() dnsmessage.Name {
	inst := strings.NewReplacer(".", " ", "\\", "").Replace(r.cfg.Instance)
	if len(inst) > 63 {
		inst = inst[:63]
	}
	return mustName(inst + "." + ServiceType)
}

// claim probes for the host name (RFC 6762 §8.1), appending -2, -3... on conflict.
func (r *Responder) claim(ctx context.Context) error {
	base := sanitizeLabel(r.cfg.Host)
	for i := 1; i <= 20; i++ {
		label := base
		if i > 1 {
			label = fmt.Sprintf("%s-%d", base, i)
		}
		r.mu.Lock()
		r.probing = label
		r.conflict = make(chan struct{}, 1)
		ch := r.conflict
		r.mu.Unlock()

		taken := false
		q := mustName(label + ".local.")
		for n := 0; n < 3 && !taken; n++ {
			r.sendQuery(q, dnsmessage.TypeA)
			select {
			case <-ch:
				taken = true
			case <-time.After(250 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if taken {
			slog.Warn("mdns: name already in use on the network, trying next", "name", label+".local")
			continue
		}
		r.mu.Lock()
		r.host, r.probing, r.ready = label, "", true
		r.mu.Unlock()
		return nil
	}
	return errors.New("mdns: could not find a free host name")
}

func sanitizeLabel(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			b.WriteRune(c)
		case c == ' ' || c == '_' || c == '.':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "remote"
	}
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

func (r *Responder) run(ctx context.Context) {
	// Announce twice (RFC 6762 §8.3), then browse with backoff.
	r.announce(ttlHost, ttlService)
	browse := time.NewTimer(100 * time.Millisecond)
	announce2 := time.NewTimer(time.Second)
	interval := time.Second
	for {
		select {
		case <-ctx.Done():
			r.announce(0, 0) // goodbye
			r.conn.Close()
			return
		case <-announce2.C:
			r.announce(ttlHost, ttlService)
		case <-browse.C:
			r.sendQuery(serviceN, dnsmessage.TypePTR)
			browse.Reset(interval)
			if interval < time.Minute {
				interval *= 3
			}
		}
	}
}

func (r *Responder) sendQuery(name dnsmessage.Name, t dnsmessage.Type) {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})
	b.EnableCompression()
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: name, Type: t, Class: dnsmessage.ClassINET})
	msg, err := b.Finish()
	if err != nil {
		return
	}
	for i := range r.ifaces {
		r.sendOn(&r.ifaces[i], msg, groupAddr)
	}
}

func (r *Responder) sendOn(ifi *net.Interface, msg []byte, dst *net.UDPAddr) {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	if ifi != nil {
		r.conn.SetMulticastInterface(ifi)
	}
	if _, err := r.conn.WriteTo(msg, nil, dst); err != nil {
		slog.Debug("mdns: send failed", "err", err)
	}
}

// announce sends unsolicited responses with all our records on every interface.
func (r *Responder) announce(hostTTL, svcTTL uint32) {
	for i := range r.ifaces {
		ips := ipv4Addrs(r.ifaces[i])
		if len(ips) == 0 {
			continue
		}
		ans := r.serviceRecords(nil, svcTTL, hostTTL)
		ans = append(ans, aRecord(r.hostName(), ips[0].IP.To4(), hostTTL))
		if msg, err := buildResponse(0, nil, ans, nil); err == nil {
			r.sendOn(&r.ifaces[i], msg, groupAddr)
		}
	}
}

func aRecord(name dnsmessage.Name, ip net.IP, ttl uint32) dnsmessage.Resource {
	var a [4]byte
	copy(a[:], ip.To4())
	return dnsmessage.Resource{
		Header: dnsmessage.ResourceHeader{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET | cacheFlush, TTL: ttl},
		Body:   &dnsmessage.AResource{A: a},
	}
}

func (r *Responder) txt() []string {
	out := []string{"v=1"}
	keys := make([]string, 0, len(r.cfg.TXT))
	for k := range r.cfg.TXT {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := k + "=" + r.cfg.TXT[k]
		if len(s) > 255 {
			s = s[:255]
		}
		out = append(out, s)
	}
	return out
}

// serviceRecords returns PTR, SRV, TXT (+ A records for all ips).
func (r *Responder) serviceRecords(extra []net.IP, svcTTL, hostTTL uint32) []dnsmessage.Resource {
	inst := r.instanceName()
	host := r.hostName()
	res := []dnsmessage.Resource{
		{Header: dnsmessage.ResourceHeader{Name: serviceN, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET, TTL: svcTTL},
			Body: &dnsmessage.PTRResource{PTR: inst}},
		{Header: dnsmessage.ResourceHeader{Name: inst, Type: dnsmessage.TypeSRV, Class: dnsmessage.ClassINET | cacheFlush, TTL: hostTTL},
			Body: &dnsmessage.SRVResource{Port: uint16(r.cfg.Port), Target: host}},
		{Header: dnsmessage.ResourceHeader{Name: inst, Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET | cacheFlush, TTL: svcTTL},
			Body: &dnsmessage.TXTResource{TXT: r.txt()}},
	}
	for _, e := range extra {
		res = append(res, aRecord(host, e, hostTTL))
	}
	return res
}

func buildResponse(id uint16, q *dnsmessage.Question, answers, additional []dnsmessage.Resource) ([]byte, error) {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, Response: true, Authoritative: true})
	b.EnableCompression()
	if q != nil {
		b.StartQuestions()
		b.Question(*q)
	}
	b.StartAnswers()
	for _, a := range answers {
		if err := addResource(&b, a); err != nil {
			return nil, err
		}
	}
	b.StartAdditionals()
	for _, a := range additional {
		if err := addResource(&b, a); err != nil {
			return nil, err
		}
	}
	return b.Finish()
}

func addResource(b *dnsmessage.Builder, r dnsmessage.Resource) error {
	switch body := r.Body.(type) {
	case *dnsmessage.AResource:
		return b.AResource(r.Header, *body)
	case *dnsmessage.PTRResource:
		return b.PTRResource(r.Header, *body)
	case *dnsmessage.SRVResource:
		return b.SRVResource(r.Header, *body)
	case *dnsmessage.TXTResource:
		return b.TXTResource(r.Header, *body)
	}
	return fmt.Errorf("unsupported resource %T", r.Body)
}

func (r *Responder) readLoop() {
	buf := make([]byte, 9000)
	for {
		n, _, src, err := r.conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		ua, ok := src.(*net.UDPAddr)
		if !ok {
			continue
		}
		var p dnsmessage.Parser
		h, err := p.Start(buf[:n])
		if err != nil {
			continue
		}
		if h.Response {
			r.handleResponse(&p, ua)
		} else {
			r.handleQuery(h, &p, ua)
		}
	}
}

func (r *Responder) handleQuery(h dnsmessage.Header, p *dnsmessage.Parser, src *net.UDPAddr) {
	r.mu.Lock()
	ready := r.ready
	r.mu.Unlock()
	if !ready {
		return
	}
	qs, err := p.AllQuestions()
	if err != nil {
		return
	}
	ip, all, ifi := r.addrsFor(src.IP)
	ips := all
	if ip != nil {
		ips = []net.IP{ip}
	}
	if len(ips) == 0 {
		return
	}
	host := r.hostName()
	inst := r.instanceName()
	legacy := src.Port != 5353

	for _, q := range qs {
		qType, qClass := q.Type, q.Class&^cacheFlush
		unicastWanted := q.Class&cacheFlush != 0 // QU bit
		if qClass != dnsmessage.ClassINET && qClass != dnsmessage.ClassANY {
			continue
		}
		name := strings.ToLower(q.Name.String())
		var answers, extra []dnsmessage.Resource
		switch {
		case name == strings.ToLower(host.String()) && (qType == dnsmessage.TypeA || qType == dnsmessage.TypeALL):
			for _, a := range ips {
				answers = append(answers, aRecord(host, a, ttlHost))
			}
		case name == strings.ToLower(serviceN.String()) && (qType == dnsmessage.TypePTR || qType == dnsmessage.TypeALL):
			recs := r.serviceRecords(ips, ttlService, ttlHost)
			answers, extra = recs[:1], recs[1:]
		case name == strings.ToLower(inst.String()) && (qType == dnsmessage.TypeSRV || qType == dnsmessage.TypeTXT || qType == dnsmessage.TypeALL):
			recs := r.serviceRecords(ips, ttlService, ttlHost)
			answers, extra = recs[1:3], recs[3:]
		case name == strings.ToLower(servicesQ.String()) && qType == dnsmessage.TypePTR:
			answers = []dnsmessage.Resource{{
				Header: dnsmessage.ResourceHeader{Name: servicesQ, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET, TTL: ttlService},
				Body:   &dnsmessage.PTRResource{PTR: serviceN},
			}}
		}
		if len(answers) == 0 {
			continue
		}
		if legacy {
			// One-shot (legacy unicast) query, e.g. from a plain DNS resolver:
			// reply directly, echoing ID and question, with short TTLs.
			for i := range answers {
				answers[i].Header.Class &^= cacheFlush
				answers[i].Header.TTL = 10
			}
			q := q
			if msg, err := buildResponse(h.ID, &q, answers, extra); err == nil {
				r.sendOn(nil, msg, src)
			}
			continue
		}
		msg, err := buildResponse(0, nil, answers, extra)
		if err != nil {
			continue
		}
		if unicastWanted {
			r.sendOn(nil, msg, src)
		}
		if ifi != nil {
			r.sendOn(ifi, msg, groupAddr)
		} else {
			for i := range r.ifaces {
				r.sendOn(&r.ifaces[i], msg, groupAddr)
			}
		}
	}
}

func (r *Responder) handleResponse(p *dnsmessage.Parser, src *net.UDPAddr) {
	if err := p.SkipAllQuestions(); err != nil {
		return
	}
	var recs []dnsmessage.Resource
	for {
		rr, err := p.Answer()
		if err != nil {
			break
		}
		recs = append(recs, rr)
	}
	p.SkipAllAuthorities()
	for {
		rr, err := p.Additional()
		if err != nil {
			break
		}
		recs = append(recs, rr)
	}

	r.mu.Lock()
	probing, conflict := r.probing, r.conflict
	r.mu.Unlock()
	own := r.instanceName().String()

	type srv struct {
		target string
		port   int
		ttl    uint32
	}
	srvs := map[string]srv{}
	txts := map[string]map[string]string{}
	hostIPs := map[string][]net.IP{}
	var ptrs []string
	for _, rr := range recs {
		name := rr.Header.Name.String()
		switch b := rr.Body.(type) {
		case *dnsmessage.AResource:
			ip := net.IP(b.A[:])
			hostIPs[strings.ToLower(name)] = append(hostIPs[strings.ToLower(name)], ip)
			if probing != "" && strings.EqualFold(name, probing+".local.") && !r.isOwnIP(ip) {
				select {
				case conflict <- struct{}{}:
				default:
				}
			}
		case *dnsmessage.PTRResource:
			if strings.EqualFold(name, ServiceType) {
				ptrs = append(ptrs, b.PTR.String())
				if rr.Header.TTL == 0 {
					r.mu.Lock()
					delete(r.peers, b.PTR.String())
					r.mu.Unlock()
				}
			}
		case *dnsmessage.SRVResource:
			srvs[name] = srv{b.Target.String(), int(b.Port), rr.Header.TTL}
		case *dnsmessage.TXTResource:
			m := map[string]string{}
			for _, kv := range b.TXT {
				k, v, _ := strings.Cut(kv, "=")
				m[k] = v
			}
			txts[name] = m
		}
	}
	for name, s := range srvs {
		if !strings.HasSuffix(strings.ToLower(name), ServiceType) || name == own {
			continue
		}
		if s.ttl == 0 {
			r.mu.Lock()
			delete(r.peers, name)
			r.mu.Unlock()
			continue
		}
		// Prefer an address on a physical LAN interface: phones on Wi-Fi
		// can't reach a peer's VPN/Docker address.
		ip, ipVirtual := src.IP, true
		if _, _, ifi := r.addrsFor(src.IP); ifi != nil {
			ipVirtual = virtual(ifi.Name)
		}
		for _, cand := range hostIPs[strings.ToLower(s.target)] {
			if a, _, ifi := r.addrsFor(cand); a != nil && (ipVirtual || !virtual(ifi.Name)) {
				ip, ipVirtual = cand, virtual(ifi.Name)
				if !ipVirtual {
					break
				}
			}
		}
		instance := strings.TrimSuffix(name, "."+ServiceType)
		ttl := s.ttl
		if ttl > 600 {
			ttl = 600
		}
		r.mu.Lock()
		if prev, ok := r.peers[name]; ok && ipVirtual && !prev.virtual {
			ip, ipVirtual = net.ParseIP(prev.IP), false // keep the better address
		}
		r.peers[name] = &Peer{
			virtual:  ipVirtual,
			Instance: instance,
			Host:     strings.TrimSuffix(s.target, "."),
			IP:       ip.String(),
			Port:     s.port,
			TXT:      txts[name],
			expires:  time.Now().Add(time.Duration(ttl) * time.Second),
		}
		r.mu.Unlock()
	}
	// A bare PTR answer without SRV: ask for details.
	for _, inst := range ptrs {
		if _, ok := srvs[inst]; !ok && inst != own {
			r.mu.Lock()
			_, known := r.peers[inst]
			r.mu.Unlock()
			if !known {
				if n, err := dnsmessage.NewName(inst); err == nil {
					r.sendQuery(n, dnsmessage.TypeSRV)
				}
			}
		}
	}
}
