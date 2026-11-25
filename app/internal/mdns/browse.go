// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mdns

import (
	"context"
	"errors"
	"fmt"
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

	tbl := &table{self: self, known: map[string]map[string]Peer{}, why: ""}

	tick := time.NewTicker(MaxBrowseDelay)
	defer tick.Stop()

	var last string

	for ctx.Err() == nil {
		err := r.browseSession(ctx, tbl, updates, tick.C)
		if ctx.Err() != nil {
			return
		}

		if now := errText(err); now != last {
			last = now

			r.log.Info("avahi browser stopped", "err", err, "retry", r.restartDelay)
		}

		select {
		case <-ctx.Done():
		case <-time.After(r.restartDelay):
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
func (r *Responder) browseSession(ctx context.Context, tbl *table, updates chan<- Peer, tick <-chan time.Time) error {
	conn, err := sysbus.Dial(ctx, r.bus, callWait)
	if err != nil {
		return fmt.Errorf("mdns: %w", err)
	}
	defer func() { _ = conn.Close() }()
	// Before the browser exists: avahi may report the first instances before the call
	// that made the browser has returned its path.
	err = watchAvahi(ctx, conn, avahiBrowser)
	if err != nil {
		return err
	}

	// The session's context: cancelling it on the way out ends the resolves of this session.
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	brw := &browser{
		r: r, c: conn, t: tbl, updates: updates,
		path: "", pending: nil, gen: 0,
		results: make(chan lookup), slots: make(chan struct{}, maxResolving),
	}
	for {
		err = brw.start(ctx)
		if err != nil {
			return err
		}

		refresh := time.NewTimer(r.browseRefresh)
		err = brw.follow(sctx, refresh.C, tick)
		refresh.Stop()

		if !errors.Is(err, errRefresh) {
			return err
		}

		_ = call(ctx, conn, brw.path, avahiBrowser, "Free").Err
	}
}

var (
	// errRefresh ends one browser's run so the next one starts.
	errRefresh = errors.New("refresh")
	// errBusClosed ends a session whose bus connection went away.
	errBusClosed = errors.New("bus connection closed")
	// errBrowserFailed is avahi's Failure signal for our browser.
	errBrowserFailed = errors.New("avahi browser failed")
)

// itemMinFields is how much of an ItemNew or ItemRemove body item reads: interface,
// protocol and name.
const itemMinFields = 3

// browser is one session's service browser and the resolves it has asked for.
type browser struct {
	r       *Responder
	c       *sysbus.Conn
	t       *table
	updates chan<- Peer

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
	err := call(ctx, b.c, "/", avahiServer, "ServiceBrowserNew", ifaceAny, protoAny, serviceType, anyDomain, noFlags).
		Store(&b.path)
	if err != nil {
		return fmt.Errorf("mdns: new service browser: %w", err)
	}

	b.t.known = map[string]map[string]Peer{}
	b.pending = map[instanceKey]uint64{}
	b.r.note("avahi browser started")

	return nil
}

// follow handles the browser's reports until refresh fires (errRefresh), ctx ends (nil),
// or the bus, avahi or the browser fails. ctx is the session's: it also ends the resolves.
func (b *browser) follow(ctx context.Context, refresh, tick <-chan time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-b.c.Done():
			return errBusClosed
		case sig := <-b.c.Signals():
			err := b.signal(ctx, sig)
			if err != nil {
				return err
			}
		case <-refresh:
			return errRefresh
		case result := <-b.results:
			b.resolved(&result)
		case <-tick:
			peers := b.t.all()
			for i := range peers {
				send(b.updates, &peers[i])
			}
		}
	}
}

// signal handles one signal: avahi leaving ends the session, a report of this browser's
// starts a resolve or departs a peer.
func (b *browser) signal(ctx context.Context, sig *dbus.Signal) error {
	if avahiLeft(sig) {
		return errAvahiGone
	}

	if sig.Path != b.path {
		return nil // a browser of before the refresh
	}

	switch sig.Name {
	case avahiBrowser + ".ItemNew":
		key, ok := item(sig)
		switch {
		case !ok:
			b.r.note("skipped: not an IPv4 instance")
		case len(b.pending) >= maxPeers:
			b.r.note("ignored: too many instances resolving: " + key.name)
		default:
			b.gen++
			b.pending[key] = b.gen
			resolveAsync(ctx, b.c, key, b.gen, b.path, b.slots, b.results)
		}
	case avahiBrowser + ".ItemRemove":
		if key, ok := item(sig); ok {
			delete(b.pending, key)

			gone := b.t.removed(interfaceName(key.iface), key.name)
			for i := range gone {
				send(b.updates, &gone[i])
			}

			b.r.note(b.t.why + ": " + key.name + " on " + interfaceName(key.iface))
		}
	case avahiBrowser + ".Failure":
		return fmt.Errorf("%w: %s", errBrowserFailed, firstString(sig.Body))
	default:
		// The browser's other signals (AllForNow, CacheExhausted) change nothing here.
	}

	return nil
}

// resolved applies a resolve unless the instance was removed, or reported again, since.
func (b *browser) resolved(result *lookup) {
	if result.browser != b.path || b.pending[result.key] != result.gen {
		return
	}

	delete(b.pending, result.key)

	if result.err != nil {
		b.r.note("not resolved: " + result.key.name + ": " + result.err.Error())

		return
	}

	iface := interfaceName(result.key.iface)

	changed := b.t.resolved(iface, result.key.name, result.res, onLink)
	for i := range changed {
		send(b.updates, &changed[i])
	}

	b.r.note(b.t.why + ": " + result.key.name + " on " + iface + " (" + result.res.host + " " +
		result.res.ip.String() + ":" + strconv.Itoa(int(result.res.port)) + ")")
}

// item reads an ItemNew or ItemRemove of an IPv4 instance: interface, protocol, name,
// type, domain, flags.
func item(sig *dbus.Signal) (instanceKey, bool) {
	var key instanceKey
	if len(sig.Body) < itemMinFields {
		return key, false
	}

	iface, _ := sig.Body[0].(int32)
	proto, _ := sig.Body[1].(int32)
	name, ok := sig.Body[2].(string)
	key.iface, key.name = iface, name

	return key, ok && proto == protoIPv4
}

func firstString(body []any) string {
	if len(body) > 0 {
		str, _ := body[0].(string)

		return str
	}

	return ""
}

// resolveAsync resolves key once a slot is free and hands the outcome back.
func resolveAsync(ctx context.Context, conn *sysbus.Conn, key instanceKey, gen uint64, browser dbus.ObjectPath,
	slots chan struct{}, results chan<- lookup,
) {
	safego.Go("mdns.resolve", func() {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}

		res, err := resolveInstance(ctx, conn, key.iface, key.name, serviceType)

		<-slots

		select {
		case results <- lookup{key: key, gen: gen, browser: browser, res: res, err: err}:
		case <-ctx.Done():
		}
	})
}

func send(updates chan<- Peer, peer *Peer) {
	select {
	case updates <- *peer:
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
	api, _ := strconv.Atoi(res.txt["api"])
	peer := Peer{
		Instance: inst, Host: res.host, IP: res.ip, Port: res.port,
		ID: res.txt["id"], Name: res.txt["name"], Model: res.txt["model"], Firmware: res.txt["fw"], API: api,
		Departure: false,
	}
	// The address comes from an A record anyone on the LAN can publish, and the
	// device proxy forwards /api/ requests to it: only an on-link address is taken.
	switch {
	case peer.ID == "":
		t.why = "ignored: no id in TXT"

		return nil
	case peer.ID == t.self:
		t.why = "ignored: this speaker"

		return nil
	case peer.IP == nil || !usable(peer.IP):
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

	t.known[inst][iface] = peer
	t.why = "peer"

	return []Peer{peer}
}

// removed applies an instance gone from one interface.
func (t *table) removed(iface, inst string) []Peer {
	ifaces := t.known[inst]

	peer, ok := ifaces[iface]
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

	peer.Departure = true
	t.why = "departed"

	return []Peer{peer}
}

func (t *table) all() []Peer {
	var out []Peer

	for _, ifaces := range t.known {
		for iface := range ifaces {
			out = append(out, ifaces[iface])

			break
		}
	}

	return out
}

// onLink reports whether ip is a unicast address inside one of this host's IPv4
// networks and not one of its own.
func onLink(addr net.IP) bool {
	if addr.IsLoopback() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}

	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && n.Contains(addr) {
			return !n.IP.Equal(addr)
		}
	}

	return false
}
