// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"testing"
	"time"
)

func TestMuteKeepsTheLevelAndToggles(t *testing.T) {
	s := New()
	s.ApplyKey("VOLUME_UP") // 30 -> 31, so the restored level is not the default
	s.ApplyKey("MUTE")
	st := s.StatusSnapshot()
	if st.Volume != 31 || !st.Muted || st.Output() != 0 {
		t.Fatalf("muted: %+v, want level 31 muted, output 0", st)
	}
	s.ApplyKey("MUTE")
	if st := s.StatusSnapshot(); st.Volume != 31 || st.Muted || st.Output() != 31 {
		t.Fatalf("unmuted: %+v, want level 31 heard", st)
	}
}

// Either volume button on a muted speaker only unmutes, at the level it had.
func TestVolumeKeysUnmuteWithoutStepping(t *testing.T) {
	for _, key := range []string{"VOLUME_UP", "VOLUME_DOWN"} {
		s := New()
		if err := s.SetVolume(context.Background(), 40); err != nil {
			t.Fatal(err)
		}
		s.ApplyKey("MUTE")
		s.ApplyKey(key)
		if st := s.StatusSnapshot(); st.Muted || st.Volume != 40 {
			t.Fatalf("%s while muted: %+v, want unmuted at 40", key, st)
		}
		s.ApplyKey(key) // the next press steps
		want := 41
		if key == "VOLUME_DOWN" {
			want = 39
		}
		if got := s.StatusSnapshot().Volume; got != want {
			t.Fatalf("%s: volume %d, want %d", key, got, want)
		}
	}
}

// Setting a level, from the API or a phone, is asking to hear it.
func TestSetVolumeUnmutes(t *testing.T) {
	s := New()
	s.ApplyKey("MUTE")
	if err := s.SetVolume(context.Background(), 47); err != nil {
		t.Fatal(err)
	}
	if st := s.StatusSnapshot(); st.Muted || st.Output() != 47 {
		t.Fatalf("after SetVolume: %+v, want unmuted at 47", st)
	}
	if s.Muted() {
		t.Fatal("Muted() still true")
	}
}

func TestMutePushesTheChangeToListeners(t *testing.T) {
	s := New()
	fired := 0
	s.OnPlayChange(func() { fired++ })
	s.ApplyKey("MUTE")
	if fired != 1 {
		t.Fatalf("play-change hook fired %d times, want 1", fired)
	}
}

func TestSkipKeysReachTheTransportHook(t *testing.T) {
	s := New()
	var got []string
	s.OnTransport(func(action string) error { got = append(got, action); return nil })
	s.ApplyKey("SOURCE_TOGGLE") // leave standby
	s.ApplyKey("NEXT_TRACK")
	s.ApplyKey("PREV_TRACK")
	if len(got) != 2 || got[0] != "NEXT_TRACK" || got[1] != "PREV_TRACK" {
		t.Fatalf("hook saw %v, want [NEXT_TRACK PREV_TRACK]", got)
	}
}

// A remote pointed at a sleeping speaker must not wake the amp through a skip.
func TestSkipKeysDoNothingInStandby(t *testing.T) {
	s := New()
	called := false
	s.OnTransport(func(string) error { called = true; return nil })
	s.ApplyKey("NEXT_TRACK")
	if called {
		t.Fatal("skip reached the transport hook from standby")
	}
}

func TestSkipKeysAreHarmlessWithoutAHook(t *testing.T) {
	s := New()
	s.ApplyKey("SOURCE_TOGGLE")
	s.ApplyKey("NEXT_TRACK") // must not panic on a nil hook
}

// Selecting Bluetooth runs the select hook once per switch to it, whichever path picked
// it; staying on Bluetooth does not run it again.
func TestBluetoothSelectHook(t *testing.T) {
	sp := New()
	calls := make(chan struct{}, 4)
	sp.OnBluetoothSelect(func() { calls <- struct{}{} })
	sp.ApplyKey("AUX_INPUT")
	sp.ApplyKey("BLUETOOTH")
	sp.ApplyKey("BLUETOOTH")
	if err := sp.SetSource(context.Background(), "BLUETOOTH"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("the hook did not run when Bluetooth was selected")
	}
	select {
	case <-calls:
		t.Fatal("the hook ran again while Bluetooth stayed the source")
	case <-time.After(100 * time.Millisecond):
	}
	sp.ApplyKey("SOURCE_TOGGLE") // to AUX
	sp.ApplyKey("SOURCE_TOGGLE") // back to Bluetooth
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("the hook did not run on the way back to Bluetooth")
	}
}

// The AUX input plays under the name the person gave it, and as "AUX" without one.
func TestAuxName(t *testing.T) {
	sp := New()
	name := ""
	sp.AuxName(func() string { return name })
	sp.ApplyKey("AUX_INPUT")
	station := func() string {
		np, err := sp.NowPlaying(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return np.Station
	}
	if got := station(); got != "AUX" {
		t.Errorf("unnamed AUX plays as %q, want AUX", got)
	}
	name = "Record player"
	if got := station(); got != name {
		t.Errorf("named AUX plays as %q, want %q", got, name)
	}
}

func TestMarkMutedKeepsTheLevelAndSendsNoVolume(t *testing.T) {
	s := New()
	ctx := context.Background()
	if err := s.SetVolume(ctx, 6); err != nil {
		t.Fatal(err)
	}
	fired := 0
	s.OnVolume(func(int) { fired++ })
	s.MarkMuted()
	st := s.StatusSnapshot()
	if !st.Muted || st.Volume != 6 || fired != 0 {
		t.Fatalf("muted=%v volume=%d hooks=%d", st.Muted, st.Volume, fired)
	}
	if err := s.SetVolume(ctx, 6); err != nil {
		t.Fatal(err)
	}
	if st := s.StatusSnapshot(); st.Muted || st.Output() != 6 {
		t.Fatalf("after unmute muted=%v output=%d", st.Muted, st.Output())
	}
}
