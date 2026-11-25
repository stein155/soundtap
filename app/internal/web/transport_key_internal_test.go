// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/settings"
)

// A follower's transport key reaches the leader through POST /api/key; anything
// else is refused there, and with no leader ForwardKey fails so the key acts locally.
func TestFollowerForwardsTransportToLeader(t *testing.T) {
	t.Parallel()

	leader, lsp, _ := radioServer(t)

	ctx := context.Background()

	err := lsp.Select(ctx, "TUNEIN", "stationurl", "/station/s1", "Een", "", "")
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(leader.postKey))
	t.Cleanup(srv.Close)
	follower, _, _ := radioServer(t)

	follower.homeDir = t.TempDir()

	err = follower.ForwardKey(ctx, "PLAY_PAUSE")
	if err == nil {
		t.Fatal("forwarded with no leader")
	}

	u, _ := url.Parse(srv.URL)

	err = follower.setFollow(u.Host, "leader")
	if err != nil {
		t.Fatal(err)
	}

	err = follower.ForwardKey(ctx, "PLAY_PAUSE")
	if err != nil {
		t.Fatal(err)
	}

	if st := lsp.StatusSnapshot(); st.PlayStatus != "PAUSE_STATE" {
		t.Fatalf("leader is %s, want PAUSE_STATE", st.PlayStatus)
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(`{"key":"POWER"}`))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || lsp.StatusSnapshot().Standby {
		t.Fatalf("POWER through /api/key: %s", resp.Status)
	}
}

// Stock's zone slave hands only the transport to its master; volume, mute, power,
// presets and the source keys act on the speaker that was pressed, leader or follower.
func TestVolumeKeysStayOnThePressedSpeaker(t *testing.T) {
	t.Parallel()

	srv, _, _ := radioServer(t)
	for _, k := range []string{"PLAY", "PAUSE", "PLAY_PAUSE", "STOP", "NEXT_TRACK", "PREV_TRACK"} {
		if !srv.ForwardsKey(k) {
			t.Errorf("%s not forwarded to the leader", k)
		}
	}

	for _, k := range []string{"VOLUME_UP", "VOLUME_DOWN", "MUTE", "POWER", "PRESET_1", "AUX_INPUT", "BLUETOOTH"} {
		if srv.ForwardsKey(k) {
			t.Errorf("%s forwarded to the leader, want local", k)
		}
	}
}

// Half of a stereo pair also hands power and presets to the leader, and the leader
// takes them; volume and mute stay local.
func TestPairForwardsPowerAndPresets(t *testing.T) {
	t.Parallel()

	srv, _, _ := radioServer(t)

	err := srv.settings.SetChannel("left")
	if err != nil {
		t.Fatal(err)
	}

	for _, k := range []string{"POWER", "PRESET_1", "PRESET_6", "PLAY_PAUSE"} {
		if !srv.ForwardsKey(k) {
			t.Errorf("%s not forwarded in a pair", k)
		}
	}

	for _, k := range []string{"VOLUME_UP", "MUTE", "PRESET_7"} {
		if srv.ForwardsKey(k) {
			t.Errorf("%s forwarded in a pair, want local", k)
		}
	}
}

// A pair stays together when its leader goes to standby and moves both halves with
// the group slider, whatever the owner's group preferences say.
func TestPairKeepsGroupAndSyncsVolume(t *testing.T) {
	t.Parallel()

	fixture := groupServer(t, settings.Multiroom{DissolveGroups: true, SyncVolume: false})
	srv, spk, followerA := fixture.srv, fixture.spk, fixture.followerA

	err := srv.settings.SetChannel("left")
	if err != nil {
		t.Fatal(err)
	}

	playStation(t, spk)
	radioDo(t, srv.Handler(), http.MethodPost, "/api/volume", `{"volume":40,"group":true}`)
	radioDo(t, srv.Handler(), http.MethodPost, "/api/stop", "")
	srv.group.WaitPeers()

	if v, posts, following := followerA.snapshot(); following != "leader" || v != 60 {
		t.Fatalf("follower: volume %d, following %q, posts %v", v, following, posts)
	}
}
