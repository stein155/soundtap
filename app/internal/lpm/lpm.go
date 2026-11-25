// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package lpm is the SoundTouch 300 module's side of its link to the soundbar processor
// (the LPM) on UART0 (ADR 0109): the frame format, the processor's source, volume, keys,
// tone and speakers, and the settings the bar keeps.
package lpm

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// The SoundTouch 300's module talks to the soundbar processor (the LPM: DSP, HDMI, the
// bass module, rears, ADAPTiQ, buttons and IR) over UART0. Behaviour read from stock
// A4VServer 27.0.6 and the running bar's stty, never its code:
//
//   - 921600 baud raw, fixed 128-byte frames in both directions.
//
//   - byte 0 = sender<<4 | receiver (the module is 6, the processor 2), byte 1 the
//     opcode, bytes 2-3 two argument bytes, 4..123 the payload zero-padded, and
//     124..127 the XOR of the 31 little-endian words before it.
//
//   - Payload fields are big-endian u32.
//
//   - The processor pings with op 0x01 {_, _, n}; stock answers op 0x02 {0, n+1} to the
//     sender (A4V_IpcServerTask::HandleEchoCmd).
//
//   - The processor also asks for health and status with op 0x11 once a second; stock
//     answers op 0x13 with its version string (SendHealthAndStatusMessage) and sends one
//     unasked at start. The processor's own image counts the requests that go unanswered:
//     after 60 it logs "Pings failed, turning on LEDs of death" and shows the error on
//     the bar's lights. It does not reset the module.
//
//   - Its first op 0x14 is its status (version, uptime, volume at byte 0x30, source at
//     0x2c); stock then asks for tone (0x8b), CEC, HDMI, speakers (0xb3) and timeouts.
//
//   - Op 0xb0 is the processor's volume {level, muted, latency}: the remote and the TV
//     change the volume there, and stock takes it over (SendVolumeMuteToAPServer).
//
//   - Op 0x50 from the processor is a source picked on the bar, its remote or the TV
//     ({source}). Stock answers 0x51 {source, 0, 1} and follows it: standby, its own
//     input, or another input it then shows as the product's source.
//
//   - Op 0x05 is a key from the bar's buttons or remote: key at byte 0x10, state at
//     0x14 (0-1 pressed, 2-3 released, 4-7 held), producer at 0x08. Volume never comes
//     this way: the processor applies it and reports 0xb0.
//
//   - Op 0x85 is a content request: argument byte 2 says what, the u32 at 4 which one.
//     0 plays preset n, 1 stores the current item as preset n, 3 is transport.
//
//   - Ops 0xd7 (CEC mode) and 0xe9 (auto power down, screensaver) are answers to
//     stock's settings queries. The processor acts on them itself; stock only shows
//     them in its app, so soundtapd only logs them.
//
//   - Op 0xb4 is the speaker list: the bass module at bytes 6-7 (paired, state), the
//     rears at 0x2a-0x2b and 0x4e-0x4f. State 0 or 2 is not connected.
//
//   - Op 0x14 was seen when the source changed, not when soundtapd started, and none of
//     the commands read in stock asks for the source (op 0x08 is a reboot, never a
//     probe). The processor keeps playing while the module restarts, so soundtapd
//     remembers the last source itself. 0xb0 too comes only on a change.
//
// SoundTap selects the module's input when it starts playing, hands the volume to the
// processor the way stock does (0..100, 0 while muted), asks for its speaker list, and
// logs what the processor says. Stock has no power opcode: standby is source 0 through 0x50. SoundTap's own standby
// selects it and waking selects the module's input again; merely going idle does not, as
// that must not switch off a bar that is playing the TV.
const (
	lpmDevice      = "/dev/ttyS0"
	lpmFrameLen    = 128
	lpmModule      = 6
	lpmProcessor   = 2
	lpmEcho        = 0x01
	lpmEchoReply   = 0x02
	lpmHealthAsk   = 0x11
	lpmHealth      = 0x13 // {version string, 23 bytes and a NUL}
	lpmStatus      = 0x14
	lpmVolumeInfo  = 0xb0
	lpmSpeakerList = 0xb4
	lpmTone        = 0x8a // tone: bass, treble, centre, rear as BE int16
	lpmToneQuery   = 0x8b // answered with 0x8a
	lpmActivate    = 0x50
	lpmActivated   = 0x51 // stock's answer to the processor's 0x50: {source, 0, 1}
	lpmKey         = 0x05
	lpmUpdateDone  = 0x0c // stock U-Boot's `gingeripc 1`: the update attempt is over
	// lpmTapCommand is stock's TAP passthrough: a line for the processor's console; byte 3 =
	// 1 on the last part.
	lpmTapCommand      = 0x09
	lpmContent         = 0x85
	lpmVolume          = 0xb2 // stock's VolumeCallbackAction: {level 0..100}
	lpmKeyMute         = 0xfe // key stock's SendMute sends in a 0x05 frame to mute the processor
	lpmKeyUnmute       = 0xcd // ... and to lift that mute
	lpmSpeakerQuery    = 0xb3 // answered with the speaker list (0xb4): bass module, rears
	lpmRadioStatus     = 0x86 // the module's network state, which the bar's lamps show
	lpmAudioMode       = 0x8d // stock's SendDSPAudioControls: {mode as BE u16}; the 0x14 status carries it at 0x3f
	lpmCECModeSet      = 0xd8 // stock's SendCECMode: {mode u8}
	lpmCECModeReport   = 0xd7 // the processor's CEC mode at byte 4
	lpmHDMIInputSet    = 0xdb // stock's SendHDMIInputSelection: {button u8}
	lpmHDMIInputReport = 0xda // the processor's HDMI input button at byte 4
	lpmLipSyncSet      = 0x88 // stock's SendAVSyncDelay: {delay in ms as BE u32}; the 0x14 status carries it at 0x48
	// Sources are one enum in both firmwares: 0 STANDBY, 1-6 HDMI_1-6, 7 SIDE_AUX, 8 TV,
	// 9 SHELBY (this module), 10 BLUETOOTH, ... 28 FACTORY_DEFAULT. Stock activates 9 and
	// 0 (A4VServer's network handler), and the processor's HandleSourceActivateFromST
	// treats 0 as standby and refuses 29 and up.
	lpmSourceModule  = 9
	lpmSourceBT      = 10
	lpmSourceStandby = 0
	lpmSourceTV      = 8
	lpmSourceHDMI1   = 1
	lpmSourceADAPTiQ = 11
	lpmSourceCount   = 29
	lpmPayloadMaxLen = lpmFrameLen - 8
	// lpmAddrBits is the width of the receiver in byte 0; the sender is above it.
	lpmAddrBits = 4
	lpmAddrMask = 1<<lpmAddrBits - 1
	// lpmLogHead is how much of an op's first frame is logged.
	lpmLogHead = 24
	// lpmFollowQuiet is how long the playback loop holds back after soundtapd follows a
	// change the processor made.
	lpmFollowQuiet = 2 * time.Second
	// lpmRadioTick is how often radioLoop looks at the radio status and the silence.
	lpmRadioTick  = 5 * time.Second
	playPlaying   = "PLAY_STATE"
	playBuffering = "BUFFERING_STATE"
)

// SourceProduct is stock's name for an input the processor plays, as the speaker's source.
const SourceProduct = "PRODUCT"

// SourceTV is the processor's TV input, the one a bar that was never told otherwise plays.
const SourceTV = lpmSourceTV

// The speaker's source names the link maps to the processor's inputs, as in the API.
const (
	sourceBluetooth = "BLUETOOTH"
	sourceTV        = "TV"
	sourceHDMI      = "HDMI"
)

func lpmChecksum(f []byte) uint32 {
	var x uint32
	for i := 0; i < lpmFrameLen-4; i += 4 {
		x ^= binary.LittleEndian.Uint32(f[i:])
	}

	return x
}

// lpmAfterRecovery says whether U-Boot ran the power-on recovery (soundtap.recovery=<rc>).
func lpmAfterRecovery(cmdline string) bool {
	return slices.ContainsFunc(strings.Fields(cmdline), func(a string) bool {
		return strings.HasPrefix(a, "soundtap.recovery=")
	})
}

func lpmFrame(dst, op byte, payload []byte) []byte {
	frame := make([]byte, lpmFrameLen)
	frame[0] = lpmModule<<lpmAddrBits | dst&lpmAddrMask
	frame[1] = op
	copy(frame[lpmPayloadAt:lpmPayloadAt+lpmPayloadMaxLen], payload)
	binary.LittleEndian.PutUint32(frame[lpmFrameLen-4:], lpmChecksum(frame))

	return frame
}

// lpmSender is the device that sent frame, from its first byte.
func lpmSender(frame []byte) byte {
	return frame[0] >> lpmAddrBits
}

func lpmValid(f []byte) bool {
	return len(f) == lpmFrameLen && binary.LittleEndian.Uint32(f[lpmFrameLen-4:]) == lpmChecksum(f)
}

// lpmActivateSource is stock's ActivateSource: {source, 0, 0} to the processor.
func lpmActivateSource(source uint32) []byte {
	payload := make([]byte, lpmActivateLen)
	binary.BigEndian.PutUint32(payload, source)

	return lpmFrame(lpmProcessor, lpmActivate, payload)
}

// lpmFrames reads frames off src, sliding one byte at a time past anything whose checksum
// does not hold, so a start mid-frame resynchronises.
func lpmFrames(src io.Reader, frame func([]byte)) error {
	buf := make([]byte, lpmFrameLen)

	err := readFull(src, buf)
	if err != nil {
		return err
	}

	for {
		if lpmValid(buf) {
			frame(bytes.Clone(buf))

			err = readFull(src, buf)
			if err != nil {
				return err
			}

			continue
		}

		copy(buf, buf[1:])

		err = readFull(src, buf[lpmFrameLen-1:])
		if err != nil {
			return err
		}
	}
}

// readFull fills buf from src.
func readFull(src io.Reader, buf []byte) error {
	_, err := io.ReadFull(src, buf)
	if err != nil {
		return fmt.Errorf("lpm read: %w", err)
	}

	return nil
}

// Keys takes the keys the processor reports: its buttons' and its remote's.
type Keys interface {
	// Key is one edge of the key at index; producer is the processor's input that sent it,
	// for the log of a confirming hold.
	Key(index int, pressed bool, action string, producer uint32)
}

// Run opens the link and keeps it until ctx ends: it answers the processor, follows the
// source, volume and keys it reports into spk and keys, and selects the module's input
// when spk plays.
func (l *Link) Run(ctx context.Context, spk *speakerstate.Speaker, keys Keys, log *slog.Logger) {
	err := exec.CommandContext(ctx, "busybox", "stty", "-F", lpmDevice, "921600", "raw", "-echo").Run()
	if err != nil {
		log.Error("lpm serial setup", "err", err)

		return
	}

	tty, err := os.OpenFile(lpmDevice, os.O_RDWR, 0)
	if err != nil {
		log.Error("lpm open", "err", err)

		return
	}

	context.AfterFunc(ctx, func() { _ = tty.Close() })

	sess := &session{
		l:           l,
		spk:         spk,
		keys:        keys,
		log:         log,
		send:        nil,
		fromLPM:     atomic.Int64{},
		barMute:     atomic.Int32{},
		bar:         atomic.Int64{},
		external:    atomic.Int64{},
		quiet:       atomic.Int64{},
		statusSeen:  atomic.Bool{},
		radioResend: atomic.Bool{},
		lastFrame:   atomic.Int64{},
		seen:        nil,
		silent:      false,
	}
	sess.send = func(what string, frame []byte) {
		log.Debug("lpm send", "what", what, "frame", hex.EncodeToString(frame))

		_, err := tty.Write(frame)
		if err != nil {
			log.Error("lpm "+what, "err", err)
		}
	}
	sess.start(ctx)
	safego.Go("lpmRadio", func() { sess.radioLoop(ctx) })
	safego.Go("lpmReader", func() {
		err := lpmFrames(tty, func(frame []byte) { sess.frame(ctx, frame) })
		if ctx.Err() == nil {
			log.Error("lpm read", "err", err)
		}
	})
	sess.followPlayback(ctx)
}

// session is one run of the link: what the module knows of the processor while it talks
// to it. Its methods take the run's context.
type session struct {
	l    *Link
	spk  *speakerstate.Speaker
	keys Keys
	log  *slog.Logger
	send func(what string, frame []byte)

	// fromLPM is the level just taken over from the processor: that one change is not
	// sent back.
	fromLPM atomic.Int64
	// barMute is the processor's own mute, as far as the module knows it.
	barMute atomic.Int32

	// bar is the source the processor plays as far as we know (-1 unknown); external the
	// last input it picked itself, which waking into PRODUCT selects again. quiet holds the
	// playback loop back while soundtapd follows a change the processor made, so the
	// standby or wake it causes is not sent back as a new request.
	// a 2 s window, not a causal link; a per-change token if it ever misfires.
	bar, external atomic.Int64
	quiet         atomic.Int64
	// statusSeen is set by the first status frame or by our own activation, whichever
	// comes first. On 2026-11-07 the bar woke from standby for a radio and its first
	// status, sent before it had handled our activation, still said standby: following
	// it put SoundTap back in standby and the radio never started.
	statusSeen atomic.Bool

	radioResend atomic.Bool
	// lastFrame is when the processor last said anything.
	lastFrame atomic.Int64
	// seen holds the ops whose first frame was logged; the reader's alone.
	seen map[byte]bool
	// silent is whether the last check found the processor silent; radioLoop's alone.
	silent bool
}

// start sets the session up before the reader runs: the volume hand-over, the saved
// choices and input, and the module's first messages to the processor.
func (s *session) start(ctx context.Context) {
	link, spk := s.l, s.spk
	s.fromLPM.Store(-1)
	spk.OnVolume(s.volumeChanged)

	for _, c := range []*Choice{link.AudioModes, link.CECModes, link.HDMIInputs} {
		c.Load()
	}

	send := s.send
	link.send.Store(&send)
	// The recovery combo puts the processor in update mode and it says nothing until told
	// the attempt is over; stock U-Boot does that, ours does not.
	if b, _ := os.ReadFile("/proc/cmdline"); lpmAfterRecovery(string(b)) {
		s.send("update done", lpmFrame(lpmProcessor, lpmUpdateDone, nil))
	}

	s.send("health", lpmHealthAnswer(lpmProcessor, "SoundTap "+link.version))
	link.ask("speaker query", lpmSpeakerQuery)
	// Tone too: otherwise only a 0x14 status asks for it, and after a boot the processor
	// may send none, so the app's first tone read failed with "has not answered yet".
	link.ask("tone query", lpmToneQuery)

	s.bar.Store(-1)
	s.external.Store(lpmSourceTV)

	pick := func(source uint32) { s.pick(ctx, source) }
	link.pick.Store(&pick)
	// Before the reader starts, so nothing goes to the processor: it is already playing this.
	if source, ok := link.SavedInput(); ok {
		s.bar.Store(int64(source))
		s.external.Store(int64(source))
		_ = spk.Select(ctx, SourceProduct, "", "", lpmSourceName(source), "", "")
		s.log.Info("lpm input from before the restart", "source", source, "name", lpmSourceName(source))
	}
}

// volumeChanged hands a volume change on the speaker to the processor once it owns the
// volume.
func (s *session) volumeChanged(level int) {
	if !s.l.ownsVolume.Load() {
		return
	}

	if frame := lpmMuteFrame(s.spk.StatusSnapshot().Muted, &s.barMute); frame != nil {
		s.send("mute", frame)
	}

	if lpmForwardVolume(level, &s.fromLPM) {
		s.send("volume", lpmSetVolume(level))
	}
}

func (s *session) remember(source uint32) {
	err := s.l.SaveSource(source)
	if err != nil {
		s.log.Warn("lpm remember source", "err", err)
	}
}

// activate selects source on the processor, unless it plays it already.
func (s *session) activate(why string, source uint32) {
	s.statusSeen.Store(true)

	if s.bar.Swap(int64(source)) != int64(source) {
		s.remember(source)
		s.send(why, lpmActivateSource(source))
		s.log.Info("lpm activate source", "why", why, "source", source)
	}
}

// follow makes the speaker follow a source the processor plays.
func (s *session) follow(ctx context.Context, source uint32) {
	spk := s.spk
	s.quiet.Store(time.Now().Add(lpmFollowQuiet).UnixNano())
	s.bar.Store(int64(source))
	s.remember(source)

	status := spk.StatusSnapshot()

	switch source {
	case lpmSourceStandby:
		_ = spk.EnterStandby(ctx)
	case lpmSourceBT:
		// The bar's own Bluetooth input is the module's: SoundTap's Bluetooth plays it,
		// and selecting it wakes the speaker as the BLUETOOTH key does on the other models.
		if status.Source != sourceBluetooth {
			spk.ApplyKey(sourceBluetooth)
		}
	case lpmSourceModule:
		if status.Source == SourceProduct {
			_ = spk.ReleaseSource(ctx, SourceProduct)
			status.Standby = true
		}

		if status.Standby {
			spk.ApplyKey("POWER")
		}
	default:
		if source != lpmSourceADAPTiQ { // waking must not start a calibration again
			s.external.Store(int64(source))
		}

		_ = spk.Select(ctx, SourceProduct, "", "", lpmSourceName(source), "", "")
	}
}

// pick switches the bar to one of the processor's own inputs, as its remote does.
func (s *session) pick(ctx context.Context, source uint32) {
	s.bar.Store(-1)
	s.activate("app", source)
	s.follow(ctx, source)
}

// radioLoop keeps the processor told that the module is there and what its network is.
func (s *session) radioLoop(ctx context.Context) {
	radio := &lpmRadio{
		mhz:    func() int { return wifiMHz(ctx) },
		wifi:   func() hw.Link { return hw.ReadLink(func() bool { return s.spk.StatusSnapshot().IP != "" }) },
		eth0:   speakerstate.Eth0,
		send:   s.send,
		last:   nil,
		sentAt: time.Time{},
	}

	for {
		s.checkSilence()

		if frame, news := radio.tick(time.Now(), s.radioResend.Swap(false)); news {
			s.log.Info("lpm radio status", "mhz", binary.BigEndian.Uint32(frame[4:]), "net", frame[12])
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(lpmRadioTick):
		}
	}
}

// checkSilence resends the health message while the processor is quiet, and reboots the
// bar once a processor that talked before has given the module up. It keeps in s.silent
// whether the processor was found silent.
func (s *session) checkSilence() {
	// After its own reset the processor waits for the module's health message and
	// asks for nothing; stock sends one at start, and a processor that rebooted
	// later never got another. After its timeout it gives the module up for good and
	// blinks two lamps orange (2026-11-10). So while it is quiet, tell it again.
	last := s.lastFrame.Load()
	if last == 0 || time.Since(time.Unix(0, last)) > lpmSilence {
		s.send("health", lpmHealthAnswer(lpmProcessor, "SoundTap "+s.l.version))
	}

	if last == 0 {
		return
	}

	quiet := time.Since(time.Unix(0, last))
	if quiet > lpmSilence && !s.silent {
		s.log.Warn("lpm processor silent", "since", time.Unix(0, last).UTC().Format(time.RFC3339))
	}
	// Once per boot: the reboot takes the module down too, and a processor that is
	// silent from the start never sets lastFrame, so this cannot loop.
	if quiet > lpmGiveUp {
		s.log.Error("lpm processor gave up on the module; rebooting the bar", "silent", quiet.Round(time.Second))
		s.send("reboot", lpmTapLine("rb 0"))
		s.lastFrame.Store(0)
	}

	s.silent = quiet > lpmSilence
}

// frame handles one frame from the processor.
func (s *session) frame(ctx context.Context, frame []byte) {
	link := s.l
	s.lastFrame.Store(time.Now().UnixNano())
	// The bar keeps the level it already plays at: SoundTap takes it over from the
	// next 0xb0 rather than pushing its own, which after an install or a reboot could
	// be far louder than what the room was listening to. Stock sends no 0xb2 here
	// either (strace 2026-11-06).
	if lpmSender(frame) == lpmProcessor && !link.ownsVolume.Swap(true) {
		s.log.Info("lpm answered; volume handed to the processor")
		link.alive.Store(true)
	}

	switch frame[1] {
	case lpmEcho:
		s.send("echo", lpmEchoAnswer(frame))
	case lpmHealthAsk:
		s.send("health", lpmHealthAnswer(lpmSender(frame), "SoundTap "+link.version))
	case lpmStatus:
		s.status(ctx, frame)
	case lpmCECModeReport:
		link.CECModes.report(uint32(frame[4]))
	case lpmHDMIInputReport:
		link.HDMIInputs.report(uint32(frame[4]))
	case lpmActivate:
		s.picked(ctx, frame)
	case lpmKey:
		lpmKeyEvent(s.keys, frame)
	case lpmContent:
		s.content(frame)
	case lpmVolumeInfo:
		s.volumeInfo(ctx, frame)
	case lpmTone:
		tone := lpmToneOf(frame)
		link.now.Store(&tone)
		s.log.Info("lpm tone", "tone", tone)
	case lpmSpeakerList:
		bar := lpmSpeakers(frame)
		link.bar.Store(&bar)
		s.log.Info("lpm speakers", "bar", bar)
	default:
		// Logged below, as every op is.
	}

	s.logFrame(frame)
}

// logFrame logs the first frame of each op, and every frame at debug.
func (s *session) logFrame(frame []byte) {
	if s.seen == nil {
		s.seen = map[byte]bool{}
	}

	if !s.seen[frame[1]] {
		s.seen[frame[1]] = true
		s.log.Info("lpm first frame", "op", frame[1], "hdr", frame[0], "head", hex.EncodeToString(frame[:lpmLogHead]))
	}

	s.log.Debug("lpm frame", "op", frame[1], "frame", hex.EncodeToString(frame))
}

// status takes a 0x14 status: the processor (re)started, or its source changed.
func (s *session) status(ctx context.Context, frame []byte) {
	s.radioResend.Store(true) // the processor (re)started: tell it again

	if src := binary.BigEndian.Uint32(frame[0x2c:]); src < lpmSourceCount {
		// The first status overrules what was remembered: the bar may have been
		// powered off since.
		if !s.statusSeen.Swap(true) && int64(src) != s.bar.Load() {
			s.follow(ctx, src)
		}

		s.bar.Store(int64(src))
	}

	s.l.statusMode(frame)
	s.send("tone query", lpmFrame(lpmProcessor, lpmToneQuery, nil))
}

// picked takes a 0x50: a source picked on the bar, its remote or the TV.
func (s *session) picked(ctx context.Context, frame []byte) {
	src := binary.BigEndian.Uint32(frame[lpmPayloadAt:])
	payload := make([]byte, lpmActivateLen)
	binary.BigEndian.PutUint32(payload, src)
	binary.BigEndian.PutUint32(payload[8:], 1)
	s.send("activated", lpmFrame(lpmSender(frame), lpmActivated, payload))

	if src < lpmSourceCount {
		s.log.Info("lpm picked a source", "source", src, "name", lpmSourceName(src))
		s.follow(ctx, src)
	}
}

// content takes a 0x85: play or store a preset, or a transport key.
func (s *session) content(frame []byte) {
	switch key, store := lpmContentOf(frame); {
	case store:
		_, err := s.spk.StoreCurrentAsPreset(key)
		if err != nil {
			s.log.Error("lpm store preset", "key", key, "err", err)
		}
	case key != "":
		s.spk.ApplyKey(key)
	default:
		// Nothing SoundTap acts on.
	}
}

// volumeInfo takes a 0xb0: the bar's remote or the TV changed the volume or the mute, and
// SoundTap follows. The processor keeps the level while muted and does the muting itself.
func (s *session) volumeInfo(ctx context.Context, frame []byte) {
	level, muted := lpmVolumeOf(frame)
	switch {
	case muted:
		s.barMute.Store(barMuted)
		s.spk.MarkMuted()
	case level <= lpmVolumeMax:
		s.barMute.Store(barUnmuted)

		if status := s.spk.StatusSnapshot(); level != status.Output() || status.Muted {
			s.fromLPM.Store(int64(level))
			_ = s.spk.SetVolume(ctx, level)
		}
	default:
		// Outside the processor's scale: not a level to follow.
	}
}

// followPlayback selects the processor's input for the speaker's standby, wake and
// playback until ctx ends.
func (s *session) followPlayback(ctx context.Context) {
	spk := s.spk
	playing := false

	wasStandby := spk.StatusSnapshot().Standby
	for {
		changed := spk.Changed()
		status := spk.StatusSnapshot()

		own := time.Now().UnixNano() >= s.quiet.Load()
		if source, ok := lpmPowerSource(wasStandby, status.Standby); ok && own {
			if source == lpmSourceModule && status.Source == SourceProduct {
				source = uint32(s.external.Load()) //nolint:gosec // a source below lpmSourceCount
			}

			s.activate("power", source)
		}

		wasStandby = status.Standby

		now := !status.Standby && status.Source != SourceProduct &&
			(status.PlayStatus == playPlaying || status.PlayStatus == playBuffering)
		if now && !playing && own {
			// Always sent, not only when bar says another input plays. bar is a guess that
			// the processor's status frames also write, and on 2026-11-06 a radio started
			// while the bar played TV sent nothing, so the owner had to switch by remote.
			// Only the start of playback sends it, so a bar already on this input gets one
			// extra activation, which the processor answers with 0x51.
			s.bar.Store(-1)
			s.activate("play", lpmModuleInput(status.Source))
		}

		playing = now

		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}
