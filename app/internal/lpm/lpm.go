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
	lpmDevice          = "/dev/ttyS0"
	lpmFrameLen        = 128
	lpmModule          = 6
	lpmProcessor       = 2
	lpmEcho            = 0x01
	lpmEchoReply       = 0x02
	lpmHealthAsk       = 0x11
	lpmHealth          = 0x13 // {version string, 23 bytes and a NUL}
	lpmStatus          = 0x14
	lpmVolumeInfo      = 0xb0
	lpmSpeakerList     = 0xb4
	lpmTone            = 0x8a // tone: bass, treble, centre, rear as BE int16
	lpmToneQuery       = 0x8b // answered with 0x8a
	lpmActivate        = 0x50
	lpmActivated       = 0x51 // stock's answer to the processor's 0x50: {source, 0, 1}
	lpmKey             = 0x05
	lpmUpdateDone      = 0x0c // stock U-Boot's `gingeripc 1`: the update attempt is over
	lpmTapCommand      = 0x09 // stock's TAP passthrough: a line for the processor's console; byte 3 = 1 on the last part
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
	playPlaying      = "PLAY_STATE"
	playBuffering    = "BUFFERING_STATE"
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
	f := make([]byte, lpmFrameLen)
	f[0] = lpmModule<<4 | dst&0xf
	f[1] = op
	copy(f[4:4+lpmPayloadMaxLen], payload)
	binary.LittleEndian.PutUint32(f[lpmFrameLen-4:], lpmChecksum(f))
	return f
}

func lpmValid(f []byte) bool {
	return len(f) == lpmFrameLen && binary.LittleEndian.Uint32(f[lpmFrameLen-4:]) == lpmChecksum(f)
}

// lpmActivateSource is stock's ActivateSource: {source, 0, 0} to the processor.
func lpmActivateSource(source uint32) []byte {
	p := make([]byte, 12)
	binary.BigEndian.PutUint32(p, source)
	return lpmFrame(lpmProcessor, lpmActivate, p)
}

// lpmFrames reads frames off r, sliding one byte at a time past anything whose checksum
// does not hold, so a start mid-frame resynchronises.
func lpmFrames(r io.Reader, frame func([]byte)) error {
	buf := make([]byte, lpmFrameLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	for {
		if lpmValid(buf) {
			frame(bytes.Clone(buf))
			if _, err := io.ReadFull(r, buf); err != nil {
				return err
			}
			continue
		}
		copy(buf, buf[1:])
		if _, err := io.ReadFull(r, buf[lpmFrameLen-1:]); err != nil {
			return err
		}
	}
}

// Keys takes the keys the processor reports: its buttons' and its remote's.
type Keys interface {
	// Key is one edge of the key at index; producer is the processor's input that sent it,
	// for the log of a confirming hold.
	Key(index int, pressed bool, action string, producer uint32)
}

// Run opens the link and keeps it until ctx ends: it answers the processor, follows the
// source, volume and keys it reports into sp and keys, and selects the module's input
// when sp plays.
func (l *Link) Run(ctx context.Context, sp *speakerstate.Speaker, keys Keys, log *slog.Logger) {
	if err := exec.CommandContext(ctx, "busybox", "stty", "-F", lpmDevice, "921600", "raw", "-echo").Run(); err != nil {
		log.Error("lpm serial setup", "err", err)
		return
	}
	f, err := os.OpenFile(lpmDevice, os.O_RDWR, 0)
	if err != nil {
		log.Error("lpm open", "err", err)
		return
	}
	context.AfterFunc(ctx, func() { _ = f.Close() })
	s := &session{l: l, ctx: ctx, sp: sp, keys: keys, log: log}
	s.send = func(what string, frame []byte) {
		log.Debug("lpm send", "what", what, "frame", hex.EncodeToString(frame))
		if _, err := f.Write(frame); err != nil {
			log.Error("lpm "+what, "err", err)
		}
	}
	s.start()
	safego.Go("lpmRadio", s.radioLoop)
	safego.Go("lpmReader", func() {
		if err := lpmFrames(f, s.frame); ctx.Err() == nil {
			log.Error("lpm read", "err", err)
		}
	})
	s.followPlayback()
}

// session is one run of the link: what the module knows of the processor while it talks
// to it.
type session struct {
	l    *Link
	ctx  context.Context
	sp   *speakerstate.Speaker
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
}

// start sets the session up before the reader runs: the volume hand-over, the saved
// choices and input, and the module's first messages to the processor.
func (s *session) start() {
	l, sp := s.l, s.sp
	s.fromLPM.Store(-1)
	sp.OnVolume(s.volumeChanged)
	for _, c := range []*Choice{l.AudioModes, l.CECModes, l.HDMIInputs} {
		c.Load()
	}
	send := s.send
	l.send.Store(&send)
	// The recovery combo puts the processor in update mode and it says nothing until told
	// the attempt is over; stock U-Boot does that, ours does not.
	if b, _ := os.ReadFile("/proc/cmdline"); lpmAfterRecovery(string(b)) {
		s.send("update done", lpmFrame(lpmProcessor, lpmUpdateDone, nil))
	}
	s.send("health", lpmHealthAnswer(lpmProcessor, "SoundTap "+l.version))
	l.ask("speaker query", lpmSpeakerQuery)
	// Tone too: otherwise only a 0x14 status asks for it, and after a boot the processor
	// may send none, so the app's first tone read failed with "has not answered yet".
	l.ask("tone query", lpmToneQuery)

	s.bar.Store(-1)
	s.external.Store(lpmSourceTV)
	pick := s.pick
	l.pick.Store(&pick)
	// Before the reader starts, so nothing goes to the processor: it is already playing this.
	if source, ok := l.SavedInput(); ok {
		s.bar.Store(int64(source))
		s.external.Store(int64(source))
		_ = sp.Select(s.ctx, SourceProduct, "", "", lpmSourceNames[source], "", "")
		s.log.Info("lpm input from before the restart", "source", source, "name", lpmSourceNames[source])
	}
}

// volumeChanged hands a volume change on the speaker to the processor once it owns the
// volume.
func (s *session) volumeChanged(level int) {
	if !s.l.ownsVolume.Load() {
		return
	}
	if fr := lpmMuteFrame(s.sp.StatusSnapshot().Muted, &s.barMute); fr != nil {
		s.send("mute", fr)
	}
	if lpmForwardVolume(level, &s.fromLPM) {
		s.send("volume", lpmSetVolume(level))
	}
}

func (s *session) remember(source uint32) {
	if err := s.l.SaveSource(source); err != nil {
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
func (s *session) follow(source uint32) {
	ctx, sp := s.ctx, s.sp
	s.quiet.Store(time.Now().Add(2 * time.Second).UnixNano())
	s.bar.Store(int64(source))
	s.remember(source)
	st := sp.StatusSnapshot()
	switch source {
	case lpmSourceStandby:
		_ = sp.EnterStandby(ctx)
	case lpmSourceBT:
		// The bar's own Bluetooth input is the module's: SoundTap's Bluetooth plays it,
		// and selecting it wakes the speaker as the BLUETOOTH key does on the other models.
		if st.Source != sourceBluetooth {
			sp.ApplyKey(sourceBluetooth)
		}
	case lpmSourceModule:
		if st.Source == SourceProduct {
			_ = sp.ReleaseSource(ctx, SourceProduct)
			st.Standby = true
		}
		if st.Standby {
			sp.ApplyKey("POWER")
		}
	default:
		if source != lpmSourceADAPTiQ { // waking must not start a calibration again
			s.external.Store(int64(source))
		}
		_ = sp.Select(ctx, SourceProduct, "", "", lpmSourceNames[source], "", "")
	}
}

// pick switches the bar to one of the processor's own inputs, as its remote does.
func (s *session) pick(source uint32) {
	s.bar.Store(-1)
	s.activate("app", source)
	s.follow(source)
}

// radioLoop keeps the processor told that the module is there and what its network is.
func (s *session) radioLoop() {
	radio := &lpmRadio{
		mhz:  func() int { return wifiMHz(s.ctx) },
		wifi: func() hw.Link { return hw.ReadLink(func() bool { return s.sp.StatusSnapshot().IP != "" }) },
		eth0: speakerstate.Eth0,
		send: s.send,
	}
	silent := false
	for {
		silent = s.checkSilence(silent)
		if fr, news := radio.tick(time.Now(), s.radioResend.Swap(false)); news {
			s.log.Info("lpm radio status", "mhz", binary.BigEndian.Uint32(fr[4:]), "net", fr[12])
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// checkSilence resends the health message while the processor is quiet, and reboots the
// bar once a processor that talked before has given the module up. silent is whether the
// last check found it silent; it returns whether this one did.
func (s *session) checkSilence(silent bool) bool {
	// After its own reset the processor waits for the module's health message and
	// asks for nothing; stock sends one at start, and a processor that rebooted
	// later never got another. After its timeout it gives the module up for good and
	// blinks two lamps orange (2026-11-10). So while it is quiet, tell it again.
	t := s.lastFrame.Load()
	if t == 0 || time.Since(time.Unix(0, t)) > lpmSilence {
		s.send("health", lpmHealthAnswer(lpmProcessor, "SoundTap "+s.l.version))
	}
	if t == 0 {
		return silent
	}
	quiet := time.Since(time.Unix(0, t))
	if quiet > lpmSilence && !silent {
		s.log.Warn("lpm processor silent", "since", time.Unix(0, t).UTC().Format(time.RFC3339))
	}
	// Once per boot: the reboot takes the module down too, and a processor that is
	// silent from the start never sets lastFrame, so this cannot loop.
	if quiet > lpmGiveUp {
		s.log.Error("lpm processor gave up on the module; rebooting the bar", "silent", quiet.Round(time.Second))
		s.send("reboot", lpmTapLine("rb 0"))
		s.lastFrame.Store(0)
	}
	return quiet > lpmSilence
}

// frame handles one frame from the processor.
func (s *session) frame(fr []byte) {
	l := s.l
	s.lastFrame.Store(time.Now().UnixNano())
	// The bar keeps the level it already plays at: SoundTap takes it over from the
	// next 0xb0 rather than pushing its own, which after an install or a reboot could
	// be far louder than what the room was listening to. Stock sends no 0xb2 here
	// either (strace 2026-11-06).
	if fr[0]>>4 == lpmProcessor && !l.ownsVolume.Swap(true) {
		s.log.Info("lpm answered; volume handed to the processor")
		l.alive.Store(true)
	}
	switch fr[1] {
	case lpmEcho:
		s.send("echo", lpmEchoAnswer(fr))
	case lpmHealthAsk:
		s.send("health", lpmHealthAnswer(fr[0]>>4, "SoundTap "+l.version))
	case lpmStatus:
		s.status(fr)
	case lpmCECModeReport:
		l.CECModes.report(uint32(fr[4]))
	case lpmHDMIInputReport:
		l.HDMIInputs.report(uint32(fr[4]))
	case lpmActivate:
		s.picked(fr)
	case lpmKey:
		lpmKeyEvent(s.keys, fr)
	case lpmContent:
		s.content(fr)
	case lpmVolumeInfo:
		s.volumeInfo(fr)
	case lpmTone:
		tone := lpmToneOf(fr)
		l.now.Store(&tone)
		s.log.Info("lpm tone", "tone", tone)
	case lpmSpeakerList:
		bar := lpmSpeakers(fr)
		l.bar.Store(&bar)
		s.log.Info("lpm speakers", "bar", bar)
	}
	s.logFrame(fr)
}

// logFrame logs the first frame of each op, and every frame at debug.
func (s *session) logFrame(fr []byte) {
	if s.seen == nil {
		s.seen = map[byte]bool{}
	}
	if !s.seen[fr[1]] {
		s.seen[fr[1]] = true
		s.log.Info("lpm first frame", "op", fr[1], "hdr", fr[0], "head", hex.EncodeToString(fr[:24]))
	}
	s.log.Debug("lpm frame", "op", fr[1], "frame", hex.EncodeToString(fr))
}

// status takes a 0x14 status: the processor (re)started, or its source changed.
func (s *session) status(fr []byte) {
	s.radioResend.Store(true) // the processor (re)started: tell it again
	if src := binary.BigEndian.Uint32(fr[0x2c:]); src < lpmSourceCount {
		// The first status overrules what was remembered: the bar may have been
		// powered off since.
		if !s.statusSeen.Swap(true) && int64(src) != s.bar.Load() {
			s.follow(src)
		}
		s.bar.Store(int64(src))
	}
	s.l.statusMode(fr)
	s.send("tone query", lpmFrame(lpmProcessor, lpmToneQuery, nil))
}

// picked takes a 0x50: a source picked on the bar, its remote or the TV.
func (s *session) picked(fr []byte) {
	src := binary.BigEndian.Uint32(fr[4:])
	p := make([]byte, 12)
	binary.BigEndian.PutUint32(p, src)
	binary.BigEndian.PutUint32(p[8:], 1)
	s.send("activated", lpmFrame(fr[0]>>4, lpmActivated, p))
	if src < lpmSourceCount {
		s.log.Info("lpm picked a source", "source", src, "name", lpmSourceNames[src])
		s.follow(src)
	}
}

// content takes a 0x85: play or store a preset, or a transport key.
func (s *session) content(fr []byte) {
	switch key, store := lpmContentOf(fr); {
	case store:
		if _, err := s.sp.StoreCurrentAsPreset(key); err != nil {
			s.log.Error("lpm store preset", "key", key, "err", err)
		}
	case key != "":
		s.sp.ApplyKey(key)
	}
}

// volumeInfo takes a 0xb0: the bar's remote or the TV changed the volume or the mute, and
// SoundTap follows. The processor keeps the level while muted and does the muting itself.
func (s *session) volumeInfo(fr []byte) {
	level, muted := lpmVolumeOf(fr)
	switch {
	case muted:
		s.barMute.Store(1)
		s.sp.MarkMuted()
	case level <= 100:
		s.barMute.Store(2)
		if st := s.sp.StatusSnapshot(); level != st.Output() || st.Muted {
			s.fromLPM.Store(int64(level))
			_ = s.sp.SetVolume(s.ctx, level)
		}
	}
}

// followPlayback selects the processor's input for the speaker's standby, wake and
// playback until ctx ends.
func (s *session) followPlayback() {
	sp := s.sp
	playing := false
	wasStandby := sp.StatusSnapshot().Standby
	for {
		changed := sp.Changed()
		st := sp.StatusSnapshot()
		own := time.Now().UnixNano() >= s.quiet.Load()
		if source, ok := lpmPowerSource(wasStandby, st.Standby); ok && own {
			if source == lpmSourceModule && st.Source == SourceProduct {
				source = uint32(s.external.Load()) //nolint:gosec // a source below lpmSourceCount
			}
			s.activate("power", source)
		}
		wasStandby = st.Standby
		now := !st.Standby && st.Source != SourceProduct &&
			(st.PlayStatus == playPlaying || st.PlayStatus == playBuffering)
		if now && !playing && own {
			// Always sent, not only when bar says another input plays. bar is a guess that
			// the processor's status frames also write, and on 2026-11-06 a radio started
			// while the bar played TV sent nothing, so the owner had to switch by remote.
			// Only the start of playback sends it, so a bar already on this input gets one
			// extra activation, which the processor answers with 0x51.
			s.bar.Store(-1)
			s.activate("play", lpmModuleInput(st.Source))
		}
		playing = now
		select {
		case <-s.ctx.Done():
			return
		case <-changed:
		}
	}
}
