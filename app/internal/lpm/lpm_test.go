// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package lpm

import (
	"bytes"
	"encoding/binary"
	"net"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/speaker"
)

func TestLPMActivateSourceFrame(t *testing.T) {
	f := lpmActivateSource(lpmSourceModule)
	if len(f) != lpmFrameLen || f[0] != 0x62 || f[1] != 0x50 {
		t.Fatalf("header % x", f[:4])
	}
	if binary.BigEndian.Uint32(f[4:]) != 9 || !lpmValid(f) {
		t.Fatalf("payload or checksum wrong: % x", f)
	}
	f[10] ^= 1
	if lpmValid(f) {
		t.Fatal("corrupt frame passed the checksum")
	}
}

func TestLPMFramesResync(t *testing.T) {
	a, b := lpmFrame(lpmProcessor, 0xb4, []byte{1, 2, 3}), lpmFrame(lpmProcessor, 0x05, nil)
	stream := append(append([]byte{0xde, 0xad, 0xbe}, a...), b...)
	var ops []byte
	_ = lpmFrames(bytes.NewReader(stream), func(f []byte) { ops = append(ops, f[1]) })
	if !bytes.Equal(ops, []byte{0xb4, 0x05}) {
		t.Fatalf("ops % x", ops)
	}
}

func TestLPMVolumeFrameAndMixerHandOver(t *testing.T) {
	f := lpmSetVolume(130)
	if f[1] != lpmVolume || binary.BigEndian.Uint32(f[4:]) != 100 || !lpmValid(f) {
		t.Fatalf("volume frame % x", f[:8])
	}
	l := New(t.TempDir(), "test")
	if l.MixerLevel(30) != 30 {
		t.Fatal("the mixer gave up the volume before the processor answered")
	}
	l.ownsVolume.Store(true)
	if l.MixerLevel(30) != 100 {
		t.Fatal("the mixer still attenuates once the processor owns the volume")
	}
}

func TestLPMEchoAnswer(t *testing.T) {
	p := make([]byte, 8)
	binary.BigEndian.PutUint32(p[4:], 41)
	f := lpmFrame(lpmProcessor, lpmEcho, p)
	f[0] = lpmProcessor<<4 | lpmModule
	binary.LittleEndian.PutUint32(f[lpmFrameLen-4:], lpmChecksum(f))
	a := lpmEchoAnswer(f)
	if a[0] != 0x62 || a[1] != lpmEchoReply || binary.BigEndian.Uint32(a[8:]) != 42 || !lpmValid(a) {
		t.Fatalf("answer % x", a[:12])
	}
}

func TestLPMVolumeAndSpeakers(t *testing.T) {
	f := make([]byte, lpmFrameLen)
	binary.BigEndian.PutUint32(f[4:], 37)
	if l, m := lpmVolumeOf(f); l != 37 || m {
		t.Fatalf("volume %d muted %v", l, m)
	}
	f[6], f[7] = 4, 2                               // the ST300's bass module while it played
	f[0x2a], f[0x2b], f[0x4e], f[0x4f] = 1, 1, 1, 1 // both rears in a state stock calls down
	if got, want := lpmSpeakers(f), (speaker.Soundbar{BassPaired: true, BassConnected: true, RearsPaired: true}); got != want {
		t.Fatalf("speakers %+v", got)
	}
	f[7], f[0x4f] = 1, 2 // bass module down, right rear up
	if got, want := lpmSpeakers(f), (speaker.Soundbar{BassPaired: true, RearsPaired: true, RearsConnected: true}); got != want {
		t.Fatalf("speakers %+v", got)
	}
	f[6], f[7] = 0, 0 // no bass module: state 0 is not up without a pairing
	if lpmSpeakers(f).BassConnected {
		t.Fatal("unpaired bass module reported connected")
	}
}

func TestLPMPowerSource(t *testing.T) {
	for _, c := range []struct {
		was, now bool
		src      uint32
		ok       bool
	}{{false, true, 0, true}, {true, false, 9, true}, {false, false, 0, false}, {true, true, 0, false}} {
		if s, ok := lpmPowerSource(c.was, c.now); s != c.src || ok != c.ok {
			t.Fatalf("%v->%v: %d %v", c.was, c.now, s, ok)
		}
	}
}

func TestLPMTone(t *testing.T) {
	f := make([]byte, lpmFrameLen)
	for i, v := range []int16{2, -3, 0, 1, -10, -10, -6, -6, 10, 10, 6, 6, 1, 1, 1, 1} {
		binary.BigEndian.PutUint16(f[4+2*i:], uint16(v))
	}
	tone := lpmToneOf(f)
	if tone.Treble != (speaker.ToneControl{Value: -3, Min: -10, Max: 10, Step: 1}) || tone.Centre.Max != 6 || tone.Rear.Value != 1 {
		t.Fatalf("tone %+v", tone)
	}
	g := lpmToneFrame([4]int{2, -3, 0, 1})
	if g[1] != lpmTone || g[0] != 0x62 || !lpmValid(g) || !bytes.Equal(g[4:12], f[4:12]) {
		t.Fatalf("frame % x", g[:16])
	}
	l := New(t.TempDir(), "test")
	var sent []byte
	send := func(_ string, fr []byte) { sent = fr }
	l.send.Store(&send)
	if l.SetTone(0, 0, 0, 0) == nil {
		t.Fatal("set before the processor answered")
	}
	l.now.Store(&tone)
	if err := l.SetTone(11, 0, 0, 0); err == nil || sent != nil {
		t.Fatalf("out of range level accepted: %v", err)
	}
	if err := l.SetTone(2, -3, 0, 1); err != nil || !bytes.Equal(sent, g) {
		t.Fatalf("set: %v", err)
	}
}

func TestLPMHealthAnswer(t *testing.T) {
	f := lpmHealthAnswer(lpmProcessor, "SoundTap 0123456789abcdefghijklmnop")
	if f[0] != 0x62 || f[1] != lpmHealth || !lpmValid(f) {
		t.Fatalf("header % x", f[:4])
	}
	if got := string(f[4:27]); got != "SoundTap 0123456789abcd" || f[27] != 0 {
		t.Fatalf("version %q, terminator %#x", got, f[27])
	}
}

// The frame that rebooted the bar from its console on 2026-11-10.
func TestLPMTapLine(t *testing.T) {
	f := lpmTapLine("rb 0")
	want := []byte{0x62, lpmTapCommand, 0, 1, 'r', 'b', ' ', '0', '\r', 0}
	if !bytes.Equal(f[:len(want)], want) || !lpmValid(f) {
		t.Fatalf("frame % x", f[:12])
	}
}

func TestLPMKeyAndContent(t *testing.T) {
	key := func(code, state uint32) []byte {
		p := make([]byte, 0x14)
		binary.BigEndian.PutUint32(p[0x0c:], code)
		binary.BigEndian.PutUint32(p[0x10:], state)
		return lpmFrame(lpmModule, lpmKey, p)
	}
	for _, c := range []struct {
		code, state uint32
		key         string
		pressed, ok bool
	}{
		{0x43, 0, "PRESET_3", true, true}, {0x43, 3, "PRESET_3", false, true},
		{0x4c, 1, "POWER", true, true}, {0x55, 2, "PLAY", false, true},
		{0x43, 4, "", false, false}, // held: the handler times holds itself
		{0x30, 0, "", true, false},  // a key SoundTap has no use for
	} {
		k, pressed, ok := lpmKeyOf(key(c.code, c.state))
		if k != c.key || pressed != c.pressed || ok != c.ok {
			t.Errorf("key %#x state %d: %q %v %v", c.code, c.state, k, pressed, ok)
		}
	}

	content := func(what byte, n uint32) []byte {
		f := lpmFrame(lpmModule, lpmContent, binary.BigEndian.AppendUint32(nil, n))
		f[2] = what
		return f
	}
	for _, c := range []struct {
		what  byte
		n     uint32
		key   string
		store bool
	}{
		{0, 2, "PRESET_2", false}, {1, 6, "PRESET_6", true}, {0, 7, "", false},
		{3, 0, "STOP", false}, {3, 2, "PAUSE", false}, {3, 4, "NEXT_TRACK", false},
		{3, 6, "", false}, {2, 1, "", false},
	} {
		if k, store := lpmContentOf(content(c.what, c.n)); k != c.key || store != c.store {
			t.Errorf("0x85 %d/%d: %q %v", c.what, c.n, k, store)
		}
	}
	if lpmSourceNames[lpmSourceModule] != "SoundTouch" || lpmSourceNames[lpmSourceCount-1] != "Factory default" {
		t.Fatal("source names out of step with the processor's enum")
	}
}

func TestLPMRadioStatusFrame(t *testing.T) {
	f := lpmRadioStatusFrame(5200, lpmNetStatus(hw.LinkUp))
	if !lpmValid(f) || f[0] != 0x62 || f[1] != lpmRadioStatus {
		t.Fatalf("header % x", f[:2])
	}
	if got := f[4:16]; !bytes.Equal(got, []byte{0, 0, 0x14, 0x50, 0, 0, 0, 0, 2, 0, 1, 0}) {
		t.Fatalf("payload % x", got)
	}
	for l, want := range map[hw.Link]byte{hw.LinkDown: 0, hw.LinkSetupAP: 1, hw.LinkConnecting: 3, hw.LinkUp: 2} {
		if got := lpmNetStatus(l); got != want {
			t.Errorf("lpmNetStatus(%d) = %d, want %d", l, got, want)
		}
	}
}

func TestLPMForwardVolumeSkipsOnlyTheTakeover(t *testing.T) {
	var fromLPM atomic.Int64
	fromLPM.Store(30) // the remote set 30, and SoundTap takes it over
	for _, c := range []struct {
		level   int
		forward bool
	}{{30, false}, {40, true}, {30, true}} {
		if got := lpmForwardVolume(c.level, &fromLPM); got != c.forward {
			t.Errorf("level %d: forward = %v, want %v", c.level, got, c.forward)
		}
	}
}

func TestLPMSavedInputComesBackOnlyForTheBarsOwnInputs(t *testing.T) {
	l := New(t.TempDir(), "test")
	if _, ok := l.SavedInput(); ok {
		t.Fatal("nothing saved yet")
	}
	for source, want := range map[uint32]bool{
		lpmSourceStandby: false, lpmSourceModule: false, lpmSourceBT: false, lpmSourceADAPTiQ: false,
		lpmSourceTV: true, 1: true,
	} {
		if err := l.SaveSource(source); err != nil {
			t.Fatal(err)
		}
		got, ok := l.SavedInput()
		if ok != want || (ok && got != source) {
			t.Errorf("source %d: got %d, %v, want %v", source, got, ok, want)
		}
	}
}

func TestLPMAudioMode(t *testing.T) {
	l := New(t.TempDir(), "test")
	var sent []byte
	send := func(_ string, fr []byte) { sent = fr }
	l.send.Store(&send)
	if l.AudioModes.Get() != "" || l.AudioModes.Set("dialog") == nil {
		t.Fatal("mode known before the first status")
	}
	status := make([]byte, lpmFrameLen)
	status[0x3f] = 4
	l.statusMode(status)
	if m := l.AudioModes.Get(); m != "night" {
		t.Fatalf("status mode %q", m)
	}
	l.alive.Store(true) // the reader sets it on the processor's first frame
	if l.AudioModes.Set("loud") == nil || sent != nil {
		t.Fatal("unknown mode sent")
	}
	if err := l.AudioModes.Set("dialog"); err != nil || sent[1] != lpmAudioMode || sent[0] != 0x62 ||
		!lpmValid(sent) || !bytes.Equal(sent[4:6], []byte{0, 3}) || l.AudioModes.Get() != "dialog" {
		t.Fatalf("set: %v % x", err, sent[:8])
	}
	status[0x3f] = 0
	l.statusMode(status)
	if l.AudioModes.Get() != "dialog" {
		t.Fatal("an unspecified mode in the status overwrote the known one")
	}
}

func TestLPMCECMode(t *testing.T) {
	l := New(t.TempDir(), "test")
	var sent []byte
	send := func(_ string, fr []byte) { sent = fr }
	l.send.Store(&send)
	if l.CECModes.Set("on") == nil {
		t.Fatal("sent before the processor's first status")
	}
	l.alive.Store(true)
	l.CECModes.report(0) // off is a real value here, unlike the audio mode's 0
	if m := l.CECModes.Get(); m != "off" {
		t.Fatalf("reported %q", m)
	}
	l.CECModes.report(9)
	if l.CECModes.Get() != "off" {
		t.Fatal("an out-of-range report was kept")
	}
	if err := l.CECModes.Set("alternate"); err != nil || sent[1] != lpmCECModeSet || sent[0] != 0x62 ||
		!lpmValid(sent) || sent[4] != 2 || sent[5] != 0 || l.CECModes.Get() != "alternate" {
		t.Fatalf("set: %v % x", err, sent[:8])
	}
}

func TestLPMHDMIInput(t *testing.T) {
	l := New(t.TempDir(), "test")
	var sent []byte
	send := func(_ string, fr []byte) { sent = fr }
	l.send.Store(&send)
	l.alive.Store(true)
	l.HDMIInputs.report(0)
	l.HDMIInputs.report(4) // stock refuses 4 and up
	if m := l.HDMIInputs.Get(); m != "none" {
		t.Fatalf("reported %q", m)
	}
	if err := l.HDMIInputs.Set("3"); err != nil || sent[1] != lpmHDMIInputSet || !lpmValid(sent) ||
		sent[4] != 3 || sent[5] != 0 || l.HDMIInputs.Get() != "3" {
		t.Fatalf("set: %v % x", err, sent[:8])
	}
}

// A bar in standby reports nothing, so a restarted module shows the last saved value.
func TestLPMChoiceSurvivesARestart(t *testing.T) {
	l := New(t.TempDir(), "test")
	l.CECModes.Load()
	if l.CECModes.Get() != "" {
		t.Fatal("a value with nothing saved")
	}
	l.CECModes.report(2)
	l.CECModes.cur.Store(0)
	l.CECModes.Load()
	if m := l.CECModes.Get(); m != "alternate" {
		t.Fatalf("after a restart %q", m)
	}
}

func TestLPMLipSync(t *testing.T) {
	l := New(t.TempDir(), "test")
	var sent []byte
	send := func(_ string, fr []byte) { sent = fr }
	l.send.Store(&send)
	if _, ok := l.LipSync(); ok {
		t.Fatal("known before a status")
	}
	status := make([]byte, lpmFrameLen)
	status[0x48], status[0x49] = 0, 40
	l.statusMode(status)
	if ms, ok := l.LipSync(); !ok || ms != 40 {
		t.Fatalf("status said %d %v", ms, ok)
	}
	if l.SetLipSync(20) == nil {
		t.Fatal("sent before the processor answered")
	}
	l.alive.Store(true)
	if l.SetLipSync(LipSyncMax+1) == nil || l.SetLipSync(-1) == nil || sent != nil {
		t.Fatal("out of range sent")
	}
	if err := l.SetLipSync(20); err != nil || sent[1] != lpmLipSyncSet || !lpmValid(sent) ||
		!bytes.Equal(sent[4:8], []byte{0, 0, 0, 20}) {
		t.Fatalf("set: %v % x", err, sent[:8])
	}
	if ms, _ := l.LipSync(); ms != 20 {
		t.Fatalf("after set %d", ms)
	}
}

func TestLPMAfterRecovery(t *testing.T) {
	for in, want := range map[string]bool{
		"console=ttyGS0 variant=ginger soundtap.recovery=-19": true,
		"soundtap.recovery=0 root=ubi0:rootfs":                true,
		"variant=ginger soundtap.nandroot=1":                  false,
		"x.soundtap.recovery=1":                               false,
	} {
		if got := lpmAfterRecovery(in); got != want {
			t.Errorf("lpmAfterRecovery(%q) = %v, want %v", in, got, want)
		}
	}
	if f := lpmFrame(lpmProcessor, lpmUpdateDone, nil); f[0] != 0x62 || f[1] != 0x0c || !bytes.Equal(f[124:], []byte{0x62, 0x0c, 0, 0}) {
		t.Errorf("update-done frame = %x, want stock's gingeripc 1 (62 0c ... 62 0c 00 00)", f)
	}
}

// The app's unmute after a mute from the bar's remote has to lift the processor's mute.
func TestLPMMuteFrameOnlyOnChange(t *testing.T) {
	var last atomic.Int32
	last.Store(1) // the remote muted the bar
	fr := lpmMuteFrame(false, &last)
	if fr == nil || fr[1] != lpmKey || binary.BigEndian.Uint32(fr[8:]) != 4 || binary.BigEndian.Uint32(fr[16:]) != lpmKeyUnmute {
		t.Fatalf("unmute frame = %x, want key frame {0, 4, 0, 0xcd, 0}", fr)
	}
	if lpmMuteFrame(false, &last) != nil {
		t.Fatal("a second unmute was sent again")
	}
	if fr := lpmMuteFrame(true, &last); fr == nil || binary.BigEndian.Uint32(fr[16:]) != lpmKeyMute {
		t.Fatalf("mute frame = %x, want key 0xfe", fr)
	}
}

// The app's HDMI pick follows the remote's HDMI button assignment, HDMI 1 while there is none.
func TestLPMPickInputHDMI(t *testing.T) {
	var got []uint32
	pick := func(source uint32) { got = append(got, source) }
	l := New(t.TempDir(), "test")
	l.pick.Store(&pick)
	l.HDMIInputs.report(0)
	if err := l.PickInput(sourceHDMI); err != nil {
		t.Fatal(err)
	}
	l.HDMIInputs.report(2)
	if err := l.PickInput(sourceHDMI); err != nil {
		t.Fatal(err)
	}
	if err := l.PickInput(sourceTV); err != nil {
		t.Fatal(err)
	}
	if err := l.PickInput(sourceBluetooth); err != nil {
		t.Fatal(err)
	}
	if l.PickInput("AUX") == nil {
		t.Fatal("AUX picked")
	}
	if want := []uint32{lpmSourceHDMI1, 2, lpmSourceTV, lpmSourceBT}; !slices.Equal(got, want) {
		t.Fatalf("picked %v, want %v", got, want)
	}
}

// Bluetooth plays on the bar's Bluetooth input, everything else the module plays on its own.
func TestLPMModuleInput(t *testing.T) {
	if lpmModuleInput(sourceBluetooth) != lpmSourceBT || lpmModuleInput("TUNEIN") != lpmSourceModule {
		t.Fatal("wrong module input")
	}
}

// fakeEth0 is the rear Ethernet as the kernel would report it.
type fakeEth0 struct {
	carrier bool
	addrs   []net.Addr
}

func (e *fakeEth0) state() (bool, []net.Addr) { return e.carrier, e.addrs }

// fakeLPMLink records the frames that would go over the processor link.
type fakeLPMLink struct{ frames [][]byte }

func (l *fakeLPMLink) send(what string, fr []byte) {
	if what != "radio status" || !lpmValid(fr) || fr[1] != lpmRadioStatus {
		panic("unexpected frame " + what)
	}
	l.frames = append(l.frames, fr)
}

// The bar is told it runs on its cable while eth0 has carrier and a lease, and goes back to
// the Wi-Fi status when the cable is pulled. Nothing goes out twice while nothing changes.
func TestLPMRadioReportsTheWiredLink(t *testing.T) {
	eth0 := &fakeEth0{}
	link := &fakeLPMLink{}
	wifi := hw.LinkUp
	r := &lpmRadio{
		mhz:  func() int { return 5200 },
		wifi: func() hw.Link { return wifi },
		eth0: eth0.state,
		send: link.send,
	}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	step := func(want byte, sends bool) {
		t.Helper()
		before := len(link.frames)
		fr, _ := r.tick(now, false)
		now = now.Add(5 * time.Second)
		if !sends {
			if fr != nil || len(link.frames) != before {
				t.Fatalf("sent % x with nothing changed", fr)
			}
			return
		}
		if len(link.frames) != before+1 || fr == nil {
			t.Fatalf("sent %d frames, want 1", len(link.frames)-before)
		}
		if fr[12] != want {
			t.Fatalf("net = %d, want %d", fr[12], want)
		}
	}
	lease := &net.IPNet{IP: net.IPv4(192, 0, 2, 120), Mask: net.CIDRMask(24, 32)}

	step(2, true)  // Wi-Fi connected, no cable
	step(0, false) // unchanged: nothing goes out
	eth0.carrier = true
	step(0, false) // cable in, no lease yet: still Wi-Fi, and nothing new to say
	eth0.addrs = []net.Addr{lease}
	step(lpmNetWired, true) // carrier and an address: wired
	step(0, false)
	step(0, false)
	eth0.carrier = false // cable pulled; the init script drops the address after
	step(2, true)        // back to the Wi-Fi status
	eth0.addrs = nil
	step(0, false)
	wifi = hw.LinkConnecting
	step(3, true)

	// Unchanged, it goes out again once a minute, and at once when the processor asks.
	now = now.Add(lpmRadioRefresh)
	step(3, true)
	if fr, news := r.tick(now, true); fr == nil || !news {
		t.Fatal("the processor asked and got nothing")
	}
}

func TestLPMNetWiredValue(t *testing.T) {
	lease := []net.Addr{&net.IPNet{IP: net.IPv4(10, 0, 0, 2), Mask: net.CIDRMask(8, 32)}}
	for _, c := range []struct {
		carrier bool
		addrs   []net.Addr
		wifi    hw.Link
		want    byte
	}{
		{true, lease, hw.LinkUp, 4},
		{true, lease, hw.LinkSetupAP, 4}, // a cable with a lease wins over the setup AP
		{true, nil, hw.LinkSetupAP, 1},
		{false, lease, hw.LinkDown, 0},
	} {
		if got := lpmNet(c.carrier, c.addrs, c.wifi); got != c.want {
			t.Errorf("lpmNet(%v, %v, %d) = %d, want %d", c.carrier, c.addrs, c.wifi, got, c.want)
		}
	}
}
