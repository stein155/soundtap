// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package mdns puts the speaker on the LAN through avahi-daemon (ADR 0043): its own
// name (soundtap-<id>.local), the shared name every speaker competes for (soundtap.local),
// the _soundtap._tcp service, and the browse that finds the other speakers.
//
// avahi does the protocol: probing, conflict renames ("keuken" becomes "keuken-2"),
// announcements, goodbyes, caching. This package asks for it over avahi's D-Bus API,
// with godbus (internal/sysbus), making the calls avahi's own tools make:
//
//	Server.SetHostName soundtap-<id>                    the speaker's own name
//	EntryGroup.AddService <name> _soundtap._tcp <port>  the service and its TXT record
//	EntryGroup.AddAddress soundtap.local <ip>           the shared name, no reverse record
//	ServiceBrowserNew _soundtap._tcp, ResolveService    the other speakers
//
// Everything is published over one bus connection; closing it withdraws it all, with
// goodbyes. It is published again when the name or address changes, when avahi's server
// is running again after a host name change, and on a new connection after avahi-daemon
// or the bus comes back.
package mdns

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/godbus/dbus/v5"

	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/sysbus"
)

const serviceType = "_soundtap._tcp"

// MaxBrowseDelay is how often every known peer is reported again. avahi reports a
// speaker once and then stays quiet while it stays up, but the registry behind GET
// /api/devices sweeps out a peer it has not heard from in five minutes (deviceStale in
// internal/web). Anything raised here has to stay comfortably under that sweep.
const MaxBrowseDelay = 2 * time.Minute

// timings are the Responder's retry and refresh periods; a field so a test need not wait.
type timings struct {
	// sharedRetry is how often a speaker that does not hold the shared name tries again,
	// which is the handover time after the holder goes away. On a collision avahi would
	// rename the address record ("soundtap-2.local") and keep that for good, so the claim
	// is withdrawn on a collision and tried again. It is also how often the host name avahi
	// settled on is read back.
	sharedRetry time.Duration
	// restartDelay paces retries of what failed for another reason, typically avahi-daemon
	// not being up yet right after boot.
	restartDelay time.Duration
	// browseRefresh is how long one avahi service browser runs before a fresh one replaces
	// it. avahi resolves a service once, when it appears, and never reports it again: a
	// speaker that rebooted into new firmware without a goodbye kept its old fw= on every
	// other speaker until they restarted (2026-11-11). A new browser lists every peer from
	// avahi's cache again.
	browseRefresh time.Duration
}

// defaultTimings are the periods on a speaker.
func defaultTimings() timings {
	return timings{
		sharedRetry: defaultSharedRetry, restartDelay: defaultRestartDelay, browseRefresh: defaultBrowseRefresh,
	}
}

// The periods on a speaker; timings says what each is for.
const (
	defaultSharedRetry   = 30 * time.Second
	defaultRestartDelay  = 5 * time.Second
	defaultBrowseRefresh = 10 * time.Minute
)

// maxLabel is the longest DNS label in bytes (RFC 1035).
const maxLabel = 63

// maxPeers bounds the browse table: it is filled from unauthenticated multicast, and a
// host flooding instance names must not grow it until a 128 MB speaker runs out.
const maxPeers = 64

// Service describes the DNS-SD TXT payload advertised by a speaker.
type Service struct {
	ID       string
	Model    string
	Firmware string
	API      int
	Port     uint16
}

// Peer is a discovered SoundTap speaker. Departure is true when avahi reports it gone;
// callers should mark the speaker offline rather than discard it.
type Peer struct {
	Instance  string
	Host      string
	IP        net.IP
	Port      uint16
	ID        string
	Name      string
	Model     string
	Firmware  string
	API       int
	Departure bool
}

// Responder publishes this speaker and browses for the others.
type Responder struct {
	timings

	log *slog.Logger
	bus string // D-Bus address of the system bus

	mu      sync.Mutex
	ip      string
	name    string
	host    string // as avahi reports it, e.g. "keuken-2.local"
	service *Service
	peers   chan<- Peer
	shared  string // e.g. "soundtap.local"; "" when off
	changed chan struct{}
	recent  []string // the last browse events and what came of them, for /api/debug
}

// recentLines bounds Recent.
const recentLines = 32

// Recent returns the last browse events with what each did, oldest first. On a speaker
// without a shell this is the only view of why a peer is or is not listed.
func (r *Responder) Recent() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.recent...)
}

// New builds a responder for the given LAN address and speaker name. Run publishes.
func New(ip, name string, log *slog.Logger) *Responder {
	return &Responder{
		timings: defaultTimings(), log: log, bus: sysbus.Address,
		mu: sync.Mutex{}, ip: ip, name: name, host: slug(name) + ".local",
		service: nil, peers: nil, shared: "", changed: make(chan struct{}, 1), recent: nil,
	}
}

// Hostname returns the name avahi last reported for this speaker, e.g. "keuken.local".
func (r *Responder) Hostname() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.host
}

// SetIP re-publishes after the speaker's address changes (a new DHCP lease).
func (r *Responder) SetIP(ip string) {
	r.mu.Lock()

	changed := ip != r.ip && net.ParseIP(ip).To4() != nil
	if changed {
		r.ip = ip
	}
	r.mu.Unlock()

	if changed {
		r.kick()
	}
}

// SetName re-publishes under a new speaker name.
func (r *Responder) SetName(name string) {
	r.mu.Lock()
	r.name = name
	r.mu.Unlock()
	r.kick()
}

// EnableDNSService advertises _soundtap._tcp and browses for it once Run starts.
// Updates are sent without blocking.
func (r *Responder) EnableDNSService(cfg Service, updates chan<- Peer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.service, r.peers = &cfg, updates
}

// EnableSharedName makes this speaker compete for <base>.local next to its own name.
// A lost claim is never renamed: "soundtap-2.local" would be a second private name,
// not a shared one.
func (r *Responder) EnableSharedName(base string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.shared = slug(base) + ".local"
}

// SharedName returns the shared name this speaker competes for, e.g. "soundtap.local", or ""
// when off.
func (r *Responder) SharedName() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.shared
}

// Run publishes until ctx is cancelled.
func (r *Responder) Run(ctx context.Context) error {
	r.mu.Lock()
	browse := r.peers != nil && r.service != nil
	r.mu.Unlock()

	browsing := make(chan struct{})

	if browse {
		safego.Go("mdns.browse", func() { defer close(browsing); r.browse(ctx) })
	} else {
		close(browsing)
	}

	defer func() { <-browsing }() // Run returns once everything it started has stopped

	var last string

	for ctx.Err() == nil {
		err := r.session(ctx)
		if ctx.Err() != nil {
			break
		}
		// avahi-daemon is not up yet after boot, for one: say it once until it changes.
		if now := errText(err); now != last {
			last = now

			r.log.Info("avahi publishing stopped", "err", err, "retry", r.restartDelay)
		}

		select {
		case <-ctx.Done():
		case <-time.After(r.restartDelay):
		}
	}

	return nil
}

func (r *Responder) note(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.recent) == recentLines {
		r.recent = append(r.recent[:0], r.recent[1:]...)
	}

	r.recent = append(r.recent, time.Now().Format("15:04:05")+" "+line)
}

func (r *Responder) kick() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

// session publishes over one bus connection until ctx ends, the bus or avahi-daemon goes
// away, or the connection fails.
func (r *Responder) session(ctx context.Context) error {
	conn, err := sysbus.Dial(ctx, r.bus, callWait)
	if err != nil {
		return fmt.Errorf("mdns: %w", err)
	}
	defer func() { _ = conn.Close() }() // avahi withdraws every group of a closed connection

	err = watchAvahi(ctx, conn, avahiServer, avahiEntryGroup)
	if err != nil {
		return err
	}

	var state int32

	err = call(ctx, conn, "/", avahiServer, "GetState").Store(&state)
	if err != nil {
		return fmt.Errorf("mdns: avahi server state: %w", err)
	}

	pub := newPublisher(r, conn)
	if state == serverRunning {
		pub.publish(ctx)
	}

	tick := time.NewTicker(min(r.restartDelay, r.sharedRetry))
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-conn.Done():
			return errBusClosed
		case <-r.changed:
			pub.withdraw(ctx)

			if state == serverRunning {
				pub.publish(ctx)
			}
		case sig := <-conn.Signals():
			if avahiLeft(sig) {
				return errAvahiGone
			}

			pub.signal(ctx, sig, &state)
		case <-tick.C:
			if state == serverRunning {
				pub.retry(ctx)
			}
		}
	}
}

// group is one avahi entry group of ours.
type group struct {
	path dbus.ObjectPath
	name string // the service instance or the shared name it holds
}

// publisher is what one session has published.
type publisher struct {
	r *Responder
	c *sysbus.Conn

	published bool   // everything below is for the values publish last read
	ip, name  string // as published
	svc       *Service
	shared    string

	hostSet   bool      // avahi took (or already had) our host name
	nextHost  time.Time // when to read back the host name avahi settled on
	service   *group    // nil while not published
	serviceAt time.Time // when to try the service again after a failure
	claim     *group    // the shared name's group; nil while not held
	claimAt   time.Time // when to try the shared name again
	lastClaim string    // what the last claim attempt came to, logged once per change
}

func newPublisher(r *Responder, conn *sysbus.Conn) *publisher {
	return &publisher{
		r: r, c: conn,
		published: false, ip: "", name: "", svc: nil, shared: "",
		hostSet: false, nextHost: time.Time{}, service: nil, serviceAt: time.Time{},
		claim: nil, claimAt: time.Time{}, lastClaim: "",
	}
}

// publish reads the current name, address and service and publishes them.
func (p *publisher) publish(ctx context.Context) {
	p.r.mu.Lock()
	p.ip, p.name, p.shared, p.svc = p.r.ip, p.r.name, p.r.shared, p.r.service
	p.r.mu.Unlock()
	p.published = true
	p.hostSet = false
	p.serviceAt, p.claimAt, p.nextHost = time.Time{}, time.Time{}, time.Time{}
	p.retry(ctx)
}

// retry does whatever is due: the host name, the service, the shared name.
func (p *publisher) retry(ctx context.Context) {
	if !p.published {
		p.publish(ctx)

		return
	}

	now := time.Now()

	if !p.hostSet {
		p.setHostName(ctx)
	}

	if p.hostSet && !now.Before(p.nextHost) {
		p.readHostName(ctx)
		p.nextHost = now.Add(p.r.sharedRetry)
	}

	if p.svc != nil && p.service == nil && !now.Before(p.serviceAt) {
		p.addService(ctx, instance(p.name))
	}

	if p.shared != "" && p.ip != "" && p.claim == nil && !now.Before(p.claimAt) {
		p.claimShared(ctx)
	}
}

// setHostName gives avahi our host name. "Redundant" means avahi already has it, which is
// what was asked for.
func (p *publisher) setHostName(ctx context.Context) {
	label := hostLabel(p.name, p.svc)

	err := call(ctx, p.c, "/", avahiServer, "SetHostName", label).Err
	if err != nil && sysbus.ErrorName(err) != "org.freedesktop.Avahi.NoChangeError" &&
		!strings.Contains(err.Error(), "redundant") {
		if ctx.Err() == nil {
			p.r.log.Warn("avahi SetHostName", "name", label, "err", err)
		}

		return
	}

	p.hostSet = true
}

// readHostName keeps Hostname current with what avahi chose: on a conflict avahi picks
// "<slug>-2" by itself, some time after the set.
func (p *publisher) readHostName(ctx context.Context) {
	var host string

	err := call(ctx, p.c, "/", avahiServer, "GetHostNameFqdn").Store(&host)
	if err == nil && host != "" {
		p.r.mu.Lock()
		p.r.host = host
		p.r.mu.Unlock()
	}
}

// newGroup makes an entry group, adds to it with add and commits it.
func (p *publisher) newGroup(ctx context.Context, name string, add func(dbus.ObjectPath) error) (*group, error) {
	var path dbus.ObjectPath

	err := call(ctx, p.c, "/", avahiServer, "EntryGroupNew").Store(&path)
	if err != nil {
		return nil, fmt.Errorf("mdns: new entry group: %w", err)
	}

	grp := &group{path: path, name: name}

	err = add(path)
	if err != nil {
		p.free(ctx, grp)

		return nil, err
	}

	err = call(ctx, p.c, path, avahiEntryGroup, "Commit").Err
	if err != nil {
		p.free(ctx, grp)

		return nil, err
	}

	return grp, nil
}

func (p *publisher) free(ctx context.Context, grp *group) {
	if grp != nil {
		_ = call(ctx, p.c, grp.path, avahiEntryGroup, "Free").Err
	}
}

func (p *publisher) addServiceTo(ctx context.Context, path dbus.ObjectPath, inst string) error {
	return call(ctx, p.c, path, avahiEntryGroup, "AddService", ifaceAny, protoAny, noFlags,
		inst, serviceType, anyDomain, ownHost, p.svc.Port, txtRecord(p.name, *p.svc)).Err
}

// addService publishes _soundtap._tcp under inst.
func (p *publisher) addService(ctx context.Context, inst string) {
	grp, err := p.newGroup(ctx, inst, func(path dbus.ObjectPath) error { return p.addServiceTo(ctx, path, inst) })
	if err != nil {
		if ctx.Err() == nil {
			p.r.log.Info("avahi service not published", "name", inst, "err", err, "retry", p.r.restartDelay)
		}

		p.serviceAt = time.Now().Add(p.r.restartDelay)

		return
	}

	p.service = grp
}

// claimShared publishes the shared name for our address while nobody else answers for it.
func (p *publisher) claimShared(ctx context.Context) {
	p.claimAt = time.Now().Add(p.r.sharedRetry)
	if !p.sharedFree(ctx) {
		p.claimNote("held by another speaker")

		return
	}

	grp, err := p.newGroup(ctx, p.shared, func(path dbus.ObjectPath) error {
		return call(ctx, p.c, path, avahiEntryGroup, "AddAddress", ifaceAny, protoAny, noReverse, p.shared, p.ip).Err
	})
	if err != nil {
		p.claimNote("not published: " + err.Error())

		return
	}

	p.claim = grp
}

// claimNote logs what a claim came to, once until that changes: a shared name another
// speaker holds is refused on every retry, all night, and at 30 s a line it pushed
// everything else out of the log /api/debug shows.
func (p *publisher) claimNote(what string) {
	if what != p.lastClaim {
		p.lastClaim = what
		p.r.log.Info("avahi shared name", "name", p.shared, "result", what, "retry", p.r.sharedRetry,
			"repeats", "logged once until the result changes")
	}
}

// sharedFree says whether this speaker may claim the shared name: nobody else answers for
// it. A claim over the holder probes the name; on Wi-Fi a probe that misses the holder's
// answer "wins", both then see the collision and both stop, and the name was gone for up
// to sharedRetry several times an hour (2026-10-23, all three speakers). A holder that
// goes away is taken over once avahi's cached record for it expires (about 2 minutes).
func (p *publisher) sharedFree(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, resolveCap)
	defer cancel()

	var (
		iface, proto, aproto int32
		name, addr           string
		flags                uint32
	)
	// No answer is the free case, and so is a failed call, as an empty answer was.
	err := p.c.Object(avahiName, "/").CallWithContext(ctx, avahiServer+".ResolveHostName", 0,
		ifaceAny, protoAny, p.shared, protoIPv4, noFlags).Store(&iface, &proto, &name, &aproto, &addr, &flags)

	return err != nil || net.ParseIP(addr) == nil || addr == p.ip
}

// signal follows avahi's server state, kept in *state, and its verdicts on our groups.
func (p *publisher) signal(ctx context.Context, sig *dbus.Signal, state *int32) {
	switch {
	case sig.Name == avahiServer+".StateChanged" && len(sig.Body) >= 1:
		*state, _ = sig.Body[0].(int32)
		p.serverState(ctx, *state)
	case sig.Name == avahiEntryGroup+".StateChanged" && len(sig.Body) >= 2:
		groupState, _ := sig.Body[0].(int32)
		text, _ := sig.Body[1].(string)
		p.groupState(ctx, sig.Path, groupState, text)
	default:
		// Other signals of avahi's server and groups change nothing here.
	}
}

// groupState follows avahi's verdict on one of our groups.
func (p *publisher) groupState(ctx context.Context, path dbus.ObjectPath, state int32, text string) {
	switch {
	case p.service != nil && path == p.service.path:
		p.serviceState(ctx, path, state, text)
	case p.claim != nil && path == p.claim.path:
		p.claimState(ctx, state)
	default:
		// A group of before a withdraw: already freed.
	}
}

// serviceState follows avahi's verdict on our service's group at path.
func (p *publisher) serviceState(ctx context.Context, path dbus.ObjectPath, state int32, text string) {
	switch state {
	case groupCollision:
		// Another host has the instance name: take avahi's alternative ("Keuken #2"),
		// as avahi-publish does.
		var alt string

		err := call(ctx, p.c, "/", avahiServer, "GetAlternativeServiceName", p.service.name).Store(&alt)
		if err != nil {
			alt = p.service.name + " #2"
		}

		p.r.log.Info("avahi service name collision", "name", p.service.name, "now", alt)

		p.service.name = alt
		if call(ctx, p.c, path, avahiEntryGroup, "Reset").Err != nil ||
			p.addServiceTo(ctx, path, alt) != nil ||
			call(ctx, p.c, path, avahiEntryGroup, "Commit").Err != nil {
			p.free(ctx, p.service)
			p.service, p.serviceAt = nil, time.Now().Add(p.r.restartDelay)
		}
	case groupFailure:
		p.r.log.Info("avahi service failed", "name", p.service.name, "err", text, "retry", p.r.restartDelay)
		p.free(ctx, p.service)
		p.service, p.serviceAt = nil, time.Now().Add(p.r.restartDelay)
	default:
		// Uncommitted, registering, established: nothing to do.
	}
}

// claimState follows avahi's verdict on the shared name's group.
func (p *publisher) claimState(ctx context.Context, state int32) {
	switch state {
	case groupEstablished:
		p.claimNote("held")
	case groupCollision, groupFailure:
		// Never renamed: withdrawn, and tried again after sharedRetry.
		p.free(ctx, p.claim)
		p.claim, p.claimAt = nil, time.Now().Add(p.r.sharedRetry)
		p.claimNote("name collision")
	default:
		// Uncommitted or registering: wait for the verdict.
	}
}

// serverState follows avahi's server: its records are re-established after a host name
// change or conflict, and ours are published again once it runs.
func (p *publisher) serverState(ctx context.Context, state int32) {
	switch state {
	case serverRunning:
		p.publish(ctx)
	case serverRegistering, serverCollision:
		p.withdraw(ctx)
	default:
		// Invalid or failure: the session ends when avahi leaves the bus.
	}
}

// withdraw frees every group, with goodbyes.
func (p *publisher) withdraw(ctx context.Context) {
	p.free(ctx, p.service)
	p.free(ctx, p.claim)
	p.service, p.claim = nil, nil
	p.published = false
}

// instance is the DNS-SD instance name: the friendly name, at most one 63-byte label.
func instance(name string) string {
	name = strings.TrimSpace(strings.TrimSuffix(name, "."))
	if name == "" {
		return "SoundTap"
	}

	for len(name) > maxLabel {
		_, size := utf8.DecodeLastRuneInString(name) // cut whole characters, never mid-rune
		name = name[:len(name)-size]
	}

	return name
}

// hostLabel is avahi's host name. People open the shared soundtap.local and nothing else
// (ADR 0043), so the own name is derived from the device id. It never invites anyone to
// type it, and a rename does not change it.
func hostLabel(name string, svc *Service) string {
	if svc != nil && svc.ID != "" {
		return slug("soundtap-" + svc.ID)
	}

	return slug(name)
}

// slug turns a speaker name into a single safe DNS label: lowercase ASCII
// letters/digits, spaces and underscores become single dashes, anything else is
// dropped. Empty input falls back to "soundtap".
func slug(name string) string {
	var label strings.Builder

	dash := false

	for _, char := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			label.WriteRune(char)

			dash = false
		case char == ' ' || char == '-' || char == '_':
			if !dash && label.Len() > 0 {
				label.WriteByte('-')

				dash = true
			}
		default:
			// Anything else is dropped.
		}
	}

	out := strings.Trim(label.String(), "-")
	if out == "" {
		return "soundtap"
	}

	if len(out) > maxLabel {
		out = strings.Trim(out[:maxLabel], "-")
	}

	return out
}
