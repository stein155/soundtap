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
	// announceNameLen is the name field of a device announcement.
	announceNameLen = 24
	// announceMinLen is the shortest device announcement: up to its info length byte.
	announceMinLen = 30
	// announceCapsLen is the info that carries the capability flags.
	announceCapsLen = 4
)

// The session's timing, as stock's ABLServer keeps it.
const (
	oldHostQueryAfter = 2 * time.Second        // an old host is asked its type this long after it was found
	oldHostQueryEvery = 500 * time.Millisecond // and again this often until it answers
	selectHold        = 2 * time.Second        // a selection request is not repeated within this
	colinVolumeBlock  = 8 * time.Second        // the SA-4 ignores volume this long after it turns on
	volumeEchoWindow  = 600 * time.Millisecond // a poll reply's level within this of ours is its echo
)

// DiscoveryQuery asks the rooms for announcements of class.
func DiscoveryQuery(class byte) []byte { return []byte{discQuery, class, 0, 0} }

// AdapterAnnouncement is the adapter's class 2 device-info announcement.
func AdapterAnnouncement(version [4]byte) []byte {
	name := make([]byte, announceNameLen)
	copy(name, "SOUNDTOUCH")

	const infoLen = 0x21

	info := make([]byte, infoLen)
	info[0], info[1] = adapterSourceMask>>byteBits, adapterSourceMask&byteMask
	info[2], info[3] = adapterCapabilities>>byteBits, adapterCapabilities&byteMask
	info[4] = OwnRoom
	copy(info[5:9], version[:])
	copy(info[9:27], "00-00000000-00000")
	copy(info[29:33], "S-TO")

	msg := []byte{discAnnounce, ClassDevice, adapterID >> byteBits, adapterID & byteMask, 0}
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
func ParseAnnouncement(data []byte) (Announcement, bool) {
	if len(data) < announceMinLen || data[0]&0x3f != discAnnounce || data[0]&0xc0 != 0 {
		return Announcement{Class: 0, ID: 0, Name: "", Capabilities: 0}, false
	}

	announcement := Announcement{
		Class: data[1], ID: uint16(data[2])<<byteBits | uint16(data[3]),
		Name:         strings.TrimRight(string(data[5:29]), "\x00 "),
		Capabilities: 0,
	}
	if announcement.Class == ClassDevice && len(data) >= announceMinLen+announceCapsLen &&
		int(data[29]) >= announceCapsLen {
		announcement.Capabilities = uint16(data[32])<<byteBits | uint16(data[33])
	}

	return announcement, true
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
func NewSession(master *Master, spk Speaker, log *slog.Logger) *Session {
	if log == nil {
		log = slog.Default()
	}

	sess := &Session{
		m: master, sp: spk, log: log, now: time.Now,
		OnHost: nil, OnKey: nil, OnSelect: nil, Bluetooth: false,
		mu: sync.Mutex{}, hostRoom: 0, hasHost: false, host: HostUnknown, caps: 0,
		queried: time.Time{}, foundAt: time.Time{}, zone: 0, zoneKnown: false,
		hostVol: -1, sentVol: -1, sentAt: time.Time{}, blockUntil: time.Time{},
		selectedAt: time.Time{}, wakeAt: time.Time{}, announced: false, muted: false,
		muteKnown: false, registered: false, linkUp: false, justStored: false,
	}
	master.Events = sess.Event

	return sess
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

		err := s.m.Cycle()
		if err != nil {
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
	answering := has && slices.Contains(active, room)

	linkUp := answering && !s.linkUp
	if !answering && s.linkUp {
		s.registered = false
	}

	s.linkUp = answering
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
//
//nolint:revive // flag-parameter: a setter, muted is the state it sets
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
func (s *Session) Event(event Event) {
	switch {
	case event.Datagram != nil && event.Datagram.DstPort == PortRemote:
		s.remote(event.Datagram.Data)
	case event.Datagram != nil && event.Datagram.DstPort == PortDiscovery:
		if a, ok := ParseAnnouncement(event.Datagram.Data); ok && a.Class == ClassDevice {
			if host := HostFromDiscovery(a.ID); host != HostUnknown {
				s.setHost(host, event.Room, a.Capabilities)
			}
		}
	case len(event.Query) >= 3 && event.QueryOf == QuerySpeakerType:
		if host := HostFromSpeakerType(event.Query[2]); host != HostUnknown {
			s.setHost(host, event.Room, 0)
		}
	case event.Poll != nil:
		s.poll(event.Room, event.Poll)
	default:
		// Nothing the session follows.
	}
}

// queryOldHost asks an old host (SA-4, Stereo JC), which never announces itself, its speaker
// type: 2 s after it was found, every 500 ms until it answers.
func (s *Session) queryOldHost(now time.Time, room byte) {
	s.mu.Lock()

	need := now.Sub(s.foundAt) > oldHostQueryAfter && now.Sub(s.queried) > oldHostQueryEvery
	if need {
		s.queried = now
	}
	s.mu.Unlock()

	if need {
		_ = s.m.Command(CmdQueryInfo, room, QuerySpeakerType)
	}
}

func (s *Session) discover() {
	for _, msg := range [][]byte{
		DiscoveryQuery(ClassNetwork), AdapterAnnouncement([4]byte{27, 0, '0', '6'}),
		DiscoveryQuery(ClassDevice), DiscoveryQuery(ClassSource),
	} {
		_ = s.m.SendDatagram(Broadcast, PortDiscovery, PortDiscovery, msg)
	}
}

// followPlayback asks the host to select the adapter while SoundTap plays and it plays
// something else; an old host that is off is woken with its power key first.
func (s *Session) followPlayback(now time.Time) {
	s.mu.Lock()
	room, host, zone, known := s.hostRoom, s.host, s.zone, s.zoneKnown
	recent := now.Sub(s.selectedAt) < selectHold
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

func (s *Session) setHost(host Host, room byte, caps uint16) {
	s.mu.Lock()
	changed := s.host != host
	s.host, s.hostRoom, s.hasHost, s.caps = host, room, true, caps
	s.mu.Unlock()

	if changed {
		s.log.Info("abl host", "class", host, "room", room, "capabilities", caps)
		// Every host but the SA-4 keeps its own volume at power-on and the adapter takes it
		// from the poll replies (stock productOwnsInitialVolume); the SA-4 gets the adapter's.
		if !OwnsInitialVolume(host) {
			s.SetVolume(s.sp.Volume())
		}

		if s.OnHost != nil {
			s.OnHost(host)
		}

		s.register(host)
	}
}

// register sends a Wave or CineMate stock's remote-control registration, once: its remote's
// presets and transport keys then come to the adapter.
func (s *Session) register(host Host) {
	if host != HostNelson && host != HostTriode {
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

	s.log.Info("abl remote keys registered", "host", host)
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
		s.sourceSelection(body)
	default:
		// Nothing the adapter answers.
	}
}

// sourceSelection takes the host selecting the adapter, or leaving it, from its own UI.
func (s *Session) sourceSelection(body []byte) {
	const presetBody = 2 // the body's second byte when it names a preset

	preset := 0
	if body[1] == presetBody && len(body) > 4 {
		preset = int(body[4])
	}

	activate := body[3] == rcActivate

	result := byte(rcNotActivated)
	if activate {
		result = rcActivated
	}

	_ = s.m.SendDatagram(Broadcast, PortRemote, PortRemote, rcActivateDoneMsg(result))

	switch {
	case activate && s.OnSelect != nil:
		s.OnSelect(preset)
	case body[3] == rcDeactivate && s.sp.Playing():
		s.sp.HostLeft()
	default:
		// Selected without a handler, or left while nothing played.
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

func (s *Session) poll(room byte, reply *PollReply) {
	now := s.now()
	s.mu.Lock()
	if !s.hasHost || room != s.hostRoom {
		s.mu.Unlock()

		return
	}

	prev, known := s.zone, s.zoneKnown
	s.zone, s.zoneKnown = reply.Zone, true

	level, take := s.hostVolumeLocked(now, reply)
	if known && prev == ZoneOff && reply.Zone != ZoneOff && s.host == HostColin {
		s.blockUntil = now.Add(colinVolumeBlock)
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

	if known && adapterZone(prev) && (reply.Zone == ZoneHostLocal || reply.Zone == ZoneOff) && s.sp.Playing() {
		s.log.Info("abl host left the adapter", "zone", reply.Zone)
		s.sp.HostLeft()
	}
}

// hostVolumeLocked is the host's volume from a poll reply, and whether SoundTap takes it: a
// change the host made, not the echo of one SoundTap sent within stock's 600 ms.
func (s *Session) hostVolumeLocked(now time.Time, reply *PollReply) (int, bool) {
	level := reply.Volume
	if s.host == HostLuke {
		level = volumeMax - reply.Volume
	}
	// What the host reports for the level last sent is its echo, not a change: 100 goes out
	// as 99 to the newer hosts and comes back as 99.
	echo := s.sentVol >= 0 && reply.Volume == int(AttenuationArg(s.host, s.sentVol))
	take := s.host != HostUnknown && !reply.Muted && level != s.hostVol && level != s.sentVol && !echo &&
		now.Sub(s.sentAt) > volumeEchoWindow && level >= 0 && level <= volumeMax
	s.hostVol = level

	return level, take
}

func adapterZone(z byte) bool { return z == ZoneAdapter || z == ZoneAdapter2 }
