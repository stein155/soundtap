// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package lpm

import (
	"bytes"
	"encoding/binary"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func TestLPMActivateSourceFrame(t *testing.T) {
	t.Parallel()

	frame := lpmActivateSource(lpmSourceModule)
	if len(frame) != lpmFrameLen || frame[0] != 0x62 || frame[1] != 0x50 {
		t.Fatalf("header % x", frame[:4])
	}

	if binary.BigEndian.Uint32(frame[4:]) != 9 || !lpmValid(frame) {
		t.Fatalf("payload or checksum wrong: % x", frame)
	}

	frame[10] ^= 1
	if lpmValid(frame) {
		t.Fatal("corrupt frame passed the checksum")
	}
}

func TestLPMFramesResync(t *testing.T) {
	t.Parallel()

	a, b := lpmFrame(lpmProcessor, 0xb4, []byte{1, 2, 3}), lpmFrame(lpmProcessor, 0x05, nil)
	stream := append(append([]byte{0xde, 0xad, 0xbe}, a...), b...)

	var ops []byte

	_ = lpmFrames(bytes.NewReader(stream), func(frame []byte) { ops = append(ops, frame[1]) })

	if !bytes.Equal(ops, []byte{0xb4, 0x05}) {
		t.Fatalf("ops % x", ops)
	}
}

func TestLPMVolumeFrameAndMixerHandOver(t *testing.T) {
	t.Parallel()

	frame := lpmSetVolume(130)
	if frame[1] != lpmVolume || binary.BigEndian.Uint32(frame[4:]) != 100 || !lpmValid(frame) {
		t.Fatalf("volume frame % x", frame[:8])
	}

	link := New(t.TempDir(), "test")
	if link.MixerLevel(30) != 30 {
		t.Fatal("the mixer gave up the volume before the processor answered")
	}

	link.ownsVolume.Store(true)

	if link.MixerLevel(30) != 100 {
		t.Fatal("the mixer still attenuates once the processor owns the volume")
	}
}

func TestLPMEchoAnswer(t *testing.T) {
	t.Parallel()

	p := make([]byte, 8)
	binary.BigEndian.PutUint32(p[4:], 41)
	frame := lpmFrame(lpmProcessor, lpmEcho, p)
	frame[0] = lpmProcessor<<4 | lpmModule
	binary.LittleEndian.PutUint32(frame[lpmFrameLen-4:], lpmChecksum(frame))

	a := lpmEchoAnswer(frame)
	if a[0] != 0x62 || a[1] != lpmEchoReply || binary.BigEndian.Uint32(a[8:]) != 42 || !lpmValid(a) {
		t.Fatalf("answer % x", a[:12])
	}
}

func TestLPMVolumeAndSpeakers(t *testing.T) {
	t.Parallel()

	frame := make([]byte, lpmFrameLen)
	binary.BigEndian.PutUint32(frame[4:], 37)

	if level, muted := lpmVolumeOf(frame); level != 37 || muted {
		t.Fatalf("volume %d muted %v", level, muted)
	}

	frame[6], frame[7] = 4, 2 // the ST300's bass module while it played

	frame[0x2a], frame[0x2b], frame[0x4e], frame[0x4f] = 1, 1, 1, 1 // both rears in a state stock calls down

	want := speaker.Soundbar{BassPaired: true, BassConnected: true, RearsPaired: true, RearsConnected: false}
	if got := lpmSpeakers(frame); got != want {
		t.Fatalf("speakers %+v", got)
	}

	frame[7], frame[0x4f] = 1, 2 // bass module down, right rear up

	want = speaker.Soundbar{BassPaired: true, BassConnected: false, RearsPaired: true, RearsConnected: true}
	if got := lpmSpeakers(frame); got != want {
		t.Fatalf("speakers %+v", got)
	}

	frame[6], frame[7] = 0, 0 // no bass module: state 0 is not up without a pairing
	if lpmSpeakers(frame).BassConnected {
		t.Fatal("unpaired bass module reported connected")
	}
}

func TestLPMPowerSource(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		was, now bool
		src      uint32
		ok       bool
	}{{false, true, 0, true}, {true, false, 9, true}, {false, false, 0, false}, {true, true, 0, false}} {
		if s, ok := lpmPowerSource(test.was, test.now); s != test.src || ok != test.ok {
			t.Fatalf("%v->%v: %d %v", test.was, test.now, s, ok)
		}
	}
}

func TestLPMTone(t *testing.T) {
	t.Parallel()

	frame := make([]byte, lpmFrameLen)
	for i, v := range []int16{2, -3, 0, 1, -10, -10, -6, -6, 10, 10, 6, 6, 1, 1, 1, 1} {
		binary.BigEndian.PutUint16(frame[4+2*i:], uint16(v))
	}

	tone := lpmToneOf(frame)
	treble := speaker.ToneControl{Value: -3, Min: -10, Max: 10, Step: 1}

	if tone.Treble != treble || tone.Centre.Max != 6 || tone.Rear.Value != 1 {
		t.Fatalf("tone %+v", tone)
	}

	toneFrame := lpmToneFrame([4]int{2, -3, 0, 1})
	if toneFrame[1] != lpmTone || toneFrame[0] != 0x62 || !lpmValid(toneFrame) ||
		!bytes.Equal(toneFrame[4:12], frame[4:12]) {
		t.Fatalf("frame % x", toneFrame[:16])
	}

	link := New(t.TempDir(), "test")

	var sent []byte

	send := func(_ string, frame []byte) { sent = frame }
	link.send.Store(&send)

	if link.SetTone(0, 0, 0, 0) == nil {
		t.Fatal("set before the processor answered")
	}

	link.now.Store(&tone)

	err := link.SetTone(11, 0, 0, 0)
	if err == nil || sent != nil {
		t.Fatalf("out of range level accepted: %v", err)
	}

	err = link.SetTone(2, -3, 0, 1)
	if err != nil || !bytes.Equal(sent, toneFrame) {
		t.Fatalf("set: %v", err)
	}
}

func TestLPMHealthAnswer(t *testing.T) {
	t.Parallel()

	frame := lpmHealthAnswer(lpmProcessor, "SoundTap 0123456789abcdefghijklmnop")
	if frame[0] != 0x62 || frame[1] != lpmHealth || !lpmValid(frame) {
		t.Fatalf("header % x", frame[:4])
	}

	if got := string(frame[4:27]); got != "SoundTap 0123456789abcd" || frame[27] != 0 {
		t.Fatalf("version %q, terminator %#x", got, frame[27])
	}
}

func TestLPMKeyAndContent(t *testing.T) {
	t.Parallel()

	key := func(code, state uint32) []byte {
		p := make([]byte, 0x14)
		binary.BigEndian.PutUint32(p[0x0c:], code)
		binary.BigEndian.PutUint32(p[0x10:], state)

		return lpmFrame(lpmModule, lpmKey, p)
	}
	for _, test := range []struct {
		code, state uint32
		key         string
		pressed, ok bool
	}{
		{0x43, 0, "PRESET_3", true, true},
		{0x43, 3, "PRESET_3", false, true},
		{0x4c, 1, "POWER", true, true},
		{0x55, 2, "PLAY", false, true},
		{0x43, 4, "", false, false}, // held: the handler times holds itself
		{0x30, 0, "", true, false},  // a key SoundTap has no use for
	} {
		k, pressed, ok := lpmKeyOf(key(test.code, test.state))
		if k != test.key || pressed != test.pressed || ok != test.ok {
			t.Errorf("key %#x state %d: %q %v %v", test.code, test.state, k, pressed, ok)
		}
	}

	content := func(what byte, n uint32) []byte {
		frame := lpmFrame(lpmModule, lpmContent, binary.BigEndian.AppendUint32(nil, n))
		frame[2] = what

		return frame
	}
	for _, test := range []struct {
		what  byte
		n     uint32
		key   string
		store bool
	}{
		{0, 2, "PRESET_2", false},
		{1, 6, "PRESET_6", true},
		{0, 7, "", false},
		{3, 0, "STOP", false},
		{3, 2, "PAUSE", false},
		{3, 4, "NEXT_TRACK", false},
		{3, 6, "", false},
		{2, 1, "", false},
	} {
		if k, store := lpmContentOf(content(test.what, test.n)); k != test.key || store != test.store {
			t.Errorf("0x85 %d/%d: %q %v", test.what, test.n, k, store)
		}
	}

	if lpmSourceName(lpmSourceModule) != "SoundTouch" || lpmSourceName(lpmSourceCount-1) != "Factory default" {
		t.Fatal("source names out of step with the processor's enum")
	}
}

func TestLPMRadioStatusFrame(t *testing.T) {
	t.Parallel()

	frame := lpmRadioStatusFrame(5200, lpmNetStatus(hw.LinkUp))
	if !lpmValid(frame) || frame[0] != 0x62 || frame[1] != lpmRadioStatus {
		t.Fatalf("header % x", frame[:2])
	}

	if got := frame[4:16]; !bytes.Equal(got, []byte{0, 0, 0x14, 0x50, 0, 0, 0, 0, 2, 0, 1, 0}) {
		t.Fatalf("payload % x", got)
	}

	for link, want := range map[hw.Link]byte{hw.LinkDown: 0, hw.LinkSetupAP: 1, hw.LinkConnecting: 3, hw.LinkUp: 2} {
		if got := lpmNetStatus(link); got != want {
			t.Errorf("lpmNetStatus(%d) = %d, want %d", link, got, want)
		}
	}
}

func TestLPMForwardVolumeSkipsOnlyTheTakeover(t *testing.T) {
	t.Parallel()

	var fromLPM atomic.Int64
	fromLPM.Store(30) // the remote set 30, and SoundTap takes it over

	for _, test := range []struct {
		level   int
		forward bool
	}{{30, false}, {40, true}, {30, true}} {
		if got := lpmForwardVolume(test.level, &fromLPM); got != test.forward {
			t.Errorf("level %d: forward = %v, want %v", test.level, got, test.forward)
		}
	}
}

func TestLPMSavedInputComesBackOnlyForTheBarsOwnInputs(t *testing.T) {
	t.Parallel()

	link := New(t.TempDir(), "test")
	if _, ok := link.SavedInput(); ok {
		t.Fatal("nothing saved yet")
	}

	for source, want := range map[uint32]bool{
		lpmSourceStandby: false, lpmSourceModule: false, lpmSourceBT: false, lpmSourceADAPTiQ: false,
		lpmSourceTV: true, 1: true,
	} {
		err := link.SaveSource(source)
		if err != nil {
			t.Fatal(err)
		}

		got, ok := link.SavedInput()
		if ok != want || (ok && got != source) {
			t.Errorf("source %d: got %d, %v, want %v", source, got, ok, want)
		}
	}
}

func TestLPMAudioMode(t *testing.T) {
	t.Parallel()

	link := New(t.TempDir(), "test")

	var sent []byte

	send := func(_ string, frame []byte) { sent = frame }
	link.send.Store(&send)

	if link.AudioModes.Get() != "" || link.AudioModes.Set("dialog") == nil {
		t.Fatal("mode known before the first status")
	}

	status := make([]byte, lpmFrameLen)
	status[0x3f] = 4
	link.statusMode(status)

	if m := link.AudioModes.Get(); m != "night" {
		t.Fatalf("status mode %q", m)
	}

	link.alive.Store(true) // the reader sets it on the processor's first frame

	if link.AudioModes.Set("loud") == nil || sent != nil {
		t.Fatal("unknown mode sent")
	}

	err := link.AudioModes.Set("dialog")
	if err != nil || sent[1] != lpmAudioMode || sent[0] != 0x62 ||
		!lpmValid(sent) || !bytes.Equal(sent[4:6], []byte{0, 3}) || link.AudioModes.Get() != "dialog" {
		t.Fatalf("set: %v % x", err, sent[:8])
	}

	status[0x3f] = 0
	link.statusMode(status)

	if link.AudioModes.Get() != "dialog" {
		t.Fatal("an unspecified mode in the status overwrote the known one")
	}
}

func TestLPMCECMode(t *testing.T) {
	t.Parallel()

	link := New(t.TempDir(), "test")

	var sent []byte

	send := func(_ string, frame []byte) { sent = frame }
	link.send.Store(&send)

	if link.CECModes.Set("on") == nil {
		t.Fatal("sent before the processor's first status")
	}

	link.alive.Store(true)
	link.CECModes.report(0) // off is a real value here, unlike the audio mode's 0

	if m := link.CECModes.Get(); m != "off" {
		t.Fatalf("reported %q", m)
	}

	link.CECModes.report(9)

	if link.CECModes.Get() != "off" {
		t.Fatal("an out-of-range report was kept")
	}

	err := link.CECModes.Set("alternate")
	if err != nil || sent[1] != lpmCECModeSet || sent[0] != 0x62 ||
		!lpmValid(sent) || sent[4] != 2 || sent[5] != 0 || link.CECModes.Get() != "alternate" {
		t.Fatalf("set: %v % x", err, sent[:8])
	}
}

func TestLPMHDMIInput(t *testing.T) {
	t.Parallel()

	link := New(t.TempDir(), "test")

	var sent []byte

	send := func(_ string, frame []byte) { sent = frame }
	link.send.Store(&send)
	link.alive.Store(true)
	link.HDMIInputs.report(0)
	link.HDMIInputs.report(4) // stock refuses 4 and up

	if m := link.HDMIInputs.Get(); m != "none" {
		t.Fatalf("reported %q", m)
	}

	err := link.HDMIInputs.Set("3")
	if err != nil || sent[1] != lpmHDMIInputSet || !lpmValid(sent) ||
		sent[4] != 3 || sent[5] != 0 || link.HDMIInputs.Get() != "3" {
		t.Fatalf("set: %v % x", err, sent[:8])
	}
}

// A bar in standby reports nothing, so a restarted module shows the last saved value.
func TestLPMChoiceSurvivesARestart(t *testing.T) {
	t.Parallel()

	link := New(t.TempDir(), "test")
	link.CECModes.Load()

	if link.CECModes.Get() != "" {
		t.Fatal("a value with nothing saved")
	}

	link.CECModes.report(2)
	link.CECModes.cur.Store(0)
	link.CECModes.Load()

	if m := link.CECModes.Get(); m != "alternate" {
		t.Fatalf("after a restart %q", m)
	}
}

func TestLPMLipSync(t *testing.T) {
	t.Parallel()

	link := New(t.TempDir(), "test")

	var sent []byte

	send := func(_ string, frame []byte) { sent = frame }
	link.send.Store(&send)

	if _, ok := link.LipSync(); ok {
		t.Fatal("known before a status")
	}

	status := make([]byte, lpmFrameLen)
	status[0x48], status[0x49] = 0, 40
	link.statusMode(status)

	if ms, ok := link.LipSync(); !ok || ms != 40 {
		t.Fatalf("status said %d %v", ms, ok)
	}

	if link.SetLipSync(20) == nil {
		t.Fatal("sent before the processor answered")
	}

	link.alive.Store(true)

	if link.SetLipSync(LipSyncMax+1) == nil || link.SetLipSync(-1) == nil || sent != nil {
		t.Fatal("out of range sent")
	}

	err := link.SetLipSync(20)
	if err != nil || sent[1] != lpmLipSyncSet || !lpmValid(sent) ||
		!bytes.Equal(sent[4:8], []byte{0, 0, 0, 20}) {
		t.Fatalf("set: %v % x", err, sent[:8])
	}

	if ms, _ := link.LipSync(); ms != 20 {
		t.Fatalf("after set %d", ms)
	}
}

func TestLPMAfterRecovery(t *testing.T) {
	t.Parallel()

	for cmdline, want := range map[string]bool{
		"console=ttyGS0 variant=ginger soundtap.recovery=-19": true,
		"soundtap.recovery=0 root=ubi0:rootfs":                true,
		"variant=ginger soundtap.nandroot=1":                  false,
		"x.soundtap.recovery=1":                               false,
	} {
		if got := lpmAfterRecovery(cmdline); got != want {
			t.Errorf("lpmAfterRecovery(%q) = %v, want %v", cmdline, got, want)
		}
	}

	frame := lpmFrame(lpmProcessor, lpmUpdateDone, nil)
	if frame[0] != 0x62 || frame[1] != 0x0c || !bytes.Equal(frame[124:], []byte{0x62, 0x0c, 0, 0}) {
		t.Errorf("update-done frame = %x, want stock's gingeripc 1 (62 0c ... 62 0c 00 00)", frame)
	}
}

// The app's unmute after a mute from the bar's remote has to lift the processor's mute.
func TestLPMMuteFrameOnlyOnChange(t *testing.T) {
	t.Parallel()

	var last atomic.Int32
	last.Store(1) // the remote muted the bar

	frame := lpmMuteFrame(false, &last)
	if len(frame) != lpmFrameLen || frame[1] != lpmKey || binary.BigEndian.Uint32(frame[8:]) != 4 ||
		binary.BigEndian.Uint32(frame[16:]) != lpmKeyUnmute {
		t.Fatalf("unmute frame = %x, want key frame {0, 4, 0, 0xcd, 0}", frame)
	}

	if lpmMuteFrame(false, &last) != nil {
		t.Fatal("a second unmute was sent again")
	}

	if frame := lpmMuteFrame(true, &last); frame == nil || binary.BigEndian.Uint32(frame[16:]) != lpmKeyMute {
		t.Fatalf("mute frame = %x, want key 0xfe", frame)
	}
}

// The app's HDMI pick follows the remote's HDMI button assignment, HDMI 1 while there is none.
func TestLPMPickInputHDMI(t *testing.T) {
	t.Parallel()

	var got []uint32

	pick := func(source uint32) { got = append(got, source) }
	link := New(t.TempDir(), "test")
	link.pick.Store(&pick)
	link.HDMIInputs.report(0)

	err := link.PickInput(sourceHDMI)
	if err != nil {
		t.Fatal(err)
	}

	link.HDMIInputs.report(2)

	err = link.PickInput(sourceHDMI)
	if err != nil {
		t.Fatal(err)
	}

	err = link.PickInput(sourceTV)
	if err != nil {
		t.Fatal(err)
	}

	err = link.PickInput(sourceBluetooth)
	if err != nil {
		t.Fatal(err)
	}

	if link.PickInput("AUX") == nil {
		t.Fatal("AUX picked")
	}

	if want := []uint32{lpmSourceHDMI1, 2, lpmSourceTV, lpmSourceBT}; !slices.Equal(got, want) {
		t.Fatalf("picked %v, want %v", got, want)
	}
}

// Bluetooth plays on the bar's Bluetooth input, everything else the module plays on its own.
func TestLPMModuleInput(t *testing.T) {
	t.Parallel()

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

func (link *fakeLPMLink) send(what string, frame []byte) {
	if what != "radio status" || !lpmValid(frame) || frame[1] != lpmRadioStatus {
		panic("unexpected frame " + what)
	}

	link.frames = append(link.frames, frame)
}

// The bar is told it runs on its cable while eth0 has carrier and a lease, and goes back to
// the Wi-Fi status when the cable is pulled. Nothing goes out twice while nothing changes.
func TestLPMRadioReportsTheWiredLink(t *testing.T) {
	t.Parallel()

	eth0 := &fakeEth0{carrier: false, addrs: nil}
	link := &fakeLPMLink{frames: nil}
	wifi := hw.LinkUp
	radio := &lpmRadio{
		mhz:    func() int { return 5200 },
		wifi:   func() hw.Link { return wifi },
		eth0:   eth0.state,
		send:   link.send,
		last:   nil,
		sentAt: time.Time{},
	}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	step := func(want byte, sends bool) {
		t.Helper()

		before := len(link.frames)
		frame, _ := radio.tick(now, false)
		now = now.Add(5 * time.Second)

		if !sends {
			if frame != nil || len(link.frames) != before {
				t.Fatalf("sent % x with nothing changed", frame)
			}

			return
		}

		if len(link.frames) != before+1 || frame == nil {
			t.Fatalf("sent %d frames, want 1", len(link.frames)-before)
		}

		if frame[12] != want {
			t.Fatalf("net = %d, want %d", frame[12], want)
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

	step(2, true) // back to the Wi-Fi status

	eth0.addrs = nil

	step(0, false)

	wifi = hw.LinkConnecting

	step(3, true)

	// Unchanged, it goes out again once a minute, and at once when the processor asks.
	now = now.Add(lpmRadioRefresh)

	step(3, true)

	if frame, news := radio.tick(now, true); frame == nil || !news {
		t.Fatal("the processor asked and got nothing")
	}
}

func TestLPMNetWiredValue(t *testing.T) {
	t.Parallel()

	lease := []net.Addr{&net.IPNet{IP: net.IPv4(10, 0, 0, 2), Mask: net.CIDRMask(8, 32)}}
	for _, test := range []struct {
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
		if got := lpmNet(speakerstate.WiredIP(test.carrier, test.addrs), test.wifi); got != test.want {
			t.Errorf("lpmNet(%v, %v, %d) = %d, want %d", test.carrier, test.addrs, test.wifi, got, test.want)
		}
	}
}

// A processor that gave the module up is never rebooted: soundtapd only keeps sending its
// health message, in this session and the next.
func TestCheckSilenceNeverRebootsTheBar(t *testing.T) {
	t.Parallel()

	link := New(t.TempDir(), "0123456789abcd")

	var sent []string

	giveUp := func() {
		sess := &session{
			l: link, spk: nil, keys: nil, log: slog.New(slog.DiscardHandler),
			send: func(what string, frame []byte) {
				sent = append(sent, what)
				if frame[1] != lpmHealth {
					t.Errorf("sent %q, op %#x, want only the health message", what, frame[1])
				}
			},
			fromLPM: atomic.Int64{}, barMute: atomic.Int32{}, bar: atomic.Int64{}, external: atomic.Int64{},
			quiet: atomic.Int64{}, statusSeen: atomic.Bool{}, radioResend: atomic.Bool{},
			lastFrame: atomic.Int64{}, seen: nil, silent: false,
		}

		for range 2 { // the processor talked, then said nothing past lpmGiveUp
			sess.lastFrame.Store(time.Now().Add(-lpmGiveUp - time.Second).UnixNano())
			sess.checkSilence()

			if got := sess.lastFrame.Load(); got != 0 {
				t.Fatalf("lastFrame = %d after a give-up, want 0", got)
			}
		}
	}

	giveUp()
	giveUp() // a new session, as after the link is opened again

	if len(sent) != 4 {
		t.Fatalf("sent %q, want the health message once per check", sent)
	}
}

// A choice whose save failed is saved again by the next report of the same value: the
// value in memory alone said it was kept, and the file stayed stale until the bar
// changed it.
func TestChoiceSaveRetriedAfterAFailedWrite(t *testing.T) {
	t.Parallel()

	home := filepath.Join(t.TempDir(), "home")
	link := New(home, "test") // home does not exist yet: the first save fails

	link.HDMIInputs.Keep("2")

	err := os.Mkdir(home, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	link.HDMIInputs.Keep("2")

	restarted := New(home, "test")
	restarted.HDMIInputs.Load()

	if got := restarted.HDMIInputs.Get(); got != "2" {
		t.Errorf("after a restart the HDMI input button is %q, want 2", got)
	}
}
