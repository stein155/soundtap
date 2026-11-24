// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/childproc"
	"github.com/stein155/soundtap/app/internal/counters"
	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/player"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/supervise"
)

// playAnnouncement decodes the clip URL to raw PCM and hands it to the arbiter, which

// ducks the bus, mixes it over the top, and restores when ffmpeg (and thus the clip)
// ends.
func playAnnouncement(ctx context.Context, arb *audio.Arbiter, n *speakerstate.Notification, ampDemand *atomic.Int32, notificationReady *atomic.Bool, log *slog.Logger) {
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	ampDemand.Add(1)
	defer ampDemand.Add(-1)
	wakeOwnership()
	// Held across the whole clip, including the wait for the amp, so sourceRoute keeps
	// the codec on the PCM path until the last sample is out.
	announcementDemand(1)
	defer announcementDemand(-1)
	if !waitForAudioReady(cctx, notificationReady) {
		return
	}
	// Same probe limits as playback: a chime must not spend five seconds in
	// avformat_find_stream_info before the first sample.
	cmd := exec.CommandContext(cctx, "ffmpeg", "-nostdin", "-loglevel", "warning",
		"-probesize", "32768", "-analyzeduration", "0",
		"-rw_timeout", "15000000", "-max_redirects", "0",
		"-protocol_whitelist", player.FFmpegProtocols, "-i", player.FFmpegURL(cctx, n.URL),
		"-f", "s16le", "-ar", fmt.Sprint(audio.SampleRate), "-ac", "2",
		"-flush_packets", "1", "pipe:1")
	cmd.Stderr = supervise.ChildLog("ffmpeg")
	if err := childproc.RunAs(cmd, decoderUser); err != nil {
		log.Warn("announcement decoder", "err", err)
		return
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		log.Error("announce pipe", "err", err)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Error("announce ffmpeg start", "err", err)
		return
	}
	// Through the same curve as the music, so that a chime at the listening volume really
	// is as loud as what it interrupts. A linear volume/ceiling put one at 14 about 49 dB
	// above the music at 14.
	gain := audio.VolumeGain(n.Volume)
	log.Info("announce", "url", n.URL, "gain", gain)

	// Signal EOF so we only Wait() (which closes the stdout pipe) once the Mixer has
	// drained the whole clip — otherwise the last ~1/3s of every announcement is cut.
	done := make(chan struct{})
	arb.Announce(&eofSignal{r: out, done: done}, gain)
	select {
	case <-done:
	case <-cctx.Done():
	}
	_ = cmd.Wait()
}

func waitForAudioReady(ctx context.Context, ready *atomic.Bool) bool {
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for !ready.Load() {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
	return true
}

// eofSignal closes done the first time the wrapped reader reports EOF.
type eofSignal struct {
	r    io.Reader
	done chan struct{}
	once sync.Once
}

func (e *eofSignal) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err == io.EOF {
		e.once.Do(func() { close(e.done) })
	}
	return n, err
}

// ownershipWake gets trackOwnership out of its settled wait for demand speakerstate does
// not signal: an announcement needs the amp now, not at the next slow step.
var ownershipWake = make(chan struct{}, 1)

func wakeOwnership() {
	select {
	case ownershipWake <- struct{}{}:
	default:
	}
}

// trackOwnership follows speaker state and keeps the arbiter's exclusive owner, master
// volume, and amp mute in sync. It steps every 50 ms while anything is in motion (audio,
// an announcement, the amp or mute sequence); settled in standby it waits for a state
// change, with a slow step for what speakerstate does not signal (amp faults).
func trackOwnership(ctx context.Context, sp *speakerstate.Speaker, arb *audio.Arbiter, health audioHealth, shuttingDown *atomic.Bool, ampDemand *atomic.Int32, notificationReady *atomic.Bool, leaveMultiroom func(), log *slog.Logger) { //nolint:gocognit,cyclop,funlen // moved unchanged
	const fast, settled = 50 * time.Millisecond, time.Second
	step := fast
	last := ""
	var flowBase audio.Health
	var changedAt time.Time
	audioFlowing := false
	soundLogged := false
	var settle sinkSettle // a muted amp opens once the sink runs steadily
	amp := ampSequence{enabled: amplifier.HasPower()}
	mute := muteDelay{muted: true} // the boot script leaves the amp muted
	var idleSince time.Time        // since when the amp has had nothing to do
	for {
		changed := sp.Changed()
		select {
		case <-ctx.Done():
			notificationReady.Store(false)
			ampLines.mute(true, log)
			ampLines.power(false, log)
			return
		case <-changed:
		case <-ownershipWake:
		case <-time.After(step):
		}
		snap := sp.StatusSnapshot()
		if shuttingDown.Load() {
			continue
		}
		// Reconciliation only: sp.OnVolume already pushed this the moment it changed.
		// Kept so a missed hook (a source that mutates state another way) cannot leave
		// the gain stale for as long as playback lasts.
		arb.SetVolume(mixerLevel(snap.Output()))
		arb.SetBass(snap.Bass)
		idle := snap.Source == sourceStandby || snap.Source == ""
		if snap.Source != last { //nolint:nestif // moved unchanged; preempt/ownership only on an actual source change
			// A follower that picks another source stops following. Left running, its
			// snapclient kept writing the leader's stream into the bus under the new
			// source: the ST10 stuttered and the ST20 crackled (2026-09-29).
			if last == sourceMultiroom {
				leaveMultiroom() // reads the follow now; the leave runs on its own goroutine
			}
			// snapclient runs on MULTIROOM as a follower, and otherwise only as a leader's
			// anchor: each change may start or park it.
			if snap.Source == sourceMultiroom {
				wakeSnapclient()
			} else {
				pokeSnapclient()
			}
			last = snap.Source
			arb.SetLive(liveBuffer(snap.Source))
			auxOwnsSink.Store(snap.Source == sourceAux)
			setSourceTrim(arb, snap.Source)
			if snap.Source == sourceAux {
				arb.Unhold() // nothing to sync with; only the fade-in of a held source
			}
			flowBase = health()
			changedAt = time.Now()
			audioFlowing, soundLogged = false, false
			settle = sinkSettle{}
			if idle {
				arb.Release(arb.Owner())
			} else {
				arb.Acquire(snap.Source)
			}
			// With amp mute, amp power and codec volume, the order a pop is read from.
			log.Info("source switch", "source", snap.Source, "held", arb.Held())
		}
		if !idle && !audioFlowing {
			audioFlowing = pcmFlowStarted(flowBase, health())
		}
		// A held follower's sink carries only the join chime, not the source: its sound
		// reaches the DAC once the hold ends.
		if audioFlowing && !soundLogged && !arb.Held() {
			soundLogged = true
			log.Info("source sound reaches the DAC", "source", snap.Source)
		}
		announcing := arb.Announcing()
		ampIdle := idle && !announcing && ampDemand.Load() == 0
		now := time.Now()
		switch {
		case !ampIdle:
			idleSince = time.Time{}
		case idleSince.IsZero():
			idleSince = now
		}
		pumpRest.set(pumpMayRest(ampIdle, idleSince, now, codecOutputReady(), tvSharing.Load()))
		_, powered, ampReady := amp.step(ampIdle, now)
		if !ampLines.power(powered, log) && powered {
			amp.powerUnavailable(now)
			ampReady = false
		}
		ampFault := ampFaulted()
		codecReady := codecOutputReady()
		notificationReady.Store(ampReady && codecReady && !ampFault)
		starting := holdForStart(idle, mute.muted, arb.Held(), now.Sub(changedAt))
		// A muted amp opens on sound that keeps coming, not on its first block: after the
		// pump's rest the DAC can still run dry once while it starts (an ST20 underrun
		// right after the unmute, heard as a pop on 2026-11-22).
		sounding := audioFlowing && (!mute.muted || settle.step(health().SinkNonzeroBlocks, counters.ALSAUnderruns.Load()))
		want := shouldMute(ampFault, ampIdle, codecReady, sounding || announcing || starting, ampReady)
		muted := mute.step(want, ampFault || !powered, now)
		ampLines.mute(muted, log)
		step = fast
		if ampIdle && !powered && muted && want {
			step = settled
		}
		// Steady playback is settled too: a source change, an announcement and an amp
		// fault all wake or act without this loop, and 20 wakeups a second cost CPU the
		// multiroom leader does not have.
		if !idle && audioFlowing && !announcing && ampReady && !muted && !want {
			step = settled
		}
	}
}

func (a *ampSequence) powerUnavailable(now time.Time) {
	a.ready = false
	a.warm = false
	a.changedAt = now
}

// ampSequence keeps the ST20 amp muted around its slow standby line without blocking
// the ownership loop. Stock gives the amplifier three seconds to shut down and wake.
type ampSequence struct {
	enabled     bool
	initialized bool
	idle        bool
	powered     bool
	ready       bool
	warm        bool
	changedAt   time.Time
}

// step returns whether the amp-power GPIO must change, its new value, and whether
// the mute may be released. Initialization always reconciles power explicitly because
// the service supervisor can restart the daemon after a prior process powered it down.
func (a *ampSequence) step(idle bool, now time.Time) (bool, bool, bool) {
	if !a.enabled {
		return false, false, true
	}
	if !a.initialized {
		a.initialized = true
		a.idle = idle
		a.powered = true
		a.ready = false
		a.changedAt = now
		return true, true, a.ready
	}
	if idle != a.idle {
		a.idle = idle
		a.changedAt = now
		a.ready = false
		if !idle && !a.powered {
			a.powered = true
			a.warm = false
			return true, true, false
		}
		if !idle {
			a.ready = a.warm // a short standby does not cool a powered amplifier
		}
		return false, a.powered, a.ready
	}
	if idle && a.powered && now.Sub(a.changedAt) >= ampPowerDelay {
		a.powered = false
		a.warm = false
		return true, false, false
	}
	if !idle && a.powered && !a.ready && now.Sub(a.changedAt) >= ampPowerDelay {
		a.ready = true
		a.warm = true
	}
	return false, a.powered, a.ready
}

// shouldMute is the amp mute decision trackOwnership applies on every tick.
//
// An asserted amp fault has to be part of this expression, not merely a one-shot
// setMute from runAmpFault: trackOwnership rewrites the mute GPIO every 50 ms, so a
// mute it does not know about is undone within one tick and the protection does
// nothing. While the fault is latched the amp stays muted whatever the source state
// says; runAmpFault clears the latch on the falling edge and the normal state resumes
// on the next tick.
//
// The rest keeps the amp off while the DAC carries only startup silence — ST20
// otherwise emits a loud transient before the decoder's first PCM block reaches McASP.
func shouldMute(ampFault, idle, codecReady, audioFlowing, ampReady bool) bool {
	return ampFault || idle || !codecReady || !audioFlowing || !ampReady
}

// unmuteSettleBlocks is how much sound (in Mixer blocks, about 300 ms) the sink carries
// without an ALSA underrun before a muted amp opens on it.
const unmuteSettleBlocks = 15

// sinkSettle times that window. An underrun inside it starts the window again, so the
// amp opens only on a DAC that has kept up for the whole of it.
type sinkSettle struct {
	started     bool
	base, xruns uint64
}

// step takes the sink's nonzero block count and the ALSA underrun count, and reports
// whether the window is over.
func (s *sinkSettle) step(sinkBlocks, xruns uint64) bool {
	if !s.started || xruns != s.xruns {
		s.started, s.base, s.xruns = true, sinkBlocks, xruns
	}
	return sinkBlocks-s.base >= unmuteSettleBlocks
}

// holdForStart reports whether a source that has not produced sound yet still counts as
// playing: only while it starts, and only if the amp was not already muted. A held
// multiroom follower is still starting until it is in sync, which takes longer than
// sourceStartHold: muting it on the way and unmuting at the fade-in popped the ST20.
func holdForStart(idle, muted, held bool, sinceChange time.Duration) bool {
	return !idle && !muted && (held || sinceChange < sourceStartHold)
}

// muteDelay holds a mute back by ampMuteDelay, so the amplifier is cut on silence
// rather than on the tail of what just stopped. A fault, or an amplifier losing power,
// mutes at once; so does anything that stays wanted, and a want that goes away inside
// the delay (a quick source switch) never mutes at all. Unmuting is never delayed.
type muteDelay struct {
	muted     bool
	wantSince time.Time
}

func (m *muteDelay) step(want, urgent bool, now time.Time) bool {
	switch {
	case !want:
		m.muted = false
		m.wantSince = time.Time{}
	case m.muted:
	case urgent:
		m.muted = true
	case m.wantSince.IsZero():
		m.wantSince = now
	case now.Sub(m.wantSince) >= ampMuteDelay:
		m.muted = true
	}
	return m.muted
}

func pcmFlowStarted(base, current audio.Health) bool {
	return current.SourceNonzeroBlocks > base.SourceNonzeroBlocks &&
		current.SinkNonzeroBlocks > base.SinkNonzeroBlocks
}

// ensureFIFO makes a FIFO at path if it isn't one already.
func ensureFIFO(path string) error {
	if fi, err := os.Stat(path); err == nil {
		if fi.Mode()&os.ModeNamedPipe != 0 {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	return mkfifo(path)
}

// ensureDACStreaming claims the McASP DMA clock so raw writes to /dev/snd/snd_shelby drain.
// Ownership tracking controls mute separately; opening the DAC must stay silent in standby.
// Best-effort: on a device we don't manage (dev != the shelby DAC) or in QEMU the sysfs
// paths are absent and the writes no-op with a warning.
func ensureDACStreaming(dev string, log *slog.Logger) {
	if dev != shelbyDev {
		return // custom AUDIO_DEV (test/tap) — don't poke the real DAC's sysfs
	}
	initAudio(log) // aic3256 codec config (idempotent; skipped if AUDIO_NOINIT)
	if shelbySource != "" {
		_ = hw.SysfsWrite(shelbySource, "wifi", log) // McASP DMA clock — written directly, not AUDIO_NOINIT-gated
	}
}

func mkfifo(path string) error { return syscall.Mkfifo(path, 0o600) }

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}
