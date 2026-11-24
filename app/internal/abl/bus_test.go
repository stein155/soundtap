// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package abl

import (
	"bytes"
	"errors"
	"io"
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
		f := []byte{0x8b, h.room, h.speakerTyp}
		return append(f, Checksum(f))
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
	f := []byte{0x80, h.zone<<4 | h.room, v}
	return append(f, Checksum(f))
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

func (l *fakeLine) Write(p []byte) (int, error) {
	l.written = append(l.written, append([]byte(nil), p...))
	echo := append([]byte(nil), p...)
	if l.corrupt {
		echo[0] ^= 0xff
		l.corrupt = false
	}
	l.rx = append(l.rx, echo...)
	switch {
	case p[0] < 0x80:
		l.lastPol = p[1] & 0x0f
		if l.host != nil {
			l.rx = append(l.rx, l.host.answer(p)...)
		}
	case p[0] == 0xe0 && l.host != nil:
		l.host.heard(p)
	}
	return len(p), nil
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
	r := &rig{line: &fakeLine{host: h}, sp: &fakeSpeaker{volume: 30}, now: time.Unix(1000, 0)}
	r.m = NewMaster(r.line, nil)
	r.m.sleep = func(time.Duration) {}
	r.m.now = func() time.Time { return r.now }
	r.s = NewSession(r.m, r.sp, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.s.now = func() time.Time { return r.now }
	return r
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
	h := &fakeHost{room: 3, zone: ZoneHostLocal, vol: 20, discID: 0x30c0}
	r := newRig(h)
	r.run(400)
	if got := r.s.Host(); got != HostNelson {
		t.Fatalf("host %v, want nelson", got)
	}
	if HostNelson.Product() != "Wave SoundTouch" || HostColin.Product() != "SoundTouch SA-4" {
		t.Fatal("host product names")
	}
	if !r.s.OwnsVolume() {
		t.Fatal("a known host does not own the volume")
	}
	// The adapter's own announcement went out, without the speed bits.
	var announced bool
	for _, f := range r.line.written {
		if d, err := ParseP2PDatagram(f); err == nil {
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
	h := &fakeHost{room: 1, zone: ZoneOff, speakerTyp: 0x03}
	r := newRig(h)
	r.run(400)
	if got := r.s.Host(); got != HostColin {
		t.Fatalf("host %v, want colin", got)
	}
}

func TestPlayingSelectsTheAdapterAndWakesAnOldHost(t *testing.T) {
	h := &fakeHost{room: 1, zone: ZoneOff, speakerTyp: 0x11}
	r := newRig(h)
	r.run(400)
	r.sp.playing = true
	r.run(20)
	if len(commandsTo(h, CmdKey)) == 0 || commandsTo(h, CmdKey)[0][2] != KeyPower {
		t.Fatalf("an off Stereo JC was not woken with its power key: %x", h.got)
	}
	h.zone = ZoneHostLocal
	r.run(200)
	sel := commandsTo(h, CmdOnOff)
	if len(sel) == 0 || sel[0][2] != ArgSelectMe {
		t.Fatalf("the adapter was not selected: %x", h.got)
	}
	h.zone = ZoneAdapter
	before := len(commandsTo(h, CmdOnOff))
	r.run(300)
	if len(commandsTo(h, CmdOnOff)) != before {
		t.Fatal("selected again while the host plays the adapter")
	}
}

func TestVolumeGoesToTheHostAndComesBack(t *testing.T) {
	h := &fakeHost{room: 2, zone: ZoneAdapter, vol: 30, discID: 0x1c00}
	r := newRig(h)
	r.run(400)
	r.s.SetVolume(45)
	r.run(5)
	att := commandsTo(h, CmdAttenuate)
	if len(att) == 0 || att[len(att)-1][2] != 45 {
		t.Fatalf("attenuation to the CineMate: %x", att)
	}
	r.now = r.now.Add(time.Second)
	h.vol = 52
	r.run(40)
	if len(r.sp.set) == 0 || r.sp.set[len(r.sp.set)-1] != 52 {
		t.Fatalf("the host's volume did not come back: %v", r.sp.set)
	}
}

// A level the host reported reaches the speaker, whose volume hook hands it back: that
// must not go out to the host again.
func TestTheHostsOwnVolumeIsNotSentBack(t *testing.T) {
	h := &fakeHost{room: 2, zone: ZoneAdapter, vol: 30, discID: 0x1c00}
	r := newRig(h)
	r.sp.hook = r.s.SetVolume
	r.run(400)
	before := len(commandsTo(h, CmdAttenuate))
	for _, v := range []byte{33, 36, 39} {
		h.vol = v
		r.run(20)
	}
	if got := r.sp.set; len(got) < 3 || got[len(got)-1] != 39 {
		t.Fatalf("the host's steps did not all come through: %v", got)
	}
	if att := commandsTo(h, CmdAttenuate)[before:]; len(att) != 0 {
		t.Fatalf("the host's own levels went back to it: %x", att)
	}
}

func TestTheHostLeavingStopsSoundTap(t *testing.T) {
	h := &fakeHost{room: 2, zone: ZoneAdapter, vol: 30, discID: 0x1401}
	r := newRig(h)
	r.run(400)
	r.sp.playing = true
	h.zone = ZoneHostLocal
	r.run(40)
	if r.sp.left != 1 {
		t.Fatalf("HostLeft called %d times, want 1", r.sp.left)
	}
}

func TestTheSA4BlocksVolumeAfterItTurnsOn(t *testing.T) {
	h := &fakeHost{room: 1, zone: ZoneOff, speakerTyp: 0x03}
	r := newRig(h)
	r.run(400)
	h.zone = ZoneAdapter
	r.run(40)
	before := len(commandsTo(h, CmdAttenuate))
	r.s.SetVolume(60)
	r.run(5)
	if len(commandsTo(h, CmdAttenuate)) != before {
		t.Fatal("volume sent within 8 s of the SA-4 turning on")
	}
	r.now = r.now.Add(9 * time.Second)
	r.s.SetVolume(61)
	r.run(5)
	att := commandsTo(h, CmdAttenuate)
	if len(att) == before || att[len(att)-1][2] != 100-61 {
		t.Fatalf("SA-4 attenuation after the block: %x", att)
	}
}

func TestACollisionIsReported(t *testing.T) {
	r := newRig(&fakeHost{room: 1})
	r.line.corrupt = true
	if err := r.m.Cycle(); !errors.Is(err, ErrCollision) {
		t.Fatalf("Cycle = %v, want a collision", err)
	}
}

func TestDatagramsRoundTripAndAvoidTheRepository(t *testing.T) {
	for n := range 60 {
		f, err := P2PDatagram(OwnRoom, Broadcast, PortDiscovery, PortDiscovery, bytes.Repeat([]byte{7}, n))
		if err != nil || !P2PLengthOK(len(f)) {
			t.Fatalf("data %d: frame %d bytes, %v", n, len(f), err)
		}
		d, err := ParseP2PDatagram(f)
		if err != nil || d.DstPort != PortDiscovery || len(d.Data) < n {
			t.Fatalf("data %d: %+v %v", n, d, err)
		}
	}
	m := NewMaster(&fakeLine{}, nil)
	if err := m.SendDatagram(Broadcast, PortRepository, PortDiscovery, nil); err == nil {
		t.Fatal("a repository datagram was queued")
	}
}

func TestMuteGoesToTheHostAsAUserMute(t *testing.T) {
	h := &fakeHost{room: 2, zone: ZoneAdapter, vol: 30, discID: 0x30c0}
	r := newRig(h)
	r.run(400)
	r.s.SetMute(true)
	r.s.SetMute(true)
	r.run(5)
	r.s.SetMute(false)
	r.run(5)
	var codes []byte
	for _, f := range commandsTo(h, CmdAttenuate) {
		if f[2] >= 0xf8 {
			codes = append(codes, f[2])
		}
	}
	if !bytes.Equal(codes, []byte{MuteUser, UnmuteUser}) {
		t.Fatalf("mute codes %x, want fd fe", codes)
	}
}

func TestAHostThatComesLaterIsStillFound(t *testing.T) {
	h := &fakeHost{room: 4, zone: ZoneHostLocal, vol: 25, discID: 0x1c00}
	r := newRig(h)
	saved := r.line.host
	r.line.host = nil // the CineMate is off while the adapter starts and announces
	r.run(100)
	r.line.host = saved
	r.run(600)
	if r.s.Host() != HostTriode {
		t.Fatalf("host %v, want triode once it came on", r.s.Host())
	}
}

func TestHostsKeepTheirVolumeAndOneHundredStays(t *testing.T) {
	h := &fakeHost{room: 2, zone: ZoneAdapter, vol: 20, discID: 0x30c0}
	r := newRig(h)
	r.sp.volume = 70
	r.run(400)
	if len(commandsTo(h, CmdAttenuate)) != 0 {
		t.Fatalf("the adapter pushed its volume to a Wave: %x", commandsTo(h, CmdAttenuate))
	}
	if r.sp.volume != 20 {
		t.Fatalf("speaker volume %d, want the Wave's 20", r.sp.volume)
	}
	r.sp.volume = 100 // the owner turns it up; soundtapd hands it to the session
	r.s.SetVolume(100)
	r.run(5)
	h.vol = 99 // the Wave's echo of 100
	r.now = r.now.Add(time.Second)
	r.run(40)
	if r.sp.volume != 100 {
		t.Fatalf("volume slid to %d after setting 100", r.sp.volume)
	}
}
