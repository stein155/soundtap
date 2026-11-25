// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
)

const (
	// probeDialTimeout bounds the TCP probe of a discovered device.
	probeDialTimeout = 2 * time.Second
	// httpPort is the port a device's HTTP API answers on when it names none.
	httpPort = 80
	// devicesTimeout bounds the reads behind GET /api/devices.
	devicesTimeout = 5 * time.Second
)

// A house has more than one speaker, and until now each one only knew about itself.
// This is the registry behind GET /api/devices: what the mDNS browser found, keyed by
// the identity that survives a rename and a new DHCP lease.
//
// It lives in web rather than in mdns because the browser is one of several things that
// could fill it -- the same shape would come out of a static list or a future cloud
// pairing -- and because keeping the dependency one-way means the web package does not
// have to import a package that opens a multicast socket to be tested.

// Device is one SoundTap speaker on the LAN, as last seen.
type Device struct {
	ID       string
	Name     string
	Model    string
	Firmware string
	IP       string
	Port     uint16
	// Departure marks a TTL-zero goodbye: the speaker said it was going. It is kept in
	// the list and reported offline rather than dropped, so a speaker that reboots does
	// not vanish from the UI and come back as a new entry.
	Departure bool
	// Capabilities is what the speaker can do. Empty for a peer: DNS-SD TXT stays small
	// on purpose and a peer's capabilities come from its own /api/info through the proxy,
	// not from a record that has to fit in a multicast packet.
	Capabilities []string
}

// deviceRecord is a Device plus when it was last heard from.
type deviceRecord struct {
	device Device
	seen   time.Time
	// probing: a takeover check of this entry's address is running.
	probing bool
	// misses counts failed liveness probes in a row, see WatchPeers.
	misses int
	// verified is the base URL that answered /api/info as this id, see peerVerified.
	verified string
}

// deviceStale is how long a speaker stays in the list without being heard from. The
// browser re-queries well inside this, so anything older has really gone: a speaker
// unplugged mid-play never sends a goodbye.
const deviceStale = 5 * time.Minute

// peerMissLimit is how many probes in a row a peer may miss before it is reported
// offline. One or two are a Wi-Fi hiccup; at WatchPeers' 15 s this is 45 s.
const peerMissLimit = 3

// maxDevices bounds the registry: any LAN host can announce ids, and entries otherwise
// leave only when a read sweeps them. A house has a handful of speakers.
const maxDevices = 64

type deviceRegistry struct {
	mu sync.Mutex
	by map[string]*deviceRecord
	// now is time.Now except in tests.
	now func() time.Time
}

func (d *deviceRegistry) clock() time.Time {
	if d.now != nil {
		return d.now()
	}

	return time.Now()
}

// peerIPAllowed reports whether ip may be a peer's address: a private LAN address
// (RFC 1918, or an IPv6 ULA) only. Anyone on the LAN can advertise a peer over mDNS, and
// the proxy forwards the browser's requests to it, so a public address would make this
// speaker a relay to any host on the internet. A variable so that tests, whose fake
// peers listen on loopback, can widen it.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var peerIPAllowed = privatePeerIP

// privatePeerIP is peerIPAllowed as the speaker runs it.
func privatePeerIP(ip net.IP) bool { return ip != nil && ip.IsPrivate() }

// SetDevice records a speaker the discovery layer saw. A speaker with no identity is
// ignored: without one there is nothing a group or a proxy route could refer to later,
// and two such speakers would collide on the empty key.
func (s *Server) SetDevice(dev Device) { //nolint:gocritic // hugeParam: cmd/soundtapd passes the Device by value
	deviceID := strings.TrimSpace(dev.ID)
	if deviceID == "" {
		return
	}

	dev.ID = deviceID
	// The proxy forwards to this address, and a request it makes to loopback passes every
	// loopbackOnly check on this speaker. Only a private LAN address is a peer.
	if dev.IP != "" && !peerIPAllowed(net.ParseIP(dev.IP)) {
		return
	}

	s.devices.mu.Lock()
	defer s.devices.mu.Unlock()

	if s.devices.by == nil {
		s.devices.by = map[string]*deviceRecord{}
	}

	now := s.devices.clock()
	// An id is held by the address that announced it until that entry goes stale or says
	// goodbye. Otherwise any LAN host could announce a real speaker's id and receive the
	// Wi-Fi and MQTT passwords the UI sends "to that speaker".
	if old := s.devices.by[deviceID]; old != nil && heldElsewhere(old, &dev, now) {
		s.takeOverLaterLocked(old, &dev)

		return
	}
	// A departure updates the flag and the timestamp and keeps everything else: the
	// goodbye carries no TXT record, so taking its empty name and model would blank the
	// entry the moment the speaker went offline.
	//
	// Only the address that holds the id may say goodbye for it. Another mDNS instance
	// can carry the same TXT id; its goodbye marked the real speaker departed, and the
	// claimant's next announcement then took the id without the liveness check.
	if old := s.devices.by[deviceID]; old != nil && dev.Departure {
		if old.device.IP == "" || dev.IP == old.device.IP {
			old.device.Departure = true
			old.seen = now
		}

		return
	}

	rec := &deviceRecord{device: dev, seen: now, probing: false, misses: 0, verified: ""}
	// The browser re-reports a peer whether it is there or not, so a report clears nothing.
	if old := s.devices.by[deviceID]; old != nil && old.device.IP == dev.IP {
		rec.misses = old.misses
	}

	if s.devices.by[deviceID] == nil && len(s.devices.by) >= maxDevices {
		s.devices.evictOldestLocked()
	}

	s.devices.by[deviceID] = rec
}

// heldElsewhere reports whether old still holds dev's id at another, live address.
func heldElsewhere(old *deviceRecord, dev *Device, now time.Time) bool {
	return !dev.Departure && !old.device.Departure && old.device.IP != "" && dev.IP != "" &&
		old.device.IP != dev.IP && now.Sub(old.seen) <= deviceStale
}

// takeOverLaterLocked lets a renumbered speaker (new DHCP lease) take its id over, which
// would otherwise stay unreachable until the old entry went stale. The new address takes
// over only when the model matches and the old address no longer answers. s.devices.mu
// is held.
func (s *Server) takeOverLaterLocked(old *deviceRecord, dev *Device) {
	if old.probing || old.device.Model != dev.Model {
		return
	}

	old.probing = true
	prev, next := old.device, *dev

	safego.Go("devices.takeOver", func() { s.takeOverIfGone(&prev, &next) })
}

// evictOldestLocked drops the entry heard from longest ago. d.mu is held.
func (d *deviceRegistry) evictOldestLocked() {
	var oldest string
	for id, rec := range d.by {
		if oldest == "" || rec.seen.Before(d.by[oldest].seen) {
			oldest = id
		}
	}

	delete(d.by, oldest)
}

// peerGone asks a speaker's old address whether it is still there. A variable for tests.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var peerGone = func(dev Device) bool {
	port := dev.Port
	if port == 0 {
		port = 80
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(dev.IP, strconv.Itoa(int(port))), probeDialTimeout)
	if err != nil {
		return true
	}

	_ = conn.Close()

	return false
}

// isGone probes a peer through the hook tests set, or peerGone.
func (s *Server) isGone(dev *Device) bool {
	if s.peerGone != nil {
		return s.peerGone(*dev)
	}

	return peerGone(*dev)
}

// WatchPeers probes each peer every interval. An unplugged speaker sends no goodbye, and
// avahi holds its records for their 75 minute TTL while the browser keeps re-reporting
// them, so discovery alone shows it online all that time. Call once in a goroutine.
func (s *Server) WatchPeers(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.probePeers()
		}
	}
}

// probePeers probes one peer at a time.
// a dead peer costs 2 s, so many dead peers skip ticks; go parallel if that shows.
func (s *Server) probePeers() {
	for _, dev := range s.knownDevices(new(Device)) {
		if dev.IP == "" {
			continue
		}

		gone := s.isGone(&dev)
		s.devices.mu.Lock()
		if rec := s.devices.by[dev.ID]; rec != nil && rec.device.IP == dev.IP {
			if gone {
				rec.misses++
			} else {
				rec.misses = 0
			}
		}
		s.devices.mu.Unlock()
	}
}

// takeOverIfGone moves id to next's address when old's address no longer answers.
func (s *Server) takeOverIfGone(old, next *Device) {
	gone := s.isGone(old)
	s.devices.mu.Lock()
	defer s.devices.mu.Unlock()

	rec := s.devices.by[old.ID]
	if rec == nil {
		return
	}

	rec.probing = false
	if gone && rec.device.IP == old.IP {
		s.devices.by[old.ID] = &deviceRecord{
			device: *next, seen: s.devices.clock(), probing: false, misses: 0, verified: "",
		}
	}
}

// writeDiscoveryDebug is the device registry and what discovery last saw, for a speaker
// with no shell to run avahi-browse on.
func (s *Server) writeDiscoveryDebug(out *strings.Builder) {
	now := s.devices.clock()
	s.devices.mu.Lock()

	ids := make([]string, 0, len(s.devices.by))
	for id := range s.devices.by {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	for _, id := range ids {
		rec := s.devices.by[id]
		d := rec.device
		fmt.Fprintf(out, "peer       : %s %q %s:%d model=%q fw=%q seen %s ago departed=%v misses=%d\n",
			d.ID, d.Name, d.IP, d.Port, d.Model, d.Firmware,
			now.Sub(rec.seen).Round(time.Second), d.Departure, rec.misses)
	}
	s.devices.mu.Unlock()

	if len(ids) == 0 {
		out.WriteString("peer       : none known\n")
	}

	s.mdnsMu.Lock()
	browser, _ := s.mdns.(interface{ Recent() []string })
	s.mdnsMu.Unlock()

	if browser != nil {
		for _, line := range browser.Recent() {
			fmt.Fprintf(out, "browse     : %s\n", line)
		}
	}
}

// devices returns every speaker currently known, this one included, oldest entries
// swept. Sorted by name so a device list does not reshuffle between requests.
func (s *Server) knownDevices(self *Device) []Device {
	now := s.devices.clock()
	s.devices.mu.Lock()

	out := make([]Device, 0, len(s.devices.by)+1)
	for id, rec := range s.devices.by {
		if now.Sub(rec.seen) > deviceStale {
			delete(s.devices.by, id)

			continue
		}

		if self.ID != "" && rec.device.ID == self.ID {
			continue // we answer for ourselves below, with live state
		}

		dev := rec.device
		dev.Departure = dev.Departure || rec.misses >= peerMissLimit
		out = append(out, dev)
	}
	s.devices.mu.Unlock()

	if self.ID != "" {
		out = append(out, *self)
	}

	slices.SortFunc(out, func(a, b Device) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})

	return out
}

// deviceIP is the address the registry holds for id, "" when it is unknown or stale.
func (s *Server) deviceIP(id string) string {
	now := s.devices.clock()
	s.devices.mu.Lock()
	defer s.devices.mu.Unlock()

	if rec := s.devices.by[id]; rec != nil && !rec.device.Departure && now.Sub(rec.seen) <= deviceStale {
		return rec.device.IP
	}

	return ""
}

func (s *Server) selfPort() uint16 {
	if s.port == 0 {
		return httpPort
	}

	return s.port
}

// devicesHandler answers GET /api/devices: every SoundTap speaker this one can see,
// itself first in the sense that it is always present even with no network.
func (s *Server) devicesHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), devicesTimeout)
	defer cancel()

	var self Device

	info, err := s.backend.Info(ctx)
	if err == nil {
		self = Device{
			ID:           s.Identity(info.DeviceID),
			Name:         info.Name,
			Model:        info.Type,
			IP:           info.IP,
			Port:         s.selfPort(),
			Firmware:     s.version,
			Capabilities: dropBluetooth(s.capabilities, "bluetooth"),
			Departure:    false,
		}
	}

	list := s.knownDevices(&self)

	out := make([]map[string]any, 0, len(list))
	for _, dev := range list {
		caps := dev.Capabilities
		if caps == nil {
			caps = []string{}
		}

		out = append(out, map[string]any{
			"capabilities": caps,
			"id":           dev.ID,
			"name":         dev.Name,
			"model":        dev.Model,
			"firmware":     dev.Firmware,
			"ip":           dev.IP,
			"port":         int(dev.Port),
			"self":         self.ID != "" && dev.ID == self.ID,
			"online":       !dev.Departure,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}
