// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// These tests drive the speaker through the same Go methods production uses
// (backend.go, ApplyKey, StatusSnapshot) and assert on the state they leave.

// bg is the context every call here runs under.
func bg() context.Context { return context.Background() }

func now(t *testing.T, sp *Speaker) *speaker.NowPlaying {
	t.Helper()

	np, err := sp.NowPlaying(bg())
	if err != nil {
		t.Fatal(err)
	}

	return np
}

func selectX(t *testing.T, sp *Speaker) {
	t.Helper()

	err := sp.Select(bg(), "TUNEIN", "", "/x", "X", "", "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestNowPlayingStartsInStandby(t *testing.T) {
	t.Parallel()

	if np := now(t, New()); np.Source != "STANDBY" {
		t.Fatalf("source = %q, want STANDBY", np.Source)
	}
}

func TestSelectThenNowPlaying(t *testing.T) {
	t.Parallel()

	spk := New()

	err := spk.Select(bg(), "TUNEIN", "stationurl", "/v1/playback/station/s123", "Jazz FM", "", "")
	if err != nil {
		t.Fatal(err)
	}

	np := now(t, spk)
	if np.Source != "TUNEIN" || np.Location != "/v1/playback/station/s123" || np.Station != "Jazz FM" ||
		np.PlayStatus != "BUFFERING_STATE" {
		t.Fatalf("now playing = %+v", np)
	}
}

func TestKeyTransitions(t *testing.T) {
	t.Parallel()

	spk := New()
	selectX(t, spk)

	for _, test := range []struct{ key, want string }{
		// Radio pauses by stopping its stream, so play tunes in again (buffering).
		{"PAUSE", "PAUSE_STATE"},
		{"PLAY", "BUFFERING_STATE"},
		{"PLAY_PAUSE", "PAUSE_STATE"},     // toggles from playing
		{"PLAY_PAUSE", "BUFFERING_STATE"}, // toggles back
		// STOP halts the stream like PAUSE, so PLAY can start it again.
		{"STOP", "PAUSE_STATE"},
		{"PLAY", "BUFFERING_STATE"},
	} {
		err := spk.Key(bg(), test.key)
		if err != nil {
			t.Fatal(err)
		}

		if got := now(t, spk).PlayStatus; got != test.want {
			t.Fatalf("after %s: %s, want %s", test.key, got, test.want)
		}
	}
}

func TestPowerKeyTogglesStandby(t *testing.T) {
	t.Parallel()

	spk := New()
	selectX(t, spk)
	spk.ApplyKey("POWER")

	if np := now(t, spk); np.Source != "STANDBY" {
		t.Fatalf("POWER did not enter standby: %+v", np)
	}

	spk.ApplyKey("POWER")

	if np := now(t, spk); np.Source != "TUNEIN" {
		t.Fatalf("POWER did not restore the source: %+v", np)
	}
}

func TestPresetStoreRecallRemove(t *testing.T) {
	t.Parallel()

	spk := New()

	err := spk.StorePreset(bg(), 3, "TUNEIN", "stationurl", "/v1/playback/station/s99", "Radio 538", "http://logo")
	if err != nil {
		t.Fatal(err)
	}

	ps, _ := spk.Presets(bg())
	if len(ps) != 1 || ps[0].Slot != 3 || ps[0].Name != "Radio 538" || ps[0].Logo != "http://logo" {
		t.Fatalf("presets = %+v", ps)
	}

	// Pressing the preset key plays the stored station.
	spk.ApplyKey("PRESET_3")

	if np := now(t, spk); np.Station != "Radio 538" || np.PlayStatus != "BUFFERING_STATE" {
		t.Fatalf("after preset press: %+v", np)
	}

	err = spk.RemovePreset(bg(), 3)
	if err != nil {
		t.Fatal(err)
	}

	if ps, _ := spk.Presets(bg()); len(ps) != 0 {
		t.Errorf("preset 3 still present after remove: %+v", ps)
	}
}

// A SPOTIFY preset hands its URI to the Spotify hook and shows the preset at once, so
// the press has feedback before librespot's round trip ends.
func TestSpotifyPresetCallsHook(t *testing.T) {
	t.Parallel()

	spk := New()
	got := make(chan string, 1)

	spk.OnSpotifyPreset(func(uri string) { got <- uri })

	const uri = "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M"

	err := spk.StorePreset(bg(), 2, "SPOTIFY", "uri", uri, "Today's Top Hits", "")
	if err != nil {
		t.Fatal(err)
	}

	spk.ApplyKey("PRESET_2")

	select {
	case u := <-got:
		if u != uri {
			t.Errorf("hook uri = %q, want %q", u, uri)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("spotify hook not called")
	}

	after := now(t, spk)
	if after.Source != "SPOTIFY" || after.Station != "Today's Top Hits" || after.PlayStatus != "BUFFERING_STATE" {
		t.Errorf("after press: %q %q %q, want SPOTIFY / Today's Top Hits / BUFFERING_STATE",
			after.Source, after.Station, after.PlayStatus)
	}

	_ = spk.SetTrackMeta(bg(), "SPOTIFY", "Song", "Artist", "")
	if np := now(t, spk); np.PlayStatus != "PLAY_STATE" {
		t.Errorf("first track left status %q, want PLAY_STATE", np.PlayStatus)
	}
}

// POWER off and on again resumes a Spotify preset instead of waking into TuneIn with
// the playlist's name under the TuneIn logo.
func TestPowerResumesSpotifyPreset(t *testing.T) {
	t.Parallel()

	spk := New()
	got := make(chan string, 2)

	spk.OnSpotifyPreset(func(uri string) { got <- uri })

	const uri = "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M"

	err := spk.StorePreset(bg(), 1, "SPOTIFY", "uri", uri, "Top Hits", "")
	if err != nil {
		t.Fatal(err)
	}

	spk.ApplyKey("PRESET_1")
	<-got
	spk.ApplyKey("POWER")
	spk.ApplyKey("POWER")

	select {
	case u := <-got:
		if u != uri {
			t.Errorf("resumed %q, want %q", u, uri)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("power on did not resume the Spotify preset")
	}

	if np := now(t, spk); np.Source != "SPOTIFY" || np.Station != "Top Hits" {
		t.Errorf("after wake: %q %q, want SPOTIFY / Top Hits", np.Source, np.Station)
	}
}

// Spotify started from the phone has no URI to resume: wake into an empty TuneIn, not
// one that still carries Spotify's name.
func TestPowerAfterSpotifyConnectWakesClean(t *testing.T) {
	t.Parallel()

	spk := New()

	err := spk.Select(bg(), "SPOTIFY", "", "", "Phone", "", "")
	if err != nil {
		t.Fatal(err)
	}

	spk.ApplyKey("POWER")
	spk.ApplyKey("POWER")

	if np := now(t, spk); np.Source != "TUNEIN" || np.Station != "" {
		t.Errorf("after wake: %q %q, want TUNEIN with no name", np.Source, np.Station)
	}
}

func TestPresetSlotOutOfRange(t *testing.T) {
	t.Parallel()

	err := New().StorePreset(bg(), 7, "TUNEIN", "", "/x", "X", "")
	if err == nil {
		t.Error("store slot 7: no error")
	}
}

func TestPressingUnsetPresetIsNoop(t *testing.T) {
	t.Parallel()

	sp := New()
	sp.ApplyKey("PRESET_5") // slot 5 empty

	if np := now(t, sp); np.Source != "STANDBY" {
		t.Errorf("pressing an empty preset changed state: %+v", np)
	}
}

func TestVolumeClamp(t *testing.T) {
	t.Parallel()

	spk := New()
	for _, test := range []struct{ set, want int }{{250, 100}, {-5, 0}} {
		err := spk.SetVolume(bg(), test.set)
		if err != nil {
			t.Fatal(err)
		}

		if got, _ := spk.Volume(bg()); got != test.want {
			t.Errorf("SetVolume(%d) -> %d, want %d", test.set, got, test.want)
		}
	}
}

func TestVolumeSetClearsOldMuteRestoreLevel(t *testing.T) {
	t.Parallel()

	spk := New()
	spk.ApplyKey("MUTE")
	spk.ApplyKey("MUTE") // leave an old restore level behind
	_ = spk.SetVolume(bg(), 47)
	spk.ApplyKey("MUTE")
	spk.ApplyKey("MUTE")

	if got := spk.StatusSnapshot().Volume; got != 47 {
		t.Fatalf("restored volume = %d, want newly set 47", got)
	}
}

// Volume changes reach the UI hook, which the old HTTP path skipped.
func TestSetVolumeFiresPlayChange(t *testing.T) {
	t.Parallel()

	sp := New()
	count := 0

	sp.OnPlayChange(func() { count++ })
	_ = sp.SetVolume(bg(), 55)
	_ = sp.SetVolume(bg(), 55) // unchanged: no notification

	if count != 1 {
		t.Fatalf("play-change fired %d times, want 1", count)
	}
}

func TestVolumeKeys(t *testing.T) {
	t.Parallel()

	spk := New()
	spk.ApplyKey("VOLUME_UP")

	if got, _ := spk.Volume(bg()); got != 31 {
		t.Fatalf("volume up: %d, want 31", got)
	}

	spk.ApplyKey("VOLUME_DOWN")

	if got, _ := spk.Volume(bg()); got != 30 {
		t.Fatalf("volume down: %d, want 30", got)
	}
}

func TestAuxKeySelectsAux(t *testing.T) {
	t.Parallel()

	sp := New()
	sp.ApplyKey("AUX_INPUT")

	if np := now(t, sp); np.Source != "AUX" || np.Station != "AUX" {
		t.Fatalf("now playing = %+v, want AUX / AUX", np)
	}
}

func TestNotificationSetsActiveFlag(t *testing.T) {
	t.Parallel()

	spk := New()

	err := spk.PlayNotification(bg(),
		speaker.Notification{URL: "http://x/ding.mp3", Volume: 0, Artist: "", Album: "", Track: ""})
	if err != nil {
		t.Fatal(err)
	}

	if !spk.StatusSnapshot().Notification {
		t.Fatal("notification flag not active")
	}
}

func TestNotificationCallsAudioHook(t *testing.T) {
	t.Parallel()

	sp := New()
	called := make(chan *Notification, 1)

	sp.OnNotify(func(n *Notification) { called <- n })

	err := sp.PlayNotification(bg(),
		speaker.Notification{URL: "http://x/ding.mp3", Volume: 0, Artist: "", Album: "", Track: ""})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case n := <-called:
		if n.URL != "http://x/ding.mp3" {
			t.Fatalf("notification URL = %q", n.URL)
		}
	default:
		t.Fatal("notification did not call audio hook")
	}
}

func TestNotificationRequiresURL(t *testing.T) {
	t.Parallel()

	err := New().PlayNotification(bg(), speaker.Notification{URL: "", Volume: 0, Artist: "", Album: "", Track: ""})
	if err == nil {
		t.Fatal("empty URL accepted")
	}
}

func TestBassClampToCaps(t *testing.T) {
	t.Parallel()

	sp := New()
	// caps are -9..0; a positive request clamps to 0, below-min to -9.
	for _, tc := range []struct{ set, want int }{{5, 0}, {-20, -9}} {
		_ = sp.SetBass(bg(), tc.set)
		if b, _ := sp.Bass(bg()); b.Actual != tc.want {
			t.Errorf("SetBass(%d) -> %d, want %d", tc.set, b.Actual, tc.want)
		}
	}
}

func TestZoneLifecycle(t *testing.T) {
	t.Parallel()

	spk := New()
	if z, _ := spk.GetZone(bg()); z.Master != "" || len(z.Members) != 0 {
		t.Fatalf("fresh zone not empty: %+v", z)
	}

	master := speaker.Member{DeviceID: "MASTER1", IP: "192.168.1.42"}
	first := speaker.Member{DeviceID: "SLAVE1", IP: "192.168.1.50"}
	second := speaker.Member{DeviceID: "SLAVE2", IP: "192.168.1.51"}

	err := spk.SetZone(bg(), master, []speaker.Member{first})
	if err != nil {
		t.Fatal(err)
	}

	z, _ := spk.GetZone(bg())
	if z.Master != "MASTER1" || len(z.Members) != 2 || z.Members[1] != first {
		t.Fatalf("zone = %+v", z)
	}

	// Add then remove a second slave.
	_ = spk.AddZoneSlave(bg(), master, []speaker.Member{second})
	if z, _ := spk.GetZone(bg()); len(z.Members) != 3 {
		t.Errorf("AddZoneSlave did not add: %+v", z)
	}

	_ = spk.RemoveZoneSlave(bg(), master, []speaker.Member{second})
	if z, _ := spk.GetZone(bg()); len(z.Members) != 2 {
		t.Errorf("RemoveZoneSlave did not remove: %+v", z)
	}
	// Removing the last slave dissolves the zone.
	_ = spk.RemoveZoneSlave(bg(), master, []speaker.Member{first})
	if z, _ := spk.GetZone(bg()); z.Master != "" {
		t.Errorf("zone not dissolved: %+v", z)
	}
}

func TestSetNameShowsInInfo(t *testing.T) {
	t.Parallel()

	sp := New()

	_ = sp.SetName(bg(), "Living Room")
	if info, _ := sp.Info(bg()); info.Name != "Living Room" {
		t.Errorf("name = %q", info.Name)
	}
}

func TestWifiProfileCallback(t *testing.T) {
	t.Setenv("SOUNDTAP_WIFI_MODE", "")

	spk := New()

	var gotSSID, gotPSK string

	spk.OnWifiProfile(func(ssid, _, psk string) error {
		gotSSID, gotPSK = ssid, psk

		return nil
	})

	err := spk.SetWifi(bg(), "Studio 5G", "wpa_or_wpa2", "hunter22")
	if err != nil {
		t.Fatal(err)
	}

	if gotSSID != "Studio 5G" || gotPSK != "hunter22" {
		t.Fatalf("wifi callback = (%q, %q)", gotSSID, gotPSK)
	}
}

// Stock transitions Standby to On on POWER even when no source has played yet.
// With no TuneIn location, wake idle rather than pretending to buffer a stream.
func TestPowerKeyWakesIdleWithNoStation(t *testing.T) {
	t.Parallel()

	sp := New()
	sp.ApplyKey("POWER")

	playing := now(t, sp)
	if playing.Source != "TUNEIN" {
		t.Fatalf("POWER did not leave standby: %+v", playing)
	}

	if playing.PlayStatus == "BUFFERING_STATE" {
		t.Fatalf("nothing to play, yet buffering: %+v", playing)
	}
}

// A preset is the whole gesture: press it while the speaker is off and it plays.
func TestPresetFromStandbyStartsPlaying(t *testing.T) {
	t.Parallel()

	spk := New()
	_ = spk.StorePreset(bg(), 3, "TUNEIN", "stationurl", "/v1/playback/station/s99", "Drie", "")
	selectX(t, spk)
	spk.ApplyKey("POWER") // off
	spk.ApplyKey("PRESET_3")

	np := now(t, spk)
	if np.Source == "STANDBY" || np.StationID != "s99" {
		t.Fatalf("preset press from standby: %+v", np)
	}
}

// Pressing a preset key on an empty slot must set EmptyPreset in the snapshot so
// the LED loop can give amber-blink feedback, exactly as stock does.
func TestEmptyPresetSetsSnapshotFlag(t *testing.T) {
	t.Parallel()

	spk := New()
	// Slot 5 is empty by default.
	spk.ApplyKey("PRESET_5")

	if snap := spk.StatusSnapshot(); !snap.EmptyPreset || snap.EmptySlot != 5 {
		t.Fatalf("pressing empty preset 5: EmptyPreset %v, slot %d in the snapshot", snap.EmptyPreset, snap.EmptySlot)
	}
	// A slot that IS filled must not set the flag.
	_ = spk.StorePreset(bg(), 2, "TUNEIN", "stationurl", "/v1/playback/station/s1", "R1", "")
	spk.ApplyKey("PRESET_2")

	if spk.StatusSnapshot().EmptyPreset {
		t.Fatal("pressing filled preset 2: EmptyPreset should not be set")
	}
}

// Spotify started from the web UI is named on the speaker and can be stored by holding
// a preset key; Spotify started from a phone has no URI and is not storable.
func TestHoldStoresSpotifyFromWebUI(t *testing.T) {
	t.Parallel()

	spk := New()
	ctx := context.Background()

	const uri = "spotify:album:4aawyAB9vmqN3uQ7FjRGTy"
	spk.RememberSpotify(uri, "Album", "art.jpg")

	_ = spk.SetSource(ctx, "SPOTIFY")

	stored, err := spk.StoreCurrentAsPreset("PRESET_4")
	if !stored || err != nil {
		t.Fatalf("store = %v, %v; want stored", stored, err)
	}

	presets, _ := spk.Presets(ctx)

	var got *speaker.Preset

	for i := range presets {
		if presets[i].Slot == 4 {
			got = &presets[i]
		}
	}

	if got == nil || got.Source != "SPOTIFY" || got.Location != uri || got.Name != "Album" {
		t.Fatalf("preset 4 = %+v, want SPOTIFY %s Album", got, uri)
	}

	_ = spk.SetSource(ctx, "AIRPLAY")

	_ = spk.SetSource(ctx, "SPOTIFY") // from a phone: nothing remembered
	if stored, _ := spk.StoreCurrentAsPreset("PRESET_5"); stored {
		t.Fatal("stored Spotify Connect playback that has no URI")
	}
}

// A volume change whose hook runs late must not overwrite a newer change already
// delivered.
func TestVolumeHookDropsStaleLevel(t *testing.T) {
	t.Parallel()

	spk := New()

	var applied []int

	spk.OnVolume(func(level int) { applied = append(applied, level) })
	spk.mu.Lock()
	before := spk.volume
	spk.volume = 40
	stale := spk.volumeHookLocked(before)
	spk.mu.Unlock()

	err := spk.SetVolume(context.Background(), 60)
	if err != nil {
		t.Fatal(err)
	}

	stale()

	if len(applied) != 1 || applied[0] != 60 {
		t.Fatalf("hook levels = %v, want [60]", applied)
	}
}

func TestFollowSpotifyStoresWhatThePhonePicked(t *testing.T) {
	t.Parallel()

	spk := New()

	ctx := context.Background()

	err := spk.SetSource(ctx, "SPOTIFY")
	if err != nil {
		t.Fatal(err)
	}

	spk.RememberSpotify("spotify:playlist:aaa", "Mine", "")

	err = spk.SetTrackMeta(ctx, "SPOTIFY", "Song", "Artist", "")
	if err != nil {
		t.Fatal(err)
	}

	spk.FollowSpotify("spotify:album:bbb")

	ok, err := spk.StoreCurrentAsPreset("PRESET_1")
	if err != nil || !ok {
		t.Fatalf("store: %v %v", ok, err)
	}

	presets, err := spk.Presets(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if presets[0].Location != "spotify:album:bbb" || presets[0].Name != "Song" {
		t.Fatalf("stored %+v", presets[0])
	}
}

func TestFollowSpotifyKeepsTheNameOfWhatWasStartedHere(t *testing.T) {
	t.Parallel()

	spk := New()

	ctx := context.Background()

	err := spk.SetSource(ctx, "SPOTIFY")
	if err != nil {
		t.Fatal(err)
	}

	spk.RememberSpotify("spotify:playlist:aaa", "Mine", "")
	spk.FollowSpotify("spotify:playlist:aaa")

	if np, _ := spk.NowPlaying(ctx); np.Station != "Mine" {
		t.Fatalf("name %q", np.Station)
	}
}

// With no station to replay, POWER wakes into the lowest preset; with no preset
// either it wakes idle, as TestPowerKeyWakesIdleWithNoStation holds.
func TestPowerKeyWakesIntoFirstPreset(t *testing.T) {
	t.Parallel()

	sp := New()
	_ = sp.StorePreset(bg(), 2, "TUNEIN", "stationurl", "/v1/playback/station/s99", "Twee", "")
	_ = sp.StorePreset(bg(), 5, "TUNEIN", "stationurl", "/v1/playback/station/s55", "Vijf", "")
	sp.ApplyKey("POWER")

	np := now(t, sp)
	if np.Source != "TUNEIN" || np.StationID != "s99" || np.PlayStatus != "BUFFERING_STATE" {
		t.Fatalf("woke into %+v, want preset 2 buffering", np)
	}
}

// While following, a key the leader takes does not act here; one it refuses does.
func TestFollowerKeysGoToTheLeader(t *testing.T) {
	t.Parallel()

	spk := New()

	err := spk.SetSource(bg(), "MULTIROOM")
	if err != nil {
		t.Fatal(err)
	}

	var sent []string

	take := true

	spk.OnForwardKey(func(key string) bool {
		sent = append(sent, key)

		return take
	})
	spk.ApplyKey("PLAY_PAUSE")

	if st := spk.StatusSnapshot(); st.PlayStatus == "PAUSE_STATE" || len(sent) != 1 {
		t.Fatalf("forwarded key also acted locally: %+v %v", st, sent)
	}

	take = false

	spk.ApplyKey("PLAY_PAUSE")

	if st := spk.StatusSnapshot(); st.PlayStatus != "PAUSE_STATE" {
		t.Fatalf("refused key did not fall back: %+v", st)
	}
}
