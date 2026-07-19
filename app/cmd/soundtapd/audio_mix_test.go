// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/hw/model"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func TestOpenAudioSinkModes(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sink, paced, closeSink, err := openAudioSink(context.Background(), "-", defaultSinkTiming, log)
	if err != nil || sink == nil || !paced || closeSink() != nil {
		t.Fatalf("discard sink = (%v, %v, %v)", sink, paced, err)
	}
	if _, _, _, err := openAudioSink(context.Background(), "alsa:", defaultSinkTiming, log); err == nil {
		t.Fatal("empty ALSA target accepted")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aplay"), []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, paced, closeSink, err = openAudioSink(context.Background(), "alsa:plughw:0,0", defaultSinkTiming, log)
	if err != nil {
		t.Fatal(err)
	}
	if paced {
		t.Fatal("ALSA pipe must use hardware backpressure as its only clock")
	}
	if err := closeSink(); err != nil {
		t.Fatal(err)
	}
}

// The WLA's analog card is the same 46875 Hz S32_LE McASP link as the ST10's. Left on
// S16 it applied low volumes in 16 bits and zeroed quiet passages at 2 %.
func TestWLAFirmwareSinkUsesS32(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$ARGS\"\ncat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, "aplay"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ARGS", args)
	t.Setenv("SOUNDTAP_FIRMWARE", "1")
	old := board
	board = model.Named(model.WLA)
	defer func() { board = old }()

	_, _, closeSink, err := openAudioSink(context.Background(), board.AudioDev, defaultSinkTiming, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if err := closeSink(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if fields := string(got); !bytes.Contains(got, []byte("S32_LE\n-r\n46875\n")) ||
		!bytes.Contains(got, []byte("plughw:0,0")) {
		t.Fatalf("WLA aplay args = %q", fields)
	}
}

// The ST10 on the mainline kernel is a plughw speaker like the Wireless Link: its McASP
// link runs at 46875 Hz only, so the raw device at the nominal rate cannot be opened at
// all. Opening hw:0,0 here is what left "Broken configuration for this PCM" in the log
// and restarted the mixer pump every two seconds.
func TestST10FirmwareSinkKeepsPlughw(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$ARGS\"\ncat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, "aplay"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ARGS", args)
	t.Setenv("SOUNDTAP_FIRMWARE", "1")
	old := board
	board = model.Named(model.ST10)
	defer func() { board = old }()

	_, _, closeSink, err := openAudioSink(context.Background(), "alsa:plughw:0,0", defaultSinkTiming, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if err := closeSink(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	// plughw stays for safety, but at the card's own format and rate it converts nothing.
	if !bytes.Contains(got, []byte("plughw:0,0")) || !bytes.Contains(got, []byte("S32_LE\n-r\n46875\n")) {
		t.Fatalf("ST10 aplay args = %q", got)
	}
}

// The ST20's hw:0,0 accepts 48000 and quietly runs at 46875, so the rate has to be
// asked for, not left to aplay's "nearest".
func TestST20FirmwareSinkOpensTheCardRate(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$ARGS\"\ncat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, "aplay"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ARGS", args)
	t.Setenv("SOUNDTAP_FIRMWARE", "1")
	old := board
	board = model.Named(model.ST20)
	defer func() { board = old }()

	_, _, closeSink, err := openAudioSink(context.Background(), "alsa:plughw:0,0", defaultSinkTiming, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if err := closeSink(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("hw:0,0")) || !bytes.Contains(got, []byte("S32_LE\n-r\n46875\n")) {
		t.Fatalf("ST20 aplay args = %q", got)
	}
}

func TestTapFailureDoesNotBreakPrimaryAudio(t *testing.T) {
	var primary bytes.Buffer
	p := []byte("pcm")
	n, err := (&tapWriter{primary: &primary, tapFD: -1, on: new(atomic.Bool)}).Write(p)
	if err != nil || n != len(p) || !bytes.Equal(primary.Bytes(), p) {
		t.Fatalf("write = (%d, %v, %q)", n, err, primary.Bytes())
	}
}

func TestTapCarriesAudioBeforeTheVolume(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	p := []byte{0x00, 0x40, 0x00, 0xc0} // two S16 samples, +0.5 and -0.5
	var primary bytes.Buffer
	tap := &tapWriter{primary: &primary, tapFD: int(w.Fd()), on: new(atomic.Bool)}
	// Solo: the DAC gets its audio, the tap nothing.
	tap.Tap([]byte{1, 2, 3, 4})
	if _, err := tap.WriteGain([]byte{1, 2, 3, 4}, 0.1); err != nil || primary.Len() != 4 {
		t.Fatalf("solo write = %v, primary %d bytes", err, primary.Len())
	}
	tap.on.Store(true)
	tap.Tap(p)
	if _, err := tap.WriteGain(p, 0.1); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(p))
	if _, err := io.ReadFull(r, got); err != nil || !bytes.Equal(got, p) {
		t.Fatalf("tap = %x (%v), want the unscaled %x and nothing from the solo write", got, err, p)
	}
}

func TestPCMFlowRequiresSourceAndSink(t *testing.T) {
	base := audio.Health{SourceNonzeroBlocks: 4, SinkNonzeroBlocks: 7}
	if pcmFlowStarted(base, audio.Health{SourceNonzeroBlocks: 5, SinkNonzeroBlocks: 7}) {
		t.Fatal("source-only flow opened mute")
	}
	if pcmFlowStarted(base, audio.Health{SourceNonzeroBlocks: 4, SinkNonzeroBlocks: 8}) {
		t.Fatal("sink-only flow opened mute")
	}
	if !pcmFlowStarted(base, audio.Health{SourceNonzeroBlocks: 5, SinkNonzeroBlocks: 8}) {
		t.Fatal("source+sink flow did not open mute")
	}
}

func TestS32WriterExpandsSamples(t *testing.T) {
	var dst bytes.Buffer
	p := []byte{0x01, 0x80, 0xff, 0x7f}
	n, err := audio.NewS32Writer(&dst, audio.SampleRate).Write(p)
	want := []byte{0, 0, 0x01, 0x80, 0, 0, 0xff, 0x7f}
	if err != nil || n != len(p) || !bytes.Equal(dst.Bytes(), want) {
		t.Fatalf("write = (%d, %v, %x), want %x", n, err, dst.Bytes(), want)
	}
}

func TestDelayWriter(t *testing.T) {
	var dst bytes.Buffer
	var blocks atomic.Int64
	blocks.Store(1)
	w := delayWriter{w: &dst, blocks: &blocks}
	if _, err := w.Write([]byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte{3, 4}); err != nil {
		t.Fatal(err)
	}
	if got, want := dst.Bytes(), []byte{0, 0, 1, 2}; !bytes.Equal(got, want) {
		t.Fatalf("delayed = %v, want %v", got, want)
	}
}

// TestShouldMute pins the mute decision, above all that a latched amp fault keeps the
// amp muted even when a source is playing and PCM is flowing. trackOwnership rewrites
// the mute GPIO every 50 ms, so leaving the fault out of this expression silently
// undoes the protection runAmpFault applies on the rising edge.
func TestShouldMute(t *testing.T) {
	for _, tc := range []struct {
		name                                          string
		ampFault, idle, codecReady, flowing, ampReady bool
		want                                          bool
	}{
		{name: "playing normally", codecReady: true, flowing: true, ampReady: true, want: false},
		{name: "amp fault while playing", ampFault: true, codecReady: true, flowing: true, ampReady: true, want: true},
		{name: "amp fault while idle", ampFault: true, idle: true, ampReady: true, want: true},
		{name: "idle", idle: true, codecReady: true, flowing: true, ampReady: true, want: true},
		{name: "codec not ready", flowing: true, ampReady: true, want: true},
		{name: "no pcm flowing yet", codecReady: true, ampReady: true, want: true},
		{name: "amp waking", codecReady: true, flowing: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldMute(tc.ampFault, tc.idle, tc.codecReady, tc.flowing, tc.ampReady); got != tc.want {
				t.Errorf("shouldMute(fault=%v, idle=%v, codec=%v, flowing=%v) = %v, want %v",
					tc.ampFault, tc.idle, tc.codecReady, tc.flowing, got, tc.want)
			}
		})
	}
}

func TestMuteDelay(t *testing.T) {
	now := time.Unix(100, 0)
	m := muteDelay{muted: true}
	if m.step(true, false, now) != true {
		t.Fatal("a muted amp was unmuted")
	}
	if m.step(false, false, now) != false {
		t.Fatal("unmute was delayed")
	}
	stop := now.Add(time.Minute)
	if m.step(true, false, stop) || m.step(true, false, stop.Add(ampMuteDelay-time.Millisecond)) {
		t.Fatal("muted before the delay, on the tail of the audio")
	}
	if !m.step(true, false, stop.Add(ampMuteDelay)) {
		t.Fatal("not muted after the delay")
	}
	m.step(false, false, stop.Add(2*time.Second))
	if m.step(true, false, stop.Add(3*time.Second)) || m.step(false, false, stop.Add(3500*time.Millisecond)) {
		t.Fatal("a quick source switch muted the amp")
	}
	if m.step(true, false, stop.Add(4*time.Second)) || !m.step(true, true, stop.Add(4050*time.Millisecond)) {
		t.Fatal("a fault or power loss did not mute at once")
	}
}

func TestAmpSequenceStandbyAndWake(t *testing.T) {
	now := time.Unix(100, 0)
	amp := ampSequence{enabled: true}
	if changed, powered, ready := amp.step(true, now); !changed || !powered || ready {
		t.Fatalf("initial standby = changed %v powered %v ready %v", changed, powered, ready)
	}
	if changed, _, _ := amp.step(true, now.Add(ampPowerDelay-time.Millisecond)); changed {
		t.Fatal("amp powered down before standby delay")
	}
	if changed, powered, ready := amp.step(true, now.Add(ampPowerDelay)); !changed || powered || ready {
		t.Fatalf("power-down = changed %v powered %v ready %v", changed, powered, ready)
	}
	wake := now.Add(4 * time.Second)
	if changed, powered, ready := amp.step(false, wake); !changed || !powered || ready {
		t.Fatalf("wake edge = changed %v powered %v ready %v", changed, powered, ready)
	}
	if _, _, ready := amp.step(false, wake.Add(ampPowerDelay-time.Millisecond)); ready {
		t.Fatal("amp became ready before wake delay")
	}
	if changed, powered, ready := amp.step(false, wake.Add(ampPowerDelay)); changed || !powered || !ready {
		t.Fatalf("wake complete = changed %v powered %v ready %v", changed, powered, ready)
	}
}

func TestAmpSequenceExplicitlyPowersOnAfterRestart(t *testing.T) {
	amp := ampSequence{enabled: true}
	now := time.Unix(300, 0)
	if changed, powered, ready := amp.step(false, now); !changed || !powered || ready {
		t.Fatalf("initial wake = changed %v powered %v ready %v", changed, powered, ready)
	}
	if _, _, ready := amp.step(false, now.Add(ampPowerDelay)); !ready {
		t.Fatal("initial wake did not become ready after delay")
	}
}

func TestAmpSequenceCancelsShortStandby(t *testing.T) {
	now := time.Unix(200, 0)
	amp := ampSequence{enabled: true}
	amp.step(false, now)
	amp.step(false, now.Add(ampPowerDelay))
	amp.step(true, now.Add(4*time.Second))
	if changed, powered, ready := amp.step(false, now.Add(5*time.Second)); changed || !powered || !ready {
		t.Fatalf("short standby wake = changed %v powered %v ready %v", changed, powered, ready)
	}
}

func TestAmpSequenceEarlyWakeStillWarmsUp(t *testing.T) {
	now := time.Unix(250, 0)
	amp := ampSequence{enabled: true}
	amp.step(true, now)
	if changed, powered, ready := amp.step(false, now.Add(time.Second)); changed || !powered || ready {
		t.Fatalf("early wake = changed %v powered %v ready %v", changed, powered, ready)
	}
}

func TestAmpSequenceWaitsAfterPowerWriteRecovers(t *testing.T) {
	now := time.Unix(275, 0)
	amp := ampSequence{enabled: true}
	amp.step(false, now)
	amp.powerUnavailable(now.Add(ampPowerDelay))
	if _, _, ready := amp.step(false, now.Add(2*ampPowerDelay-time.Millisecond)); ready {
		t.Fatal("amp became ready before three seconds of successfully applied power")
	}
	if _, _, ready := amp.step(false, now.Add(2*ampPowerDelay)); !ready {
		t.Fatal("amp did not become ready after recovered power delay")
	}
}

func TestWaitForAudioReady(t *testing.T) {
	var ready atomic.Bool
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		time.Sleep(20 * time.Millisecond)
		ready.Store(true)
	}()
	if !waitForAudioReady(ctx, &ready) {
		t.Fatal("readiness wait did not observe ready state")
	}

	ready.Store(false)
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if waitForAudioReady(cancelled, &ready) {
		t.Fatal("readiness wait ignored cancellation")
	}
}

func TestAnnouncementReplacementCancelsPreviousLifetime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var lifetime announcementLifetime
	started := make(chan struct{})
	stopped := make(chan struct{})
	lifetime.Start(ctx, func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(stopped)
	})
	<-started
	lifetime.Start(ctx, func(context.Context) {})
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("replaced announcement lifetime leaked")
	}
}

func TestAnnouncementStopEndsThePlayingOne(t *testing.T) {
	var lifetime announcementLifetime
	lifetime.Stop() // nothing playing yet: a no-op
	started, stopped := make(chan struct{}), make(chan struct{})
	lifetime.Start(context.Background(), func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(stopped)
	})
	<-started
	lifetime.Stop()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("standby left the announcement playing")
	}
}

// The rate converter that feeds this writer emits 937 and 938 frame blocks in turn, so
// the delay queue is handed blocks of two sizes. It used to copy each new block into the
// slot the previous one left behind: a longer block lost its tail while Write still
// claimed the whole of it, and a shorter one left the previous block's tail in place. The
// delayed stream then carried audio that was never written, or was written twice.
func TestDelayWriterHandlesBlocksOfDifferentSizes(t *testing.T) {
	var dst bytes.Buffer
	var blocks atomic.Int64
	blocks.Store(1)
	w := delayWriter{w: &dst, blocks: &blocks}

	long, short, again := []byte{1, 2, 3, 4}, []byte{5, 6}, []byte{7, 8, 9, 10}
	for _, block := range [][]byte{long, short, again} {
		n, err := w.Write(block)
		if err != nil {
			t.Fatal(err)
		}
		if n != len(block) {
			t.Fatalf("Write(%d bytes) = %d; a short count would be a silent drop", len(block), n)
		}
	}

	// One block of delay: silence, then each block in turn, whole and unmixed.
	want := []byte{0, 0, 0, 0, 1, 2, 3, 4, 5, 6}
	if got := dst.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("delayed stream = %v, want %v", got, want)
	}
}

func TestHoldForStartKeepsAPlayingAmpOpenAcrossASwitch(t *testing.T) {
	for _, c := range []struct {
		idle, muted, held bool
		since             time.Duration
		want              bool
	}{
		{false, false, false, 2 * time.Second, true},  // TuneIn -> Spotify preset, still loading
		{false, false, false, sourceStartHold, false}, // a source that never starts may still mute
		{false, false, true, 10 * time.Second, true},  // a follower held until in sync
		{false, true, true, 0, false},                 // from standby: stays muted until sound
		{false, true, false, 0, false},
		{true, false, false, 0, false}, // going to standby mutes as before
	} {
		if got := holdForStart(c.idle, c.muted, c.held, c.since); got != c.want {
			t.Errorf("holdForStart(%v, %v, %v, %v) = %v, want %v", c.idle, c.muted, c.held, c.since, got, c.want)
		}
	}
}

// A follower stays silent until it is in sync, often longer than sourceStartHold. The amp
// that played the previous source must stay open through that, as it does for a local
// source that is still loading, rather than mute and unmute again at the fade-in.
func TestFollowerStartDoesNotToggleTheMute(t *testing.T) {
	mutes := func(held bool) int {
		mute := muteDelay{} // unmuted: the previous source was playing
		start := time.Unix(0, 0)
		toggles := 0
		for since := time.Duration(0); since < 10*time.Second; since += 50 * time.Millisecond {
			flowing := since >= 8*time.Second // in sync and fading in after 8 s
			starting := holdForStart(false, mute.muted, held && !flowing, since)
			want := shouldMute(false, false, true, flowing || starting, true)
			if was := mute.muted; mute.step(want, false, start.Add(since)) != was {
				toggles++
			}
		}
		return toggles
	}
	if n := mutes(true); n != 0 {
		t.Errorf("held follower: mute changed %d times, want 0", n)
	}
	// The same silence from a source that is not held still mutes once sourceStartHold is
	// out, and unmutes when sound comes: what the follower did before.
	if n := mutes(false); n != 2 {
		t.Errorf("unheld start: mute changed %d times, want 2", n)
	}
}

// Past tapSilenceHold of digital silence (a leader in standby) the tap stays quiet, and the
// first block with sound goes out again at once.
func TestTapHoldsBackLongSilence(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	rfd := int(r.Fd()) // once: every Fd call puts the file back in blocking mode
	if err := syscall.SetNonblock(rfd, true); err != nil {
		t.Fatal(err)
	}
	tap := &tapWriter{primary: io.Discard, tapFD: int(w.Fd()), on: new(atomic.Bool)}
	tap.on.Store(true)
	block := make([]byte, 3840) // 20 ms
	drain := func() int {
		n, buf := 0, make([]byte, 65536)
		for {
			k, err := syscall.Read(rfd, buf)
			if k <= 0 || err != nil {
				return n
			}
			n += k
		}
	}
	sent := 0
	for range 2 * tapSilenceHold / len(block) {
		tap.Tap(block)
		sent += drain()
	}
	if sent > tapSilenceHold || sent < tapSilenceHold-len(block) {
		t.Fatalf("tap passed %d bytes of silence, want about %d", sent, tapSilenceHold)
	}
	tap.Tap(block)
	if n := drain(); n != 0 {
		t.Fatalf("tap passed %d bytes of silence after the hold", n)
	}
	block[100] = 1
	tap.Tap(block)
	if n := drain(); n != len(block) {
		t.Fatalf("tap passed %d bytes of the first block with sound, want %d", n, len(block))
	}
}

// The amp is powered and warm before the mute opens, and on standby the mute closes
// before the power goes: the order a pop is read from.
func TestTrackOwnershipPowersBeforeUnmuteAndMutesBeforePowerOff(t *testing.T) {
	edges := recordAmpLines(t)
	oldAmp := amplifier
	amplifier = hw.NewAmp("", "", "test") // a model with an amp-power line
	codecGainReady.Store(true)
	t.Cleanup(func() { amplifier = oldAmp; codecGainReady.Store(false) })

	sp := speakerstate.New()
	if err := sp.Select(context.Background(), sourceUPnP, "", "http://192.168.1.10/a.flac", "track", "", ""); err != nil {
		t.Fatal(err)
	}
	var blocks atomic.Uint64
	health := func() audio.Health { // sound flows from the start
		n := blocks.Add(1)
		return audio.Health{SourceNonzeroBlocks: n, SinkNonzeroBlocks: n}
	}
	arb := audio.NewArbiter(audio.NewMixer(io.Discard, false), nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		var shuttingDown, ready atomic.Bool
		var demand atomic.Int32
		trackOwnership(ctx, sp, arb, health, &shuttingDown, &demand, &ready, func() {}, slog.New(slog.DiscardHandler))
	}()
	waitEdges := func(want ...string) {
		t.Helper()
		deadline := time.Now().Add(ampPowerDelay + ampMuteDelay + 3*time.Second)
		for !slices.Equal(edges(), want) {
			if time.Now().After(deadline) {
				t.Fatalf("amp edges %v, want %v", edges(), want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitEdges("amp on", "muted", "unmuted")
	if err := sp.EnterStandby(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitEdges("amp on", "muted", "unmuted", "muted", "amp off")
	cancel()
	<-done
	if got := edges(); len(got) != 5 {
		t.Fatalf("shutdown changed the lines again: %v", got)
	}
}

// Fd() silently turns an os.File pipe into a blocking fd. The source-bus flush
// relies on deadlines, so sizing the pipe must leave runtime polling intact.
func TestPipeSizingPreservesFlushDeadline(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	setPipeSize(r, audio.BusPipeBytes, "test bus", quietLog())
	if err := r.SetReadDeadline(time.Now()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { var b [1]byte; _, err := r.Read(b[:]); done <- err }()
	select {
	case err := <-done:
		if !os.IsTimeout(err) {
			t.Fatalf("reader did not observe deadline: %v", err)
		}
	case <-time.After(time.Second):
		// Supply a byte as well as closing: a read made blocking through Fd cannot
		// always be interrupted by Close on every Go/runtime combination.
		_, _ = w.Write([]byte{0})
		t.Fatal("pipe sizing made the reader uninterruptible")
	}
}

func TestLogHealthWindowThinsCleanPlayback(t *testing.T) {
	for _, c := range []struct {
		name               string
		owner, loggedOwner string
		silence            int64
		underruns          uint64
		since              time.Duration
		want               bool
	}{
		{"clean, logged 10 s ago", "TUNEIN", "TUNEIN", 0, 0, 10 * time.Second, false},
		{"clean, a minute on", "TUNEIN", "TUNEIN", 0, 0, time.Minute, true},
		{"silence", "TUNEIN", "TUNEIN", 3, 0, 10 * time.Second, true},
		{"underrun", "TUNEIN", "TUNEIN", 0, 1, 10 * time.Second, true},
		{"new owner", "AIRPLAY", "TUNEIN", 0, 0, 10 * time.Second, true},
	} {
		if got := logHealthWindow(c.owner, c.loggedOwner, c.silence, c.underruns, c.since); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// recordAmpLines swaps the mute and amp-power lines for a recorder of their edges; like
// the real lines' caches, a repeated value is not an edge.
func recordAmpLines(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var edges []string
	last := map[string]string{}
	add := func(line, v string) {
		mu.Lock()
		defer mu.Unlock()
		if last[line] != v {
			last[line] = v
			edges = append(edges, v)
		}
	}
	old := ampLines
	ampLines.mute = func(m bool, _ *slog.Logger) {
		add("mute", map[bool]string{true: "muted", false: "unmuted"}[m])
	}
	ampLines.power = func(p bool, _ *slog.Logger) bool {
		add("power", map[bool]string{true: "amp on", false: "amp off"}[p])
		return true
	}
	t.Cleanup(func() { ampLines = old })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(edges)
	}
}

// A change of the leader's delay while music plays is a jump in the stream, and a jump
// cut off mid-waveform clicks (the pop at the start of multiroom on the ST20, 2026-11-22).
// The block cut off fades to zero and the first one after the jump rises from zero, both
// when the delay grows and when it shrinks.
func TestDelayWriterFadesEveryJump(t *testing.T) {
	const frames = 8
	block := func(v int16) []byte {
		p := make([]byte, frames*4)
		for i := 0; i < len(p); i += 2 {
			binary.LittleEndian.PutUint16(p[i:], uint16(v))
		}
		return p
	}
	sample := func(p []byte, frame int) int16 { return int16(binary.LittleEndian.Uint16(p[frame*4:])) }
	var dst bytes.Buffer
	var blocks atomic.Int64
	w := delayWriter{w: &dst, blocks: &blocks}
	write := func(v int16) []byte {
		dst.Reset()
		if n, err := w.Write(block(v)); err != nil || n != frames*4 {
			t.Fatalf("Write = %d, %v", n, err)
		}
		return slices.Clone(dst.Bytes())
	}

	if got := write(1000); !bytes.Equal(got, block(1000)) {
		t.Fatalf("no delay: %v", got)
	}
	blocks.Store(2) // multiroom on: the delay grows while playing
	out := write(2000)
	if sample(out, 0) != 2000 || sample(out, frames-1) != 0 {
		t.Fatalf("the block cut off is not faded out: %v", out)
	}
	if got := write(3000); !bytes.Equal(got, make([]byte, frames*4)) {
		t.Fatalf("while the delay fills: %v", got)
	}
	in := write(4000)
	if sample(in, 0) != 0 || sample(in, frames-1) != 2000 {
		t.Fatalf("the first block after the jump does not rise from zero: %v", in)
	}
	if got := write(5000); !bytes.Equal(got, block(3000)) {
		t.Fatalf("delayed by two blocks: %v", got)
	}

	blocks.Store(0) // multiroom off: the delay shrinks, the waiting blocks are skipped
	out = write(6000)
	if sample(out, 0) != 4000 || sample(out, frames-1) != 0 {
		t.Fatalf("the block cut off on shrinking is not faded out: %v", out)
	}
	in = write(7000)
	if sample(in, 0) != 0 || sample(in, frames-1) != 7000 {
		t.Fatalf("the first block after shrinking does not rise from zero: %v", in)
	}
	if got := write(8000); !bytes.Equal(got, block(8000)) {
		t.Fatalf("no delay again: %v", got)
	}
}
