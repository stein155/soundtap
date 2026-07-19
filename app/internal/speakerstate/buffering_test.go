// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"testing"
)

func status(sp *Speaker) string {
	np, err := sp.NowPlaying(context.Background())
	if err != nil {
		panic(err)
	}
	return np.PlayStatus
}

// Selecting a station must show as buffering straight away — resolving the stream and
// connecting takes seconds, and reporting PLAY_STATE immediately is what left the UI
// with no feedback at all. Push sources are already producing audio when they claim
// the amp, so they stay PLAY_STATE.
func TestSelectStartsBuffering(t *testing.T) {
	ctx := context.Background()
	sp := New()
	if err := sp.Select(ctx, "TUNEIN", "", "/station/s47309", "NPO Radio 2", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := status(sp); got != "BUFFERING_STATE" {
		t.Fatalf("after selecting a station playStatus = %q, want BUFFERING_STATE", got)
	}

	sp = New()
	if err := sp.SetSource(ctx, "AIRPLAY"); err != nil {
		t.Fatal(err)
	}
	if got := status(sp); got != "PLAY_STATE" {
		t.Fatalf("AIRPLAY playStatus = %q, want PLAY_STATE", got)
	}
}

// The player promotes buffering to playing only once audio really flows, and only for
// the attempt it started.
func TestConfirmPlayingHonoursGeneration(t *testing.T) {
	ctx := context.Background()
	sp := New()
	if err := sp.Select(ctx, "TUNEIN", "", "/station/s1", "One", "", ""); err != nil {
		t.Fatal(err)
	}
	gen := sp.BeginBuffering("TUNEIN")
	if gen == 0 {
		t.Fatal("BeginBuffering returned 0 for the owning source")
	}

	sp.ConfirmPlaying(gen)
	if got := status(sp); got != "PLAY_STATE" {
		t.Fatalf("playStatus = %q, want PLAY_STATE", got)
	}

	// The user switches station; the first stream finally connects and reports in.
	// That must not resurrect the station they left.
	if err := sp.Select(ctx, "TUNEIN", "", "/station/s2", "Two", "", ""); err != nil {
		t.Fatal(err)
	}
	sp.ConfirmPlaying(gen)
	if got := status(sp); got != "BUFFERING_STATE" {
		t.Fatalf("stale confirmation took effect: playStatus = %q", got)
	}
	np, _ := sp.NowPlaying(ctx)
	if np.Station != "Two" {
		t.Fatalf("station = %q, want Two", np.Station)
	}
}

func TestReselectSamePresetStartsNewPlayback(t *testing.T) {
	ctx := context.Background()
	sp := New()
	if err := sp.StorePreset(ctx, 1, "TUNEIN", "stationurl", "/station/s1", "One", ""); err != nil {
		t.Fatal(err)
	}
	sp.ApplyKey("PRESET_1")
	_, _, _, _, first := sp.PlaybackSelection()
	old := sp.BeginBuffering("TUNEIN")
	sp.ConfirmPlaying(old)
	sp.ApplyKey("PRESET_1")
	src, loc, _, _, second := sp.PlaybackSelection()
	if src != "TUNEIN" || loc != "/station/s1" || second != first+1 || status(sp) != "BUFFERING_STATE" {
		t.Fatalf("same preset: source=%q location=%q selection=%d->%d status=%q", src, loc, first, second, status(sp))
	}
	sp.ConfirmPlaying(old)
	if status(sp) != "BUFFERING_STATE" {
		t.Fatal("old decoder confirmed reselected preset")
	}
	sp.ConfirmPlaying(sp.BeginBuffering("TUNEIN"))
	if status(sp) != "PLAY_STATE" {
		t.Fatal("new decoder did not confirm reselected preset")
	}
}

// A stream that never produces audio must land somewhere the UI reads as "off",
// never spin forever.
func TestNowPlayingTellsAFailureFromAStop(t *testing.T) {
	sp := New()
	sp.applySource("TUNEIN", "http://h/a", "A", "")
	gen := sp.BeginBuffering("TUNEIN")
	sp.FailPlayback(gen)
	np, err := sp.NowPlaying(context.Background())
	if err != nil || !np.Failed {
		t.Fatalf("a stream that never played reads as failed, got %+v, %v", np, err)
	}
	sp.applySource("TUNEIN", "http://h/b", "B", "")
	sp.BeginBuffering("TUNEIN")
	sp.ApplyKey("STOP")
	if np, _ = sp.NowPlaying(context.Background()); np.Failed {
		t.Fatalf("a stop by hand on the next attempt is not a failure: %+v", np)
	}
}

func TestFailPlaybackStopsTheSpinner(t *testing.T) {
	ctx := context.Background()
	sp := New()
	if err := sp.Select(ctx, "TUNEIN", "", "/station/s1", "One", "", ""); err != nil {
		t.Fatal(err)
	}
	gen := sp.BeginBuffering("TUNEIN")
	sp.FailPlayback(gen)
	if got := status(sp); got != "STOP_STATE" {
		t.Fatalf("playStatus = %q, want STOP_STATE", got)
	}
	// And a failure cannot be undone by the same generation reporting success later.
	sp.ConfirmPlaying(gen)
	if got := status(sp); got != "STOP_STATE" {
		t.Fatalf("confirmation after failure = %q, want STOP_STATE", got)
	}
}

// A decoder that lost the amp must not be able to speak for the current source.
func TestBeginBufferingRejectsNonOwner(t *testing.T) {
	ctx := context.Background()
	sp := New()
	if err := sp.SetSource(ctx, "AIRPLAY"); err != nil {
		t.Fatal(err)
	}
	if gen := sp.BeginBuffering("TUNEIN"); gen != 0 {
		t.Fatalf("BeginBuffering for a non-owner returned %d, want 0", gen)
	}
	if got := status(sp); got != "PLAY_STATE" {
		t.Fatalf("non-owner changed playStatus to %q", got)
	}
}

// Standby is not playing. Leaving playStatus at PLAY_STATE made the MQTT
// bridge publish "playing" for a speaker that is off.
func TestStandbyReportsStopped(t *testing.T) {
	ctx := context.Background()
	sp := New()
	if err := sp.SetSource(ctx, "AIRPLAY"); err != nil {
		t.Fatal(err)
	}
	if err := sp.EnterStandby(ctx); err != nil {
		t.Fatal(err)
	}
	if got := status(sp); got != "STOP_STATE" {
		t.Fatalf("standby playStatus = %q, want STOP_STATE", got)
	}
}

// The play-change hook is how the SSE hub learns about a transition that happens
// outside an HTTP request.
func TestOnPlayChangeFires(t *testing.T) {
	ctx := context.Background()
	sp := New()
	fired := 0
	sp.OnPlayChange(func() { fired++ })
	if err := sp.Select(ctx, "TUNEIN", "", "/station/s1", "One", "", ""); err != nil {
		t.Fatal(err)
	}
	sp.ConfirmPlaying(sp.BeginBuffering("TUNEIN"))
	if fired != 1 {
		t.Fatalf("play-change hook fired %d times, want 1", fired)
	}
}

// Selecting Bluetooth from the front panel must not claim to be playing: no
// phone may be connected yet, and the front-panel lamp distinguishes the two.
func TestSourceToggleToBluetoothWaitsForADevice(t *testing.T) {
	s := New()
	s.ApplyKey("SOURCE_TOGGLE") // -> AUX
	if got := s.StatusSnapshot().Source; got != "AUX" {
		t.Fatalf("first toggle = %q, want AUX", got)
	}
	s.ApplyKey("SOURCE_TOGGLE") // -> BLUETOOTH
	st := s.StatusSnapshot()
	if st.Source != "BLUETOOTH" {
		t.Fatalf("second toggle = %q, want BLUETOOTH", st.Source)
	}
	if st.PlayStatus == "PLAY_STATE" {
		t.Fatal("selecting Bluetooth reported PLAY_STATE with nothing connected")
	}
}

// POWER two seconds after a preset left the player's confirmation running, and 25 s
// later it logged "stream produced no audio" for a stream standby had already stopped.
// The player asks StillBuffering first; after standby the answer is no.
func TestStandbyEndsBuffering(t *testing.T) {
	ctx := context.Background()
	sp := New()
	if err := sp.Select(ctx, "TUNEIN", "", "/station/s1", "One", "", ""); err != nil {
		t.Fatal(err)
	}
	gen := sp.BeginBuffering("TUNEIN")
	if !sp.StillBuffering(gen) {
		t.Fatal("a fresh attempt does not report buffering")
	}
	sp.ApplyKey("POWER")
	if sp.StillBuffering(gen) {
		t.Fatal("standby did not end the buffering attempt")
	}
}

// Leaving a multiroom group selects STANDBY through SetSource. That must be a real
// standby: stopped, and POWER wakes the speaker again.
func TestSetSourceStandbyIsStandby(t *testing.T) {
	ctx := context.Background()
	sp := New()
	if err := sp.SetSource(ctx, "MULTIROOM"); err != nil {
		t.Fatal(err)
	}
	if err := sp.SetSource(ctx, "STANDBY"); err != nil {
		t.Fatal(err)
	}
	np, _ := sp.NowPlaying(ctx)
	if np.Source != "STANDBY" || np.PlayStatus != "STOP_STATE" {
		t.Fatalf("after leaving: %q/%q, want STANDBY/STOP_STATE", np.Source, np.PlayStatus)
	}
	_ = sp.Key(ctx, "POWER")
	if np, _ = sp.NowPlaying(ctx); np.Source == "STANDBY" {
		t.Fatal("POWER did not wake the speaker after it left a group")
	}
}

// A UPnP track that played to its end stops, even though it was confirmed playing, and
// an end reported for an attempt the user already replaced changes nothing.
func TestEndPlaybackStopsAConfirmedTrack(t *testing.T) {
	ctx := context.Background()
	sp := New()
	if err := sp.Select(ctx, "UPNP", "", "http://192.168.1.9/a.mp3", "a", "", ""); err != nil {
		t.Fatal(err)
	}
	gen := sp.BeginBuffering("UPNP")
	sp.ConfirmPlaying(gen)
	sp.EndPlayback(gen)
	if got := status(sp); got != "STOP_STATE" {
		t.Fatalf("after the end = %q, want STOP_STATE", got)
	}
	if err := sp.Select(ctx, "UPNP", "", "http://192.168.1.9/b.mp3", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	next := sp.BeginBuffering("UPNP")
	sp.ConfirmPlaying(next)
	sp.EndPlayback(gen)
	if got := status(sp); got != "PLAY_STATE" {
		t.Fatalf("a stale end = %q, want PLAY_STATE", got)
	}
}
