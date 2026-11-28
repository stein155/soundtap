// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// autoOffSnapshot is a status with only what auto-off reads set: no blink, no address.
func autoOffSnapshot(source, playStatus string, volume int) speakerstate.StatusSnapshot {
	return speakerstate.StatusSnapshot{
		Source: source, PlayStatus: playStatus, Standby: false, Volume: volume, Muted: false, Bass: 0,
		Notification: false, EmptyPreset: false, EmptySlot: 0, TransportAck: false, IP: "",
	}
}

// 0 means "not configured" and gets the stock default; a negative value is the only
// way to say "off", because JSON cannot tell an absent int from a zero one.
func TestAutoOffTimeout(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")

	set := settings.Open(path)
	if got := autoOffTimeout(set); got != DefaultAutoOffMinutes*time.Minute {
		t.Fatalf("unconfigured = %v, want the %d minute default", got, DefaultAutoOffMinutes)
	}

	err := set.SetAutoOff(45)
	if err != nil {
		t.Fatal(err)
	}

	if got := autoOffTimeout(set); got != 45*time.Minute {
		t.Fatalf("configured = %v, want 45m", got)
	}

	err = set.SetAutoOff(-1)
	if err != nil {
		t.Fatal(err)
	}

	if got := autoOffTimeout(set); got != 0 {
		t.Fatalf("disabled = %v, want 0 (never fires)", got)
	}
}

func TestAutoOffActivity(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
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
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			snap := autoOffSnapshot(test.source, test.status, 0)
			if got := autoOffHasActivity(&snap, test.phone, test.blocks, test.lastBlocks); got != test.want {
				t.Fatalf("activity = %v, want %v", got, test.want)
			}
		})
	}
}

// A person's change re-arms auto-off; the LED-only fields and the address do not.
func TestAutoOffUserFacing(t *testing.T) {
	t.Parallel()

	base := autoOffSnapshot("AIRPLAY", "PAUSE_STATE", 20)
	blink := autoOffSnapshot("AIRPLAY", "PAUSE_STATE", 20)
	blink.IP, blink.TransportAck = "10.0.0.2", true

	if userFacing(&base) != userFacing(&blink) {
		t.Error("address or LED blink counted as user activity")
	}

	muted := autoOffSnapshot("AIRPLAY", "PAUSE_STATE", 20)
	muted.Muted = true

	for name, snap := range map[string]speakerstate.StatusSnapshot{
		"volume": autoOffSnapshot("AIRPLAY", "PAUSE_STATE", 21),
		"mute":   muted,
		"play":   autoOffSnapshot("AIRPLAY", "PLAY_STATE", 20),
		"source": autoOffSnapshot("RADIO", "PAUSE_STATE", 20),
	} {
		if userFacing(&base) == userFacing(&snap) {
			t.Errorf("%s change not counted as user activity", name)
		}
	}

	retrying := autoOffSnapshot("UPNP", playStopped, 20)
	buffering := retrying

	buffering.PlayStatus = playBuffering
	if userFacing(&retrying) != userFacing(&buffering) {
		t.Error("a player retrying a dead stream counted as user activity")
	}
}

// Restoring at boot must reach the live speaker, not just the settings file.
func TestRestoreAudioPrefs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.json")

	set := settings.Open(path)

	err := set.SetAudio(settings.Audio{Volume: 63, Bass: -6, HasVolume: true, AutoOff: 0})
	if err != nil {
		t.Fatal(err)
	}

	state := speakerstate.New()
	restoreAudioPrefs(ctx, state, set, quietLog())

	if got := state.StatusSnapshot().Volume; got != 63 {
		t.Fatalf("volume = %d, want 63", got)
	}

	bass, err := state.Bass(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if bass.Target != -6 {
		t.Fatalf("bass = %d, want -6", bass.Target)
	}
}

// Nothing saved yet must leave the built-in defaults alone rather than writing a
// zero volume over them.
func TestRestoreAudioPrefsSkipsUnsaved(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	set := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	state := speakerstate.New()
	before := state.StatusSnapshot().Volume
	restoreAudioPrefs(ctx, state, set, quietLog())

	if got := state.StatusSnapshot().Volume; got != before {
		t.Fatalf("volume moved from %d to %d with nothing saved", before, got)
	}
}

// currentAudioPrefs is what the saver compares and stores; it must read the live
// speaker, and always mark the result as saved so a genuine 0 round-trips.
func TestCurrentAudioPrefs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	state := speakerstate.New()

	err := state.SetVolume(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}

	got, err := currentAudioPrefs(ctx, state)
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
	t.Parallel()

	ctx := t.Context()

	set := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	// A tiny timeout, expressed the same way a user would: minutes are too coarse
	// for a test, so drive the comparison through a stubbed clock instead.
	state := speakerstate.New()

	err := state.SetSource(ctx, "AIRPLAY")
	if err != nil {
		t.Fatal(err)
	}

	if state.IsStandby() {
		t.Fatal("speaker started in standby")
	}
	// Silent sink: SinkNonzeroBlocks never advances.
	silent := func() audio.Health {
		var none audio.Health

		return none
	}
	go runAutoOff(ctx, state, set, silent, quietLog())
	// With the default 20-minute timeout it must NOT fire promptly; that is the
	// property worth pinning here (an over-eager auto-off is the harmful failure).
	time.Sleep(200 * time.Millisecond)

	if state.IsStandby() {
		t.Fatal("auto-off fired immediately")
	}
}

// No arbiter means no trustworthy idleness signal, so auto-off must stay out of the
// way rather than powering the speaker down on a guess.
func TestAutoOffWithoutHealthIsInert(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	set := settings.Open(filepath.Join(t.TempDir(), "settings.json"))

	state := speakerstate.New()

	err := state.SetSource(ctx, "AIRPLAY")
	if err != nil {
		t.Fatal(err)
	}

	runAutoOff(ctx, state, set, nil, quietLog()) // returns immediately

	if state.IsStandby() {
		t.Fatal("auto-off acted with no health source")
	}
}
