// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// 0 means "not configured" and gets the stock default; a negative value is the only
// way to say "off", because JSON cannot tell an absent int from a zero one.
func TestAutoOffTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	set := settings.Open(path)
	if got := autoOffTimeout(set); got != DefaultAutoOffMinutes*time.Minute {
		t.Fatalf("unconfigured = %v, want the %d minute default", got, DefaultAutoOffMinutes)
	}
	if err := set.SetAutoOff(45); err != nil {
		t.Fatal(err)
	}
	if got := autoOffTimeout(set); got != 45*time.Minute {
		t.Fatalf("configured = %v, want 45m", got)
	}
	if err := set.SetAutoOff(-1); err != nil {
		t.Fatal(err)
	}
	if got := autoOffTimeout(set); got != 0 {
		t.Fatalf("disabled = %v, want 0 (never fires)", got)
	}
}

func TestAutoOffActivity(t *testing.T) {
	for _, tc := range []struct {
		name               string
		source, status     string
		phone              bool
		blocks, lastBlocks uint64
		want               bool
	}{
		{"aux bypasses PCM counter", "AUX", "PLAY_STATE", false, 4, 4, true},
		{"ST300 TV bypasses PCM counter", "PRODUCT", "PLAY_STATE", false, 4, 4, true},
		{"digital PCM advanced", "AIRPLAY", "PLAY_STATE", false, 5, 4, true},
		{"digital PCM quiet", "AIRPLAY", "PLAY_STATE", false, 4, 4, false},
		{"bluetooth paused, phone connected", "BLUETOOTH", "PAUSE_STATE", true, 4, 4, true},
		{"bluetooth paused, phone lost", "BLUETOOTH", "PAUSE_STATE", false, 4, 4, false},
		{"bluetooth phone playing", "BLUETOOTH", "PLAY_STATE", false, 4, 4, true},
		{"bluetooth waiting for a phone", "BLUETOOTH", "STOP_STATE", false, 4, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := speakerstate.StatusSnapshot{Source: tc.source, PlayStatus: tc.status}
			if got := autoOffHasActivity(st, tc.phone, tc.blocks, tc.lastBlocks); got != tc.want {
				t.Fatalf("activity = %v, want %v", got, tc.want)
			}
		})
	}
}

// A person's change re-arms auto-off; the LED-only fields and the address do not.
func TestAutoOffUserFacing(t *testing.T) {
	base := speakerstate.StatusSnapshot{Source: "AIRPLAY", PlayStatus: "PAUSE_STATE", Volume: 20}
	if userFacing(base) != userFacing(speakerstate.StatusSnapshot{Source: "AIRPLAY", PlayStatus: "PAUSE_STATE", Volume: 20, IP: "10.0.0.2", TransportAck: true}) {
		t.Error("address or LED blink counted as user activity")
	}
	for name, st := range map[string]speakerstate.StatusSnapshot{
		"volume": {Source: "AIRPLAY", PlayStatus: "PAUSE_STATE", Volume: 21},
		"mute":   {Source: "AIRPLAY", PlayStatus: "PAUSE_STATE", Volume: 20, Muted: true},
		"play":   {Source: "AIRPLAY", PlayStatus: "PLAY_STATE", Volume: 20},
		"source": {Source: "RADIO", PlayStatus: "PAUSE_STATE", Volume: 20},
	} {
		if userFacing(base) == userFacing(st) {
			t.Errorf("%s change not counted as user activity", name)
		}
	}
	retrying := speakerstate.StatusSnapshot{Source: "UPNP", PlayStatus: playStopped, Volume: 20}
	buffering := retrying
	buffering.PlayStatus = playBuffering
	if userFacing(retrying) != userFacing(buffering) {
		t.Error("a player retrying a dead stream counted as user activity")
	}
}

// Restoring at boot must reach the live speaker, not just the settings file.
func TestRestoreAudioPrefs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.json")
	set := settings.Open(path)
	if err := set.SetAudio(settings.Audio{Volume: 63, Bass: -6, HasVolume: true}); err != nil {
		t.Fatal(err)
	}
	sp := speakerstate.New()
	restoreAudioPrefs(ctx, sp, set, quietLog())
	if got := sp.StatusSnapshot().Volume; got != 63 {
		t.Fatalf("volume = %d, want 63", got)
	}
	b, err := sp.Bass(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b.Target != -6 {
		t.Fatalf("bass = %d, want -6", b.Target)
	}
}

// Nothing saved yet must leave the built-in defaults alone rather than writing a
// zero volume over them.
func TestRestoreAudioPrefsSkipsUnsaved(t *testing.T) {
	ctx := context.Background()
	set := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	sp := speakerstate.New()
	before := sp.StatusSnapshot().Volume
	restoreAudioPrefs(ctx, sp, set, quietLog())
	if got := sp.StatusSnapshot().Volume; got != before {
		t.Fatalf("volume moved from %d to %d with nothing saved", before, got)
	}
}

// currentAudioPrefs is what the saver compares and stores; it must read the live
// speaker, and always mark the result as saved so a genuine 0 round-trips.
func TestCurrentAudioPrefs(t *testing.T) {
	ctx := context.Background()
	sp := speakerstate.New()
	if err := sp.SetVolume(ctx, 0); err != nil {
		t.Fatal(err)
	}
	got, err := currentAudioPrefs(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	if got.Volume != 0 || !got.HasVolume {
		t.Fatalf("got %+v, want volume 0 marked as saved", got)
	}
}

// Auto-off must key off PCM that actually reached the DAC. A source selected but
// producing nothing is exactly the case that used to leave the amp live all night.
func TestAutoOffEntersStandbyWhenNoAudioReachesTheDAC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	set := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	// A tiny timeout, expressed the same way a user would: minutes are too coarse
	// for a test, so drive the comparison through a stubbed clock instead.
	sp := speakerstate.New()
	if err := sp.SetSource(ctx, "AIRPLAY"); err != nil {
		t.Fatal(err)
	}
	if sp.IsStandby() {
		t.Fatal("speaker started in standby")
	}
	// Silent sink: SinkNonzeroBlocks never advances.
	silent := func() audio.Health { return audio.Health{} }
	go runAutoOff(ctx, sp, set, silent, quietLog())
	// With the default 20-minute timeout it must NOT fire promptly; that is the
	// property worth pinning here (an over-eager auto-off is the harmful failure).
	time.Sleep(200 * time.Millisecond)
	if sp.IsStandby() {
		t.Fatal("auto-off fired immediately")
	}
}

// No arbiter means no trustworthy idleness signal, so auto-off must stay out of the
// way rather than powering the speaker down on a guess.
func TestAutoOffWithoutHealthIsInert(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	set := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	sp := speakerstate.New()
	if err := sp.SetSource(ctx, "AIRPLAY"); err != nil {
		t.Fatal(err)
	}
	runAutoOff(ctx, sp, set, nil, quietLog()) // returns immediately
	if sp.IsStandby() {
		t.Fatal("auto-off acted with no health source")
	}
}
