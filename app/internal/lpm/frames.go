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
		return mixerUnity
	}

	return output
}

// Sizes and places in a frame's payload, which starts at byte lpmPayloadAt. Payload
// fields are big-endian u32 (lpmWord bytes), tone levels BE int16 (lpmHalfWord bytes).
const (
	lpmPayloadAt = 4
	lpmWord      = 4
	lpmHalfWord  = 2
	// lpmHealthLen is the health message's payload: the version string, 23 bytes and a NUL.
	lpmHealthLen = 24
	// lpmRadioStatusLen is the radio status's payload: two u32 frequencies, then a byte
	// each for the network, Bluetooth and the account login, padded to a word.
	lpmRadioStatusLen = 12
	// lpmActivateLen is a source activation's payload: {source, 0, 0} or {source, 0, 1}.
	lpmActivateLen = 12
	// lpmKeyFrameLen is a key frame's payload, five u32: {0, lpmKeyWord1, 0, key, 0}.
	lpmKeyFrameLen = 20
	// lpmKeyWord1 is the second word of stock's mute key frame, as SendMute writes it.
	lpmKeyWord1 = 4
	// lpmEchoReplyLen is the echo reply's payload: {0, n+1}.
	lpmEchoReplyLen = 8
	// lpmToneControls is the tone controls in a 0x8a frame: bass, treble, centre, rear.
	lpmToneControls = 4
	// lpmVolumeMax is the top of the processor's volume scale, 0..100.
	lpmVolumeMax = 100
	// mixerUnity is the mixer's level that leaves the output as it is.
	mixerUnity = 100
	// lpmPresets is the presets a 0x85 content request can play or store: 1 to 6.
	lpmPresets = 6
)

// Key states at byte 0x14 of a 0x05 frame: 0-1 pressed, 2-3 released, 4-7 held.
const (
	lpmKeyPressed, lpmKeyPressedToo   = 0, 1
	lpmKeyReleased, lpmKeyReleasedToo = 2, 3
)

// What argument byte 2 of a 0x85 content request asks for.
const (
	lpmContentPlay      = 0 // play preset n
	lpmContentStore     = 1 // store what plays as preset n
	lpmContentTransport = 3 // the transport key n
)

// The processor's network values, which its Wi-Fi lamp reads (lpmNetStatus).
const (
	lpmNetNone       = 0 // off
	lpmNetSetupAP    = 1 // amber
	lpmNetConnected  = 2 // steady white
	lpmNetConnecting = 3 // blinking white
)

// The module's knowledge of the processor's user mute (session.barMute).
const (
	barMuteUnknown = 0
	barMuted       = 1
	barUnmuted     = 2
)

// lpmSpeakerStateUp is, with 0, the state byte stock counts as a paired speaker that is up.
const lpmSpeakerStateUp = 2

// lpmSourceName is the processor's source name for source, by number, as the bar shows it.
func lpmSourceName(source uint32) string {
	return [lpmSourceCount]string{
		"Standby", "HDMI 1", "HDMI 2", "HDMI 3", "HDMI 4", "HDMI 5", "HDMI 6", "Side AUX", "TV",
		"SoundTouch", "Bluetooth", "ADAPTiQ", "Demo", "PTS", "Update", "Front analog", "Analog 1",
		"Analog 2", "Coax 1", "Coax 2", "Optical 1", "Optical 2", "Unify", "ASOC", "DARR", "LFE",
		"Wired", "Cold boot", "Factory default",
	}[source]
}

// lpmKeys maps the processor's key codes to SoundTap's, read from A4VServer's
// translation table: the six presets, power and transport.
//
//nolint:gochecknoglobals // a lookup table, built once and only read
var lpmKeys = map[uint32]string{ // the key names soundtapd uses on every model
	0x1a: "STOP", 0x41: "PRESET_1", 0x42: "PRESET_2", 0x43: "PRESET_3", 0x44: "PRESET_4",
	0x45: "PRESET_5", 0x46: "PRESET_6", 0x4c: "POWER", 0x55: "PLAY", 0x56: "PAUSE",
	0x59: "PREV_TRACK", 0x5a: "NEXT_TRACK",
}

// lpmKeyEvent hands a 0x05 frame's key to keys, with its producer (the bar's remote is
// producer 2 at 0x08) for the log of a confirming hold.
func lpmKeyEvent(keys Keys, frame []byte) {
	key, pressed, ok := lpmKeyOf(frame)
	if !ok {
		return
	}

	keys.Key(int(binary.BigEndian.Uint32(frame[0x10:])), pressed, key, binary.BigEndian.Uint32(frame[0x08:]))
}

// lpmKeyOf reads a 0x05 frame: the key, whether it went down or up, and whether it is a
// key SoundTap knows in a state it acts on. Held states are left out, as the button
// handler times holds itself.
func lpmKeyOf(frame []byte) (string, bool, bool) {
	key, ok := lpmKeys[binary.BigEndian.Uint32(frame[0x10:])]
	switch binary.BigEndian.Uint32(frame[0x14:]) {
	case lpmKeyPressed, lpmKeyPressedToo:
		return key, true, ok
	case lpmKeyReleased, lpmKeyReleasedToo:
		return key, false, ok
	default:
		return "", false, false
	}
}

// lpmContentOf reads a 0x85 frame: the key it stands for, and whether it asks to store
// what plays as that preset.
func lpmContentOf(frame []byte) (string, bool) {
	which := binary.BigEndian.Uint32(frame[lpmPayloadAt:])
	switch frame[2] {
	case lpmContentPlay, lpmContentStore:
		if which >= 1 && which <= lpmPresets {
			return "PRESET_" + strconv.Itoa(int(which)), frame[2] == lpmContentStore
		}
	case lpmContentTransport:
		return map[uint32]string{0: "STOP", 1: "PLAY", 2: "PAUSE", 4: "NEXT_TRACK", 5: "PREV_TRACK"}[which], false
	default:
		// Nothing SoundTap acts on.
	}

	return "", false
}

// lpmHealthAnswer is stock's health and status message: the module's version string.
func lpmHealthAnswer(dst byte, ver string) []byte {
	payload := make([]byte, lpmHealthLen)
	copy(payload[:lpmHealthLen-1], ver)

	return lpmFrame(dst, lpmHealth, payload)
}

// lpmRadioStatusFrame is the module's radio state as stock reports it (SendRadioStatus):
// the Wi-Fi frequency in MHz, a second radio's (0, SoundTap has one), then one byte each
// for the network, Bluetooth and the account login. Stock sends it at start and on every
// change; without it the bar blinks its Wi-Fi and SoundTouch lamps orange.
func lpmRadioStatusFrame(mhz int, netStatus byte) []byte {
	payload := make([]byte, lpmRadioStatusLen)
	binary.BigEndian.PutUint32(payload, uint32(max(mhz, 0))) //nolint:gosec // clamped above
	payload[8] = netStatus
	payload[10] = 1 // logged in: SoundTap needs no account, so it never has a login to wait for

	return lpmFrame(lpmProcessor, lpmRadioStatus, payload)
}

// lpmRadioRefresh is how often the radio status goes out again unchanged.
const lpmRadioRefresh = time.Minute

// lpmTapLine is one console line for the processor, as stock's TAP passthrough sends it.
func lpmTapLine(line string) []byte {
	frame := lpmFrame(lpmProcessor, lpmTapCommand, []byte(line+"\r"))
	frame[3] = 1
	binary.LittleEndian.PutUint32(frame[lpmFrameLen-4:], lpmChecksum(frame))

	return frame
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
func lpmNetStatus(link hw.Link) byte {
	return map[hw.Link]byte{
		hw.LinkDown:       lpmNetNone,
		hw.LinkSetupAP:    lpmNetSetupAP,
		hw.LinkConnecting: lpmNetConnecting,
		hw.LinkUp:         lpmNetConnected,
	}[link] // anything else: lpmNetNone, the zero value
}

// lpmNetWired is the network value for the bar running on its rear Ethernet: the processor
// turns the Wi-Fi lamp off, as stock does on Ethernet. The value is the fifth entry of the
// network enum read from stock's behaviour (ADR 0109); the bar took it without complaint on
// 2026-11-16 with a cable in.
const lpmNetWired = 4

// lpmNet is the network value for the processor: wired while eth0 has an IPv4 address with
// carrier (wiredIP, speakerstate.WiredIP), the Wi-Fi state otherwise. A cable without a
// lease yet is not a network, so the lamp keeps showing Wi-Fi until DHCP answers, and
// pulling the cable hands it back to Wi-Fi.
func lpmNet(wiredIP string, wifi hw.Link) byte {
	if wiredIP != "" {
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
func (r *lpmRadio) tick(now time.Time, asked bool) ([]byte, bool) {
	carrier, addrs := r.eth0()
	frame := lpmRadioStatusFrame(r.mhz(), lpmNet(speakerstate.WiredIP(carrier, addrs), r.wifi()))
	// Also once a minute unchanged: the processor can lose it without sending a new
	// 0x14 status. On 2026-11-10 an all-zero frame came in, after which the bar blinked
	// its Wi-Fi and SoundTouch lamps orange while the radio still played.
	news := asked || !bytes.Equal(frame, r.last)
	if !news && now.Sub(r.sentAt) < lpmRadioRefresh {
		return nil, false
	}

	r.send("radio status", frame)
	r.last, r.sentAt = frame, now

	return frame, news
}

// wifiMHz is the frequency wlan0 is associated on, 0 when it is not.
func wifiMHz(ctx context.Context) int {
	out, _ := exec.CommandContext(ctx, "iw", "dev", "wlan0", "link").Output()
	for line := range strings.SplitSeq(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "freq:"); ok {
			whole, _, _ := strings.Cut(v, ".")
			mhz, _ := strconv.Atoi(strings.TrimSpace(whole))

			return mhz
		}
	}

	return 0
}

func lpmSetVolume(level int) []byte {
	payload := make([]byte, lpmWord)
	binary.BigEndian.PutUint32(payload, uint32(min(max(level, 0), lpmVolumeMax)))

	return lpmFrame(lpmProcessor, lpmVolume, payload)
}

// lpmMuteFrame returns stock's user mute (SendMute, FUN_0008d88c) when muted differs from
// what the processor last had, or nil. last holds barMuteUnknown, barMuted or barUnmuted.
// It is a 0x05 key frame {0, 4, 0, key, 0}: 0xfe mutes, 0xcd unmutes. The processor mutes itself
// on its remote's mute key, and a level sent by 0xb2 does not lift that: an unmute from the
// app left the bar silent (2026-11-08). Op 0xb1 is stock's internal mute, not this one.
//
//nolint:revive // flag-parameter: muted is the speaker's mute state, the data this turns into a frame
func lpmMuteFrame(muted bool, last *atomic.Int32) []byte {
	want, key := int32(barUnmuted), uint32(lpmKeyUnmute)
	if muted {
		want, key = barMuted, lpmKeyMute
	}

	if last.Swap(want) == want {
		return nil
	}

	payload := make([]byte, lpmKeyFrameLen)
	binary.BigEndian.PutUint32(payload[4:], lpmKeyWord1)
	binary.BigEndian.PutUint32(payload[12:], key)

	return lpmFrame(lpmProcessor, lpmKey, payload)
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
	payload := make([]byte, lpmEchoReplyLen)
	binary.BigEndian.PutUint32(payload[4:], binary.BigEndian.Uint32(ping[8:])+1)

	return lpmFrame(lpmSender(ping), lpmEchoReply, payload)
}

// lpmVolumeOf reads a 0xb0 frame: the level the processor plays at and whether it is
// muted.
func lpmVolumeOf(frame []byte) (int, bool) {
	return int(binary.BigEndian.Uint32(frame[4:])), binary.BigEndian.Uint32(frame[8:]) != 0
}

// lpmSpeakers reads a 0xb4 frame: whether the bass module and the rears are paired, and
// whether they are connected. Stock (HandleSpeakerListResponse) counts a paired speaker
// as up when its state byte is 0 or 2; the bass module playing in the room reports 2
// (2026-11-06), which this read as down before. The rears are up when either
// one is.
func lpmSpeakers(frame []byte) speaker.Soundbar {
	isUp := func(state byte) bool { return state == 0 || state == lpmSpeakerStateUp }
	rears := frame[0x2a] != 0 || frame[0x4e] != 0

	return speaker.Soundbar{
		BassPaired: frame[6] != 0, BassConnected: frame[6] != 0 && isUp(frame[7]),
		RearsPaired:    rears,
		RearsConnected: rears && (isUp(frame[0x2b]) || isUp(frame[0x4f])),
	}
}

// lpmToneOf reads a 0x8a frame: four BE int16 levels, then (when the processor sends
// them) the four minimums, four maximums and four steps. A control whose range is empty
// cannot be set.
func lpmToneOf(frame []byte) speaker.Tone {
	// The groups of four: the levels, then the minimums, maximums and steps; in each the
	// bass, treble, centre and rear.
	const levels, minimums, maximums, steps = 0, 1, 2, 3

	const bass, treble, centre, rear = 0, 1, 2, 3

	field := func(group, control int) int {
		raw := frame[lpmPayloadAt+lpmHalfWord*(group*lpmToneControls+control):]

		return int(int16(binary.BigEndian.Uint16(raw))) //nolint:gosec // two's complement of a 16-bit field
	}
	control := func(i int) speaker.ToneControl {
		return speaker.ToneControl{
			Value: field(levels, i), Min: field(minimums, i), Max: field(maximums, i), Step: field(steps, i),
		}
	}

	return speaker.Tone{Bass: control(bass), Treble: control(treble), Centre: control(centre), Rear: control(rear)}
}

// lpmToneFrame is stock's tone request to the processor: four BE int16 levels.
func lpmToneFrame(levels [lpmToneControls]int) []byte {
	payload := make([]byte, lpmToneControls*lpmHalfWord)
	for i, level := range levels {
		// The level was checked against the int16 range the soundbar reported.
		binary.BigEndian.PutUint16(payload[lpmHalfWord*i:], uint16(int16(level))) //nolint:gosec // checked range
	}

	return lpmFrame(lpmProcessor, lpmTone, payload)
}
