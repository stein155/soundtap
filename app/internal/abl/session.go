// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package abl

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// Discovery message types and classes (port 19, sent to the broadcast room).
const (
	discQuery    = 0x00
	discAnnounce = 0x01

	ClassNetwork = 1
	ClassDevice  = 2
	ClassSource  = 3
)

// The adapter's own device announcement, as stock's ABLServer makes it: device id 0x5000,
// name SOUNDTOUCH, source mask 0x0043. The capability flags leave out the speed bits (0x0E00)
// on purpose: with them a host moves the link to 230400 or 460800 baud, timing the module
// never set for those speeds; the commands this master sends fit 19200.
const (
	adapterID           = 0x5000
	adapterSourceMask   = 0x0043
	adapterCapabilities = 0x000f
)

// DiscoveryQuery asks the rooms for announcements of class.
func DiscoveryQuery(class byte) []byte { return []byte{discQuery, class, 0, 0} }

// AdapterAnnouncement is the adapter's class 2 device-info announcement.
func AdapterAnnouncement(version [4]byte) []byte {
	name := make([]byte, 24)
	copy(name, "SOUNDTOUCH")
	const infoLen = 0x21
	info := make([]byte, infoLen)
	info[0], info[1] = adapterSourceMask>>8, adapterSourceMask&0xff
	info[2], info[3] = adapterCapabilities>>8, adapterCapabilities&0xff
	info[4] = OwnRoom
	copy(info[5:9], version[:])
	copy(info[9:27], "00-00000000-00000")
	copy(info[29:33], "S-TO")
	msg := []byte{discAnnounce, ClassDevice, adapterID >> 8, adapterID & 0xff, 0}
	msg = append(msg, name...)
	msg = append(msg, infoLen)
	return append(msg, info...)
}

// Announcement is a parsed device-info announcement.
type Announcement struct {
	Class        byte
	ID           uint16
	Name         string
	Capabilities uint16
}

// ParseAnnouncement reads a discovery announcement; ok is false for anything else.
func ParseAnnouncement(d []byte) (Announcement, bool) {
	if len(d) < 30 || d[0]&0x3f != discAnnounce || d[0]&0xc0 != 0 {
		return Announcement{}, false
	}
	a := Announcement{Class: d[1], ID: uint16(d[2])<<8 | uint16(d[3]),
		Name: strings.TrimRight(string(d[5:29]), "\x00 ")}
	if a.Class == ClassDevice && len(d) >= 30+4 && int(d[29]) >= 4 {
		a.Capabilities = uint16(d[32])<<8 | uint16(d[33])
	}
	return a, true
}

// Speaker is what the session drives on soundtapd's side.
type Speaker interface {
	// Playing reports whether SoundTap plays something the host should hear.
	Playing() bool
	Volume() int
	SetVolume(level int)
	// HostLeft is called when the host turns to its own source or off while SoundTap played.
	HostLeft()
}

// Session runs the link to the host: it finds and classifies it, selects the adapter as the
// host's input while SoundTap plays, hands the volume to the host and takes the host's
// volume back, as stock's ABLServer does.
type Session struct {
	m   *Master
	sp  Speaker
	log *slog.Logger
	now func() time.Time
	// OnHost, when set, is called with each newly classified host.
	OnHost func(Host)
	// OnKey, when set, takes a key of the host's remote: a soundtapd key, or a preset to play
	// or store. OnSelect takes the host selecting the adapter from its own UI (preset 0 for
	// none). Bluetooth says whether to register the Bluetooth keys (the SM2 adapter).
	OnKey     func(key string, preset int, store bool)
	OnSelect  func(preset int)
	Bluetooth bool

	mu         sync.Mutex
	hostRoom   byte
	hasHost    bool // hostRoom is known
	host       Host
	caps       uint16
	queried    time.Time
	foundAt    time.Time
	zone       byte
	zoneKnown  bool
	hostVol    int
	sentVol    int
	sentAt     time.Time
	blockUntil time.Time // the SA-4 ignores volume for 8 s after it turns on
	selectedAt time.Time
	wakeAt     time.Time
	announced  bool
	muted      bool
	muteKnown  bool
	registered bool
	linkUp     bool // the host room answers polls
	justStored bool // the key that stored a preset comes again on release: not a play
}

// NewSession is a session over m for sp.
func NewSession(m *Master, sp Speaker, log *slog.Logger) *Session {
	if log == nil {
		log = slog.Default()
	}
	s := &Session{m: m, sp: sp, log: log, now: time.Now, sentVol: -1, hostVol: -1}
	m.Events = s.Event
	return s
}

// Host is the host class found so far.
func (s *Session) Host() Host {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.host
}

// OwnsVolume reports whether the host applies the volume: once it is known, the PCM stays at
// full scale (stock VOLUME_FORWARD_ABL).
func (s *Session) OwnsVolume() bool { return s.Host() != HostUnknown }

// Run keeps the link until ctx ends.
func (s *Session) Run(ctx context.Context) {
	s.m.Sync()
	for ctx.Err() == nil {
		s.Tick()
		if err := s.m.Cycle(); err != nil {
			s.log.Debug("abl cycle", "err", err)
		}
	}
}

// Tick does the session's periodic work between bus cycles. Exported for tests.
func (s *Session) Tick() {
	now := s.now()
	active := s.m.Active()
	s.mu.Lock()
	first := !s.announced
	s.announced = true
	room, has, host := s.hostRoom, s.hasHost, s.host
	if !has && len(active) > 0 {
		s.hostRoom, s.hasHost, s.foundAt = active[0], true, now
		room, has = s.hostRoom, true
		s.log.Info("abl host found", "room", room)
	}
	// Stock queries and announces on every link up: a host that came on after us, or came
	// back, hears it then, and a Wave or CineMate is registered again.
	up := has && slices.Contains(active, room)
	linkUp := up && !s.linkUp
	if !up && s.linkUp {
		s.registered = false
	}
	s.linkUp = up
	s.mu.Unlock()
	if first || linkUp {
		s.discover()
	}
	if linkUp {
		s.register(host)
	}
	if has && host == HostUnknown {
		s.queryOldHost(now, room)
	}
	if host != HostUnknown {
		s.followPlayback(now)
	}
}

// queryOldHost asks an old host (SA-4, Stereo JC), which never announces itself, its speaker
// type: 2 s after it was found, every 500 ms until it answers.
func (s *Session) queryOldHost(now time.Time, room byte) {
	s.mu.Lock()
	need := now.Sub(s.foundAt) > 2*time.Second && now.Sub(s.queried) > 500*time.Millisecond
	if need {
		s.queried = now
	}
	s.mu.Unlock()
	if need {
		_ = s.m.Command(CmdQueryInfo, room, QuerySpeakerType)
	}
}

func (s *Session) discover() {
	for _, msg := range [][]byte{DiscoveryQuery(ClassNetwork), AdapterAnnouncement([4]byte{27, 0, '0', '6'}),
		DiscoveryQuery(ClassDevice), DiscoveryQuery(ClassSource)} {
		_ = s.m.SendDatagram(Broadcast, PortDiscovery, PortDiscovery, msg)
	}
}

// followPlayback asks the host to select the adapter while SoundTap plays and it plays
// something else; an old host that is off is woken with its power key first.
func (s *Session) followPlayback(now time.Time) {
	s.mu.Lock()
	room, host, zone, known := s.hostRoom, s.host, s.zone, s.zoneKnown
	recent := now.Sub(s.selectedAt) < 2*time.Second
	waking := now.Sub(s.wakeAt) < time.Second
	s.mu.Unlock()
	if !s.sp.Playing() || !known || zone == ZoneAdapter || zone == ZoneAdapter2 || recent || waking {
		return
	}
	if (host == HostColin || host == HostLuke) && zone == ZoneOff {
		s.mu.Lock()
		s.wakeAt = now
		s.mu.Unlock()
		_ = s.m.Command(CmdKey, room, KeyPower)
		s.log.Info("abl wake host", "host", host)
		return
	}
	s.mu.Lock()
	s.selectedAt = now
	s.mu.Unlock()
	_ = s.m.Command(CmdOnOff, room, ArgSelectMe)
	s.log.Info("abl select adapter on host", "host", host, "zone", zone)
}

// SetVolume hands a volume change on SoundTap to the host.
func (s *Session) SetVolume(level int) {
	s.mu.Lock()
	room, has, host := s.hostRoom, s.hasHost, s.host
	blocked := s.now().Before(s.blockUntil)
	if !has || host == HostUnknown || blocked || level == s.sentVol {
		s.mu.Unlock()
		return
	}
	s.sentVol, s.sentAt = level, s.now()
	s.mu.Unlock()
	_ = s.m.Command(CmdAttenuate, room, AttenuationArg(host, level))
}

// SetMute hands SoundTap's mute to the host as a user mute, as stock's ABLMute does.
func (s *Session) SetMute(muted bool) {
	s.mu.Lock()
	room, has, host := s.hostRoom, s.hasHost, s.host
	changed := !s.muteKnown || s.muted != muted
	s.muted, s.muteKnown = muted, true
	s.mu.Unlock()
	if !has || host == HostUnknown || !changed {
		return
	}
	arg := byte(UnmuteUser)
	if muted {
		arg = MuteUser
	}
	_ = s.m.Command(CmdAttenuate, room, arg)
}

// Event takes what the bus reported.
func (s *Session) Event(e Event) {
	switch {
	case e.Datagram != nil && e.Datagram.DstPort == PortRemote:
		s.remote(e.Datagram.Data)
	case e.Datagram != nil && e.Datagram.DstPort == PortDiscovery:
		if a, ok := ParseAnnouncement(e.Datagram.Data); ok && a.Class == ClassDevice {
			if h := HostFromDiscovery(a.ID); h != HostUnknown {
				s.setHost(h, e.Room, a.Capabilities)
			}
		}
	case len(e.Query) >= 3 && e.QueryOf == QuerySpeakerType:
		if h := HostFromSpeakerType(e.Query[2]); h != HostUnknown {
			s.setHost(h, e.Room, 0)
		}
	case e.Poll != nil:
		s.poll(e.Room, *e.Poll)
	}
}

func (s *Session) setHost(h Host, room byte, caps uint16) {
	s.mu.Lock()
	changed := s.host != h
	s.host, s.hostRoom, s.hasHost, s.caps = h, room, true, caps
	s.mu.Unlock()
	if changed {
		s.log.Info("abl host", "class", h, "room", room, "capabilities", caps)
		// Every host but the SA-4 keeps its own volume at power-on and the adapter takes it
		// from the poll replies (stock productOwnsInitialVolume); the SA-4 gets the adapter's.
		if !OwnsInitialVolume(h) {
			s.SetVolume(s.sp.Volume())
		}
		if s.OnHost != nil {
			s.OnHost(h)
		}
		s.register(h)
	}
}

// register sends a Wave or CineMate stock's remote-control registration, once: its remote's
// presets and transport keys then come to the adapter.
func (s *Session) register(h Host) {
	if h != HostNelson && h != HostTriode {
		return
	}
	s.mu.Lock()
	done := s.registered
	s.registered = true
	s.mu.Unlock()
	if done {
		return
	}
	for _, msg := range RegistrationMessages(s.Bluetooth) {
		_ = s.m.SendDatagram(Broadcast, PortRemote, PortRemote, msg)
	}
	s.log.Info("abl remote keys registered", "host", h)
}

// remote takes a remote-control message from the host.
func (s *Session) remote(d []byte) {
	typ, body, ok := rcMessage(d)
	if !ok {
		return
	}
	switch typ {
	case rcCommand:
		s.command(body[3])
	case rcSourceSelection:
		preset := 0
		if body[1] == 2 && len(body) > 4 {
			preset = int(body[4])
		}
		activate := body[3] == rcActivate
		_ = s.m.SendDatagram(Broadcast, PortRemote, PortRemote, rcActivateDoneMsg(activate))
		switch {
		case activate && s.OnSelect != nil:
			s.OnSelect(preset)
		case body[3] == rcDeactivate && s.sp.Playing():
			s.sp.HostLeft()
		}
	}
}

func (s *Session) command(code byte) {
	key, preset, store := KeyAction(code)
	s.mu.Lock()
	// Only preset keys touch the guard, as stock's: a store, then its release.
	skip := preset > 0 && !store && s.justStored
	if preset > 0 {
		s.justStored = store
	}
	s.mu.Unlock()
	if skip || s.OnKey == nil || key == "" && preset == 0 {
		return
	}
	s.OnKey(key, preset, store)
}

func (s *Session) poll(room byte, r PollReply) {
	now := s.now()
	s.mu.Lock()
	if !s.hasHost || room != s.hostRoom {
		s.mu.Unlock()
		return
	}
	prev, known := s.zone, s.zoneKnown
	s.zone, s.zoneKnown = r.Zone, true
	level, take := s.hostVolumeLocked(now, r)
	if known && prev == ZoneOff && r.Zone != ZoneOff && s.host == HostColin {
		s.blockUntil = now.Add(8 * time.Second)
	}
	if take {
		// Before the speaker hears it: its volume hook calls SetVolume back, which must see
		// the host's own level and send nothing.
		s.sentVol = level
	}
	s.mu.Unlock()
	if take {
		s.sp.SetVolume(level)
	}
	if known && adapterZone(prev) && (r.Zone == ZoneHostLocal || r.Zone == ZoneOff) && s.sp.Playing() {
		s.log.Info("abl host left the adapter", "zone", r.Zone)
		s.sp.HostLeft()
	}
}

// hostVolumeLocked is the host's volume from a poll reply, and whether SoundTap takes it: a
// change the host made, not the echo of one SoundTap sent within stock's 600 ms.
func (s *Session) hostVolumeLocked(now time.Time, r PollReply) (int, bool) {
	level := r.Volume
	if s.host == HostLuke {
		level = 100 - r.Volume
	}
	// What the host reports for the level last sent is its echo, not a change: 100 goes out
	// as 99 to the newer hosts and comes back as 99.
	echo := s.sentVol >= 0 && r.Volume == int(AttenuationArg(s.host, s.sentVol))
	take := s.host != HostUnknown && !r.Muted && level != s.hostVol && level != s.sentVol && !echo &&
		now.Sub(s.sentAt) > 600*time.Millisecond && level >= 0 && level <= 100
	s.hostVol = level
	return level, take
}

func adapterZone(z byte) bool { return z == ZoneAdapter || z == ZoneAdapter2 }
