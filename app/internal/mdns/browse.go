// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mdns

import (
	"context"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/sysbus"
)

// maxResolving bounds the resolves in flight: each new instance avahi reports is resolved
// on its own, and a flood of names must not become a flood of goroutines.
const maxResolving = 4

// browse follows avahi's service browser and reports peers, re-reporting known ones every
// MaxBrowseDelay so a quiet but healthy speaker never goes stale.
func (r *Responder) browse(ctx context.Context) {
	r.mu.Lock()
	self, updates := r.service.ID, r.peers
	r.mu.Unlock()
	t := &table{self: self, known: map[string]map[string]Peer{}}
	tick := time.NewTicker(MaxBrowseDelay)
	defer tick.Stop()
	var last string
	for ctx.Err() == nil {
		err := r.browseSession(ctx, t, updates, tick.C)
		if ctx.Err() != nil {
			return
		}
		if now := errText(err); now != last {
			last = now
			r.log.Info("avahi browser stopped", "err", err, "retry", restartDelay)
		}
		select {
		case <-ctx.Done():
		case <-time.After(restartDelay):
		}
	}
}

// instanceKey is one instance on one interface, as avahi reports them.
type instanceKey struct {
	iface int32
	name  string
}

// lookup is the outcome of one resolve.
type lookup struct {
	key     instanceKey
	gen     uint64
	browser dbus.ObjectPath
	res     resolved
	err     error
}

// browseSession browses over one bus connection, replacing its browser every
// browseRefresh, until ctx ends or the bus or avahi-daemon goes away.
func (r *Responder) browseSession(ctx context.Context, t *table, updates chan<- Peer, tick <-chan time.Time) error {
	c, err := sysbus.Dial(ctx, r.bus, callWait)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	// Before the browser exists: avahi may report the first instances before the call
	// that made the browser has returned its path.
	if err := watchAvahi(ctx, c, avahiBrowser); err != nil {
		return err
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel() // ends the resolves of this session
	b := &browser{r: r, c: c, t: t, updates: updates, ctx: sctx,
		results: make(chan lookup), slots: make(chan struct{}, maxResolving)}
	for {
		if err := b.start(ctx); err != nil {
			return err
		}
		refresh := time.NewTimer(browseRefresh)
		err := b.follow(ctx, refresh.C, tick)
		refresh.Stop()
		if !errors.Is(err, errRefresh) {
			return err
		}
		_ = call(ctx, c, b.path, avahiBrowser, "Free").Err
	}
}

// errRefresh ends one browser's run so the next one starts.
var errRefresh = errors.New("refresh")

// browser is one session's service browser and the resolves it has asked for.
type browser struct {
	r       *Responder
	c       *sysbus.Conn
	t       *table
	updates chan<- Peer
	ctx     context.Context // the session's: it ends the resolves

	path    dbus.ObjectPath
	pending map[instanceKey]uint64
	gen     uint64
	results chan lookup
	slots   chan struct{}
}

// start makes a new browser. A new browser lists the peers there are now; one that left
// while no browser ran never sends its departure. So the table starts over, or the tick
// keeps reporting a vanished peer as online forever.
func (b *browser) start(ctx context.Context) error {
	if err := call(ctx, b.c, "/", avahiServer, "ServiceBrowserNew", ifaceAny, protoAny, serviceType, anyDomain, noFlags).Store(&b.path); err != nil {
		return err
	}
	b.t.known = map[string]map[string]Peer{}
	b.pending = map[instanceKey]uint64{}
	b.r.note("avahi browser started")
	return nil
}

// follow handles the browser's reports until refresh fires (errRefresh), ctx ends (nil),
// or the bus, avahi or the browser fails.
func (b *browser) follow(ctx context.Context, refresh, tick <-chan time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-b.c.Done():
			return errors.New("bus connection closed")
		case <-refresh:
			return errRefresh
		case s := <-b.c.Signals():
			if err := b.signal(s); err != nil {
				return err
			}
		case l := <-b.results:
			b.resolved(l)
		case <-tick:
			for _, p := range b.t.all() {
				send(b.updates, p)
			}
		}
	}
}

// signal handles one signal: avahi leaving ends the session, a report of this browser's
// starts a resolve or departs a peer.
func (b *browser) signal(s *dbus.Signal) error {
	if avahiLeft(s) {
		return errAvahiGone
	}
	if s.Path != b.path {
		return nil // a browser of before the refresh
	}
	switch s.Name {
	case avahiBrowser + ".ItemNew":
		k, ok := item(s)
		switch {
		case !ok:
			b.r.note("skipped: not an IPv4 instance")
		case len(b.pending) >= maxPeers:
			b.r.note("ignored: too many instances resolving: " + k.name)
		default:
			b.gen++
			b.pending[k] = b.gen
			resolveAsync(b.ctx, b.c, k, b.gen, b.path, b.slots, b.results)
		}
	case avahiBrowser + ".ItemRemove":
		if k, ok := item(s); ok {
			delete(b.pending, k)
			for _, p := range b.t.removed(interfaceName(k.iface), k.name) {
				send(b.updates, p)
			}
			b.r.note(b.t.why + ": " + k.name + " on " + interfaceName(k.iface))
		}
	case avahiBrowser + ".Failure":
		return errors.New("avahi browser failed: " + firstString(s.Body))
	}
	return nil
}

// resolved applies a resolve unless the instance was removed, or reported again, since.
func (b *browser) resolved(l lookup) {
	if l.browser != b.path || b.pending[l.key] != l.gen {
		return
	}
	delete(b.pending, l.key)
	if l.err != nil {
		b.r.note("not resolved: " + l.key.name + ": " + l.err.Error())
		return
	}
	iface := interfaceName(l.key.iface)
	for _, p := range b.t.resolved(iface, l.key.name, l.res, onLink) {
		send(b.updates, p)
	}
	b.r.note(b.t.why + ": " + l.key.name + " on " + iface + " (" + l.res.host + " " +
		l.res.ip.String() + ":" + strconv.Itoa(int(l.res.port)) + ")")
}

// item reads an ItemNew or ItemRemove of an IPv4 instance: interface, protocol, name,
// type, domain, flags.
func item(s *dbus.Signal) (instanceKey, bool) {
	if len(s.Body) < 3 {
		return instanceKey{}, false
	}
	iface, _ := s.Body[0].(int32)
	proto, _ := s.Body[1].(int32)
	name, ok := s.Body[2].(string)
	return instanceKey{iface: iface, name: name}, ok && proto == protoIPv4
}

func firstString(body []any) string {
	if len(body) > 0 {
		s, _ := body[0].(string)
		return s
	}
	return ""
}

// resolveAsync resolves k once a slot is free and hands the outcome back.
func resolveAsync(ctx context.Context, c *sysbus.Conn, k instanceKey, gen uint64, browser dbus.ObjectPath,
	slots chan struct{}, results chan<- lookup) {
	safego.Go("mdns.resolve", func() {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		res, err := resolveService(ctx, c, k.iface, k.name, serviceType)
		<-slots
		select {
		case results <- lookup{key: k, gen: gen, browser: browser, res: res, err: err}:
		case <-ctx.Done():
		}
	})
}

func send(ch chan<- Peer, p Peer) {
	select {
	case ch <- p:
	default: // a slow registry must not stall the browse
	}
}

// table is what avahi has reported: per instance, per interface, because a speaker on two
// interfaces is reported twice and is only gone when both are.
type table struct {
	self  string
	known map[string]map[string]Peer
	why   string // what the last event did, for Recent
}

// resolved applies one resolved instance and returns what changed for the registry.
func (t *table) resolved(iface, inst string, res resolved, usable func(net.IP) bool) []Peer {
	p := Peer{Instance: inst, Host: res.host, IP: res.ip, Port: res.port}
	p.ID, p.Name, p.Model, p.Firmware = res.txt["id"], res.txt["name"], res.txt["model"], res.txt["fw"]
	p.API, _ = strconv.Atoi(res.txt["api"])
	// The address comes from an A record anyone on the LAN can publish, and the
	// device proxy forwards /api/ requests to it: only an on-link address is taken.
	switch {
	case p.ID == "":
		t.why = "ignored: no id in TXT"
		return nil
	case p.ID == t.self:
		t.why = "ignored: this speaker"
		return nil
	case p.IP == nil || !usable(p.IP):
		t.why = "ignored: address not on a local network"
		return nil
	}
	if t.known[inst] == nil {
		if len(t.known) >= maxPeers {
			t.why = "ignored: peer table full"
			return nil
		}
		t.known[inst] = map[string]Peer{}
	}
	t.known[inst][iface] = p
	t.why = "peer"
	return []Peer{p}
}

// removed applies an instance gone from one interface.
func (t *table) removed(iface, inst string) []Peer {
	ifaces := t.known[inst]
	p, ok := ifaces[iface]
	if !ok {
		t.why = "gone, was not known"
		return nil
	}
	delete(ifaces, iface)
	if len(ifaces) > 0 {
		t.why = "gone on " + iface + ", still on another interface"
		return nil
	}
	delete(t.known, inst)
	p.Departure = true
	t.why = "departed"
	return []Peer{p}
}

func (t *table) all() []Peer {
	var out []Peer
	for _, ifaces := range t.known {
		for _, p := range ifaces {
			out = append(out, p)
			break
		}
	}
	return out
}

// onLink reports whether ip is a unicast address inside one of this host's IPv4
// networks and not one of its own.
func onLink(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && n.Contains(ip) {
			return !n.IP.Equal(ip)
		}
	}
	return false
}
