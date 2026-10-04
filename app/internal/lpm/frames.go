// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package lpm

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// MixerLevel is the level the mixer applies for the speaker's output level: unity once the
// processor has answered and applies the volume itself, as stock's S/PDIF does. Until then
// the mixer keeps the volume, so a link that never comes up cannot leave the bar at full
// scale.
func (l *Link) MixerLevel(output int) int {
	if l.ownsVolume.Load() {
		return 100
	}
	return output
}

// lpmSourceNames are the processor's source names, by number, as the bar shows them.
var lpmSourceNames = [lpmSourceCount]string{
	"Standby", "HDMI 1", "HDMI 2", "HDMI 3", "HDMI 4", "HDMI 5", "HDMI 6", "Side AUX", "TV",
	"SoundTouch", "Bluetooth", "ADAPTiQ", "Demo", "PTS", "Update", "Front analog", "Analog 1",
	"Analog 2", "Coax 1", "Coax 2", "Optical 1", "Optical 2", "Unify", "ASOC", "DARR", "LFE",
	"Wired", "Cold boot", "Factory default",
}

// lpmKeys maps the processor's key codes to SoundTap's, read from A4VServer's
// translation table: the six presets, power and transport.
var lpmKeys = map[uint32]string{ // the key names soundtapd uses on every model
	0x1a: "STOP", 0x41: "PRESET_1", 0x42: "PRESET_2", 0x43: "PRESET_3", 0x44: "PRESET_4",
	0x45: "PRESET_5", 0x46: "PRESET_6", 0x4c: "POWER", 0x55: "PLAY", 0x56: "PAUSE",
	0x59: "PREV_TRACK", 0x5a: "NEXT_TRACK",
}

// lpmKeyEvent hands a 0x05 frame's key to keys, with its producer (the bar's remote is
// producer 2 at 0x08) for the log of a confirming hold.
func lpmKeyEvent(keys Keys, fr []byte) {
	key, pressed, ok := lpmKeyOf(fr)
	if !ok {
		return
	}
	keys.Key(int(binary.BigEndian.Uint32(fr[0x10:])), pressed, key, binary.BigEndian.Uint32(fr[0x08:]))
}

// lpmKeyOf reads a 0x05 frame: the key and whether it went down or up. Held states are
// left out, as the button handler times holds itself.
func lpmKeyOf(f []byte) (key string, pressed, ok bool) {
	key, ok = lpmKeys[binary.BigEndian.Uint32(f[0x10:])]
	switch binary.BigEndian.Uint32(f[0x14:]) {
	case 0, 1:
		return key, true, ok
	case 2, 3:
		return key, false, ok
	}
	return "", false, false
}

// lpmContentOf reads a 0x85 frame: the key it stands for, or the preset to store.
func lpmContentOf(f []byte) (key string, store bool) {
	n := binary.BigEndian.Uint32(f[4:])
	switch f[2] {
	case 0, 1:
		if n >= 1 && n <= 6 {
			return "PRESET_" + strconv.Itoa(int(n)), f[2] == 1
		}
	case 3:
		return map[uint32]string{0: "STOP", 1: "PLAY", 2: "PAUSE", 4: "NEXT_TRACK", 5: "PREV_TRACK"}[n], false
	}
	return "", false
}

// lpmHealthAnswer is stock's health and status message: the module's version string.
func lpmHealthAnswer(dst byte, ver string) []byte {
	p := make([]byte, 24)
	copy(p[:23], ver)
	return lpmFrame(dst, lpmHealth, p)
}

// lpmRadioStatusFrame is the module's radio state as stock reports it (SendRadioStatus):
// the Wi-Fi frequency in MHz, a second radio's (0, SoundTap has one), then one byte each
// for the network, Bluetooth and the account login. Stock sends it at start and on every
// change; without it the bar blinks its Wi-Fi and SoundTouch lamps orange.
func lpmRadioStatusFrame(mhz int, net byte) []byte {
	p := make([]byte, 12)
	binary.BigEndian.PutUint32(p, uint32(max(mhz, 0))) //nolint:gosec // clamped above
	p[8] = net
	p[10] = 1 // logged in: SoundTap needs no account, so it never has a login to wait for
	return lpmFrame(lpmProcessor, lpmRadioStatus, p)
}

// lpmRadioRefresh is how often the radio status goes out again unchanged.
const lpmRadioRefresh = time.Minute

// lpmTapLine is one console line for the processor, as stock's TAP passthrough sends it.
func lpmTapLine(line string) []byte {
	f := lpmFrame(lpmProcessor, lpmTapCommand, []byte(line+"\r"))
	f[3] = 1
	binary.LittleEndian.PutUint32(f[lpmFrameLen-4:], lpmChecksum(f))
	return f
}

// lpmGiveUp is how long a processor that has talked before may stay silent, health messages
// and all, before soundtapd reboots the bar through its console. A processor that missed
// the module's health messages for too long gives the module up ("SM2 is toast") and
// stays so until it reboots; nothing else on the link brings it back (2026-11-10).
const lpmGiveUp = 2 * time.Minute

// lpmSilence is how long the processor may say nothing before soundtapd sends its health
// message unasked and logs the silence. The processor asks for it every 5 s while it runs.
const lpmSilence = 10 * time.Second

// lpmNetStatus is the network value the processor's Wi-Fi lamp reads: 0 none (off), 1 setup
// AP (amber), 2 connected (steady white), 3 connecting (blinking white); 4, wired, is
// lpmNetWired.
func lpmNetStatus(l hw.Link) byte {
	switch l {
	case hw.LinkSetupAP:
		return 1
	case hw.LinkConnecting:
		return 3
	case hw.LinkUp:
		return 2
	}
	return 0
}

// lpmNetWired is the network value for the bar running on its rear Ethernet: the processor
// turns the Wi-Fi lamp off, as stock does on Ethernet. The value is the fifth entry of the
// network enum read from stock's behaviour (ADR 0109); the bar took it without complaint on
// 2026-11-16 with a cable in.
const lpmNetWired = 4

// lpmNet is the network value for the processor: wired while eth0 has carrier and an IPv4
// address, the Wi-Fi state otherwise. A cable without a lease yet is not a network, so the
// lamp keeps showing Wi-Fi until DHCP answers, and pulling the cable hands it back to Wi-Fi.
func lpmNet(carrier bool, addrs []net.Addr, wifi hw.Link) byte {
	if speakerstate.WiredIP(carrier, addrs) != "" {
		return lpmNetWired
	}
	return lpmNetStatus(wifi)
}

// lpmRadio sends the processor the module's radio status (0x15): when it changes, when the
// processor asks for it again, and once per lpmRadioRefresh unchanged.
type lpmRadio struct {
	mhz  func() int                              // the Wi-Fi frequency, 0 when not associated
	wifi func() hw.Link                          // the Wi-Fi link as the lamps see it
	eth0 func() (carrier bool, addrs []net.Addr) // the rear Ethernet as the kernel sees it
	send func(what string, frame []byte)

	last   []byte
	sentAt time.Time
}

// tick builds the current status and sends it if it is due. It returns the frame it sent,
// nil when nothing went out, and whether that was news (changed or asked for).
func (r *lpmRadio) tick(now time.Time, asked bool) (sent []byte, news bool) {
	carrier, addrs := r.eth0()
	fr := lpmRadioStatusFrame(r.mhz(), lpmNet(carrier, addrs, r.wifi()))
	// Also once a minute unchanged: the processor can lose it without sending a new
	// 0x14 status. On 2026-11-10 an all-zero frame came in, after which the bar blinked
	// its Wi-Fi and SoundTouch lamps orange while the radio still played.
	news = asked || !bytes.Equal(fr, r.last)
	if !news && now.Sub(r.sentAt) < lpmRadioRefresh {
		return nil, false
	}
	r.send("radio status", fr)
	r.last, r.sentAt = fr, now
	return fr, news
}

// wifiMHz is the frequency wlan0 is associated on, 0 when it is not.
func wifiMHz(ctx context.Context) int {
	out, _ := exec.CommandContext(ctx, "iw", "dev", "wlan0", "link").Output()
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "freq:"); ok {
			mhz, _ := strconv.Atoi(strings.TrimSpace(strings.SplitN(v, ".", 2)[0]))
			return mhz
		}
	}
	return 0
}

func lpmSetVolume(level int) []byte {
	p := make([]byte, 4)
	binary.BigEndian.PutUint32(p, uint32(min(max(level, 0), 100)))
	return lpmFrame(lpmProcessor, lpmVolume, p)
}

// lpmMuteFrame returns stock's user mute (SendMute, FUN_0008d88c) when muted differs from
// what the processor last had, or nil. last holds 0 (unknown), 1 (muted) or 2 (not). It is
// a 0x05 key frame {0, 4, 0, key, 0}: 0xfe mutes, 0xcd unmutes. The processor mutes itself
// on its remote's mute key, and a level sent by 0xb2 does not lift that: an unmute from the
// app left the bar silent (2026-11-08). Op 0xb1 is stock's internal mute, not this one.
func lpmMuteFrame(muted bool, last *atomic.Int32) []byte {
	want, key := int32(2), uint32(lpmKeyUnmute)
	if muted {
		want, key = 1, lpmKeyMute
	}
	if last.Swap(want) == want {
		return nil
	}
	p := make([]byte, 20)
	binary.BigEndian.PutUint32(p[4:], 4)
	binary.BigEndian.PutUint32(p[12:], key)
	return lpmFrame(lpmProcessor, lpmKey, p)
}

// lpmForwardVolume says whether a volume change goes to the processor. Only the change
// that takes over the processor's own level stays here, and only once: a level that stayed
// remembered swallowed the app's later return to it, leaving the bar louder than the app
// showed.
func lpmForwardVolume(level int, fromLPM *atomic.Int64) bool {
	return fromLPM.Swap(-1) != int64(level)
}

// lpmEchoAnswer is stock's answer to the processor's ping: {0, n+1} back to its sender.
func lpmEchoAnswer(ping []byte) []byte {
	p := make([]byte, 8)
	binary.BigEndian.PutUint32(p[4:], binary.BigEndian.Uint32(ping[8:])+1)
	return lpmFrame(ping[0]>>4, lpmEchoReply, p)
}

// lpmVolumeOf reads a 0xb0 frame: the level the processor plays at and whether it is
// muted.
func lpmVolumeOf(f []byte) (level int, muted bool) {
	return int(binary.BigEndian.Uint32(f[4:])), binary.BigEndian.Uint32(f[8:]) != 0
}

// lpmSpeakers reads a 0xb4 frame: whether the bass module and the rears are paired, and
// whether they are connected. Stock (HandleSpeakerListResponse) counts a paired speaker
// as up when its state byte is 0 or 2; the bass module playing in the room reports 2
// (2026-11-06), which this read as down before. The rears are up when either
// one is.
func lpmSpeakers(f []byte) speaker.Soundbar {
	up := func(state byte) bool { return state == 0 || state == 2 }
	rears := f[0x2a] != 0 || f[0x4e] != 0
	return speaker.Soundbar{
		BassPaired: f[6] != 0, BassConnected: f[6] != 0 && up(f[7]),
		RearsPaired:    rears,
		RearsConnected: rears && (up(f[0x2b]) || up(f[0x4f])),
	}
}

// lpmToneOf reads a 0x8a frame: four BE int16 levels, then (when the processor sends
// them) the four minimums, four maximums and four steps. A control whose range is empty
// cannot be set.
func lpmToneOf(f []byte) speaker.Tone {
	at := func(group, control int) int {
		return int(int16(binary.BigEndian.Uint16(f[4+2*(group*4+control):]))) //nolint:gosec // two's complement of a 16-bit field
	}
	c := func(i int) speaker.ToneControl {
		return speaker.ToneControl{Value: at(0, i), Min: at(1, i), Max: at(2, i), Step: at(3, i)}
	}
	return speaker.Tone{Bass: c(0), Treble: c(1), Centre: c(2), Rear: c(3)}
}

// lpmToneFrame is stock's tone request to the processor: four BE int16 levels.
func lpmToneFrame(levels [4]int) []byte {
	p := make([]byte, 8)
	for i, l := range levels {
		binary.BigEndian.PutUint16(p[2*i:], uint16(int16(l))) //nolint:gosec // the level was checked against the int16 range the soundbar reported
	}
	return lpmFrame(lpmProcessor, lpmTone, p)
}
