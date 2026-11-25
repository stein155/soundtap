// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package abl

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// fakeHost is a host product on the bus in its own room: it answers polls with its status,
// a datagram it has queued, or the reply to the last query, as a client does.
type fakeHost struct {
	room       byte
	zone, vol  byte
	muted      bool
	discID     uint16 // 0: an old host that does not announce itself
	speakerTyp byte
	out        [][]byte // client frames for its next polls
	got        [][]byte // master frames addressed to it
	lastQuery  byte
}

// newFakeHost is a host in room with the zone and volume its polls report; discID 0 is an
// old host that answers speakerTyp when asked.
func newFakeHost(room, zone, vol byte, discID uint16, speakerTyp byte) *fakeHost {
	return &fakeHost{
		room: room, zone: zone, vol: vol, muted: false, discID: discID, speakerTyp: speakerTyp,
		out: nil, got: nil, lastQuery: 0,
	}
}

func (h *fakeHost) answer(master []byte) []byte {
	if master[1]&0x0f != h.room {
		return nil
	}

	if master[0] != CmdPoll {
		h.got = append(h.got, master)
	}

	if master[0] == CmdQueryInfo {
		h.lastQuery = master[2]
		if h.discID != 0 || master[2] != QuerySpeakerType {
			return nil
		}

		reply := []byte{0x8b, h.room, h.speakerTyp, 0}
		reply[3] = Checksum(reply[:3])

		return reply
	}

	if len(h.out) > 0 {
		f := h.out[0]
		h.out = h.out[1:]

		return f
	}

	v := h.vol
	if h.muted {
		v |= 0x80
	}

	reply := []byte{0x80, h.zone<<4 | h.room, v, 0}
	reply[3] = Checksum(reply[:3])

	return reply
}

// heard is a datagram the adapter broadcast: a discovery query for devices gets the host's
// announcement on its next poll.
func (h *fakeHost) heard(own []byte) {
	d, err := ParseP2PDatagram(own)
	if err != nil || d.DstPort != PortDiscovery || h.discID == 0 {
		return
	}

	if len(d.Data) >= 2 && d.Data[0] == discQuery && d.Data[1] == ClassDevice {
		name := make([]byte, 24)
		copy(name, "HOST")
		msg := append([]byte{discAnnounce, ClassDevice, byte(h.discID >> 8), byte(h.discID), 0}, name...)
		msg = append(msg, 4, 0, 0, 0x04, 0x00)
		f, _ := P2PDatagram(h.room, Broadcast, PortDiscovery, PortDiscovery, msg)
		h.out = append(h.out, f)
	}
}

// fakeLine echoes what is written and puts the addressed host's answer after it.
type fakeLine struct {
	host    *fakeHost
	rx      []byte
	written [][]byte
	corrupt bool // the next echo comes back changed: a collision
	lastPol byte
}

func (l *fakeLine) Write(data []byte) (int, error) {
	l.written = append(l.written, append([]byte(nil), data...))

	echo := append([]byte(nil), data...)
	if l.corrupt {
		echo[0] ^= 0xff
		l.corrupt = false
	}

	l.rx = append(l.rx, echo...)
	switch {
	case data[0] < 0x80:
		l.lastPol = data[1] & 0x0f
		if l.host != nil {
			l.rx = append(l.rx, l.host.answer(data)...)
		}
	case data[0] == 0xe0 && l.host != nil:
		l.host.heard(data)
	default:
		// Another client frame: nobody answers it.
	}

	return len(data), nil
}

// newFakeLine is a quiet line with host on it, nil for none.
func newFakeLine(host *fakeHost) *fakeLine {
	return &fakeLine{host: host, rx: nil, written: nil, corrupt: false, lastPol: 0}
}

func (l *fakeLine) ReadByteWithin(time.Duration) (byte, error) {
	if len(l.rx) == 0 {
		return 0, ErrTimeout
	}

	b := l.rx[0]
	l.rx = l.rx[1:]

	return b, nil
}

type fakeSpeaker struct {
	playing bool
	volume  int
	set     []int
	left    int
	// hook is the speaker's volume hook, which on soundtapd calls Session.SetVolume back.
	hook func(int)
}

func (s *fakeSpeaker) Playing() bool { return s.playing }
func (s *fakeSpeaker) Volume() int   { return s.volume }
func (s *fakeSpeaker) SetVolume(level int) {
	s.volume = level

	s.set = append(s.set, level)
	if s.hook != nil {
		s.hook(level)
	}
}
func (s *fakeSpeaker) HostLeft() { s.left++ }

type rig struct {
	line *fakeLine
	m    *Master
	s    *Session
	sp   *fakeSpeaker
	now  time.Time
}

func newRig(h *fakeHost) *rig {
	bench := &rig{
		line: newFakeLine(h),
		m:    nil,
		s:    nil,
		sp:   &fakeSpeaker{playing: false, volume: 30, set: nil, left: 0, hook: nil},
		now:  time.Unix(1000, 0),
	}
	bench.m = NewMaster(bench.line, nil)
	bench.m.sleep = func(time.Duration) {}
	bench.m.now = func() time.Time { return bench.now }
	bench.s = NewSession(bench.m, bench.sp, slog.New(slog.DiscardHandler))
	bench.s.now = func() time.Time { return bench.now }

	return bench
}

// run gives the session n ticks and cycles, 15 ms apart.
func (r *rig) run(n int) {
	for range n {
		r.s.Tick()
		_ = r.m.Cycle()
		r.now = r.now.Add(15 * time.Millisecond)
	}
}

func commandsTo(h *fakeHost, cmd byte) [][]byte {
	var out [][]byte

	for _, f := range h.got {
		if f[0] == cmd {
			out = append(out, f)
		}
	}

	return out
}

func TestTheMasterFindsAndClassifiesAnAnnouncingHost(t *testing.T) {
	t.Parallel()

	host := newFakeHost(3, ZoneHostLocal, 20, 0x30c0, 0)
	bus := newRig(host)
	bus.run(400)

	if got := bus.s.Host(); got != HostNelson {
		t.Fatalf("host %v, want nelson", got)
	}

	if HostNelson.Product() != "Wave SoundTouch" || HostColin.Product() != "SoundTouch SA-4" {
		t.Fatal("host product names")
	}

	if !bus.s.OwnsVolume() {
		t.Fatal("a known host does not own the volume")
	}
	// The adapter's own announcement went out, without the speed bits.
	var announced bool

	for _, frame := range bus.line.written {
		d, err := ParseP2PDatagram(frame)
		if err == nil {
			if a, ok := ParseAnnouncement(d.Data); ok && a.ID == adapterID {
				announced = a.Capabilities&0x0e00 == 0
			}
		}
	}

	if !announced {
		t.Fatal("no adapter announcement without speed bits")
	}
}

func TestAnOldHostIsAskedItsSpeakerType(t *testing.T) {
	t.Parallel()

	host := newFakeHost(1, ZoneOff, 0, 0, 0x03)
	bus := newRig(host)
	bus.run(400)

	if got := bus.s.Host(); got != HostColin {
		t.Fatalf("host %v, want colin", got)
	}
}

func TestPlayingSelectsTheAdapterAndWakesAnOldHost(t *testing.T) {
	t.Parallel()

	host := newFakeHost(1, ZoneOff, 0, 0, 0x11)
	bus := newRig(host)
	bus.run(400)
	bus.sp.playing = true
	bus.run(20)

	if len(commandsTo(host, CmdKey)) == 0 || commandsTo(host, CmdKey)[0][2] != KeyPower {
		t.Fatalf("an off Stereo JC was not woken with its power key: %x", host.got)
	}

	host.zone = ZoneHostLocal

	bus.run(200)

	sel := commandsTo(host, CmdOnOff)
	if len(sel) == 0 || sel[0][2] != ArgSelectMe {
		t.Fatalf("the adapter was not selected: %x", host.got)
	}

	host.zone = ZoneAdapter
	before := len(commandsTo(host, CmdOnOff))

	bus.run(300)

	if len(commandsTo(host, CmdOnOff)) != before {
		t.Fatal("selected again while the host plays the adapter")
	}
}

func TestVolumeGoesToTheHostAndComesBack(t *testing.T) {
	t.Parallel()

	host := newFakeHost(2, ZoneAdapter, 30, 0x1c00, 0)
	bus := newRig(host)
	bus.run(400)
	bus.s.SetVolume(45)
	bus.run(5)

	att := commandsTo(host, CmdAttenuate)
	if len(att) == 0 || att[len(att)-1][2] != 45 {
		t.Fatalf("attenuation to the CineMate: %x", att)
	}

	bus.now = bus.now.Add(time.Second)
	host.vol = 52

	bus.run(40)

	if len(bus.sp.set) == 0 || bus.sp.set[len(bus.sp.set)-1] != 52 {
		t.Fatalf("the host's volume did not come back: %v", bus.sp.set)
	}
}

// A level the host reported reaches the speaker, whose volume hook hands it back: that
// must not go out to the host again.
func TestTheHostsOwnVolumeIsNotSentBack(t *testing.T) {
	t.Parallel()

	host := newFakeHost(2, ZoneAdapter, 30, 0x1c00, 0)
	bus := newRig(host)
	bus.sp.hook = bus.s.SetVolume
	bus.run(400)

	before := len(commandsTo(host, CmdAttenuate))
	for _, v := range []byte{33, 36, 39} {
		host.vol = v

		bus.run(20)
	}

	if got := bus.sp.set; len(got) < 3 || got[len(got)-1] != 39 {
		t.Fatalf("the host's steps did not all come through: %v", got)
	}

	if att := commandsTo(host, CmdAttenuate)[before:]; len(att) != 0 {
		t.Fatalf("the host's own levels went back to it: %x", att)
	}
}

func TestTheHostLeavingStopsSoundTap(t *testing.T) {
	t.Parallel()

	host := newFakeHost(2, ZoneAdapter, 30, 0x1401, 0)
	bus := newRig(host)
	bus.run(400)
	bus.sp.playing = true
	host.zone = ZoneHostLocal

	bus.run(40)

	if bus.sp.left != 1 {
		t.Fatalf("HostLeft called %d times, want 1", bus.sp.left)
	}
}

func TestTheSA4BlocksVolumeAfterItTurnsOn(t *testing.T) {
	t.Parallel()

	host := newFakeHost(1, ZoneOff, 0, 0, 0x03)
	bus := newRig(host)
	bus.run(400)

	host.zone = ZoneAdapter

	bus.run(40)

	before := len(commandsTo(host, CmdAttenuate))

	bus.s.SetVolume(60)
	bus.run(5)

	if len(commandsTo(host, CmdAttenuate)) != before {
		t.Fatal("volume sent within 8 s of the SA-4 turning on")
	}

	bus.now = bus.now.Add(9 * time.Second)
	bus.s.SetVolume(61)
	bus.run(5)

	att := commandsTo(host, CmdAttenuate)
	if len(att) == before || att[len(att)-1][2] != 100-61 {
		t.Fatalf("SA-4 attenuation after the block: %x", att)
	}
}

func TestACollisionIsReported(t *testing.T) {
	t.Parallel()

	bus := newRig(newFakeHost(1, 0, 0, 0, 0))

	bus.line.corrupt = true

	err := bus.m.Cycle()
	if !errors.Is(err, ErrCollision) {
		t.Fatalf("Cycle = %v, want a collision", err)
	}
}

func TestDatagramsRoundTripAndAvoidTheRepository(t *testing.T) {
	t.Parallel()

	for size := range 60 {
		frame, err := P2PDatagram(OwnRoom, Broadcast, PortDiscovery, PortDiscovery, bytes.Repeat([]byte{7}, size))
		if err != nil || !P2PLengthOK(len(frame)) {
			t.Fatalf("data %d: frame %d bytes, %v", size, len(frame), err)
		}

		d, err := ParseP2PDatagram(frame)
		if err != nil || d.DstPort != PortDiscovery || len(d.Data) < size {
			t.Fatalf("data %d: %+v %v", size, d, err)
		}
	}

	m := NewMaster(newFakeLine(nil), nil)

	err := m.SendDatagram(Broadcast, PortRepository, PortDiscovery, nil)
	if err == nil {
		t.Fatal("a repository datagram was queued")
	}
}

func TestMuteGoesToTheHostAsAUserMute(t *testing.T) {
	t.Parallel()

	host := newFakeHost(2, ZoneAdapter, 30, 0x30c0, 0)
	bus := newRig(host)
	bus.run(400)
	bus.s.SetMute(true)
	bus.s.SetMute(true)
	bus.run(5)
	bus.s.SetMute(false)
	bus.run(5)

	var codes []byte

	for _, frame := range commandsTo(host, CmdAttenuate) {
		if frame[2] >= 0xf8 {
			codes = append(codes, frame[2])
		}
	}

	if !bytes.Equal(codes, []byte{MuteUser, UnmuteUser}) {
		t.Fatalf("mute codes %x, want fd fe", codes)
	}
}

func TestAHostThatComesLaterIsStillFound(t *testing.T) {
	t.Parallel()

	host := newFakeHost(4, ZoneHostLocal, 25, 0x1c00, 0)
	bus := newRig(host)
	saved := bus.line.host
	bus.line.host = nil // the CineMate is off while the adapter starts and announces
	bus.run(100)
	bus.line.host = saved
	bus.run(600)

	if bus.s.Host() != HostTriode {
		t.Fatalf("host %v, want triode once it came on", bus.s.Host())
	}
}

func TestHostsKeepTheirVolumeAndOneHundredStays(t *testing.T) {
	t.Parallel()

	host := newFakeHost(2, ZoneAdapter, 20, 0x30c0, 0)
	bus := newRig(host)
	bus.sp.volume = 70
	bus.run(400)

	if len(commandsTo(host, CmdAttenuate)) != 0 {
		t.Fatalf("the adapter pushed its volume to a Wave: %x", commandsTo(host, CmdAttenuate))
	}

	if bus.sp.volume != 20 {
		t.Fatalf("speaker volume %d, want the Wave's 20", bus.sp.volume)
	}

	bus.sp.volume = 100 // the owner turns it up; soundtapd hands it to the session
	bus.s.SetVolume(100)
	bus.run(5)

	host.vol = 99 // the Wave's echo of 100
	bus.now = bus.now.Add(time.Second)
	bus.run(40)

	if bus.sp.volume != 100 {
		t.Fatalf("volume slid to %d after setting 100", bus.sp.volume)
	}
}
