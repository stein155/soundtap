// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate_test

import (
	"context"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func TestMuteKeepsTheLevelAndToggles(t *testing.T) {
	t.Parallel()

	spk := speakerstate.New()
	spk.ApplyKey("VOLUME_UP") // 30 -> 31, so the restored level is not the default
	spk.ApplyKey("MUTE")

	st := spk.StatusSnapshot()
	if st.Volume != 31 || !st.Muted || st.Output() != 0 {
		t.Fatalf("muted: %+v, want level 31 muted, output 0", st)
	}

	spk.ApplyKey("MUTE")

	if st := spk.StatusSnapshot(); st.Volume != 31 || st.Muted || st.Output() != 31 {
		t.Fatalf("unmuted: %+v, want level 31 heard", st)
	}
}

// Either volume button on a muted speaker only unmutes, at the level it had.
func TestVolumeKeysUnmuteWithoutStepping(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"VOLUME_UP", "VOLUME_DOWN"} {
		spk := speakerstate.New()

		err := spk.SetVolume(context.Background(), 40)
		if err != nil {
			t.Fatal(err)
		}

		spk.ApplyKey("MUTE")
		spk.ApplyKey(key)

		if st := spk.StatusSnapshot(); st.Muted || st.Volume != 40 {
			t.Fatalf("%s while muted: %+v, want unmuted at 40", key, st)
		}

		spk.ApplyKey(key) // the next press steps

		want := 41
		if key == "VOLUME_DOWN" {
			want = 39
		}

		if got := spk.StatusSnapshot().Volume; got != want {
			t.Fatalf("%s: volume %d, want %d", key, got, want)
		}
	}
}

// Setting a level, from the API or a phone, is asking to hear it.
func TestSetVolumeUnmutes(t *testing.T) {
	t.Parallel()

	spk := speakerstate.New()
	spk.ApplyKey("MUTE")

	err := spk.SetVolume(context.Background(), 47)
	if err != nil {
		t.Fatal(err)
	}

	if st := spk.StatusSnapshot(); st.Muted || st.Output() != 47 {
		t.Fatalf("after SetVolume: %+v, want unmuted at 47", st)
	}

	if spk.Muted() {
		t.Fatal("Muted() still true")
	}
}

func TestMutePushesTheChangeToListeners(t *testing.T) {
	t.Parallel()

	s := speakerstate.New()
	fired := 0

	s.OnPlayChange(func() { fired++ })
	s.ApplyKey("MUTE")

	if fired != 1 {
		t.Fatalf("play-change hook fired %d times, want 1", fired)
	}
}

func TestSkipKeysReachTheTransportHook(t *testing.T) {
	t.Parallel()

	spk := speakerstate.New()

	var got []string

	spk.OnTransport(func(action string) error {
		got = append(got, action)

		return nil
	})
	spk.ApplyKey("SOURCE_TOGGLE") // leave standby
	spk.ApplyKey("NEXT_TRACK")
	spk.ApplyKey("PREV_TRACK")

	if len(got) != 2 || got[0] != "NEXT_TRACK" || got[1] != "PREV_TRACK" {
		t.Fatalf("hook saw %v, want [NEXT_TRACK PREV_TRACK]", got)
	}
}

// A remote pointed at a sleeping speaker must not wake the amp through a skip.
func TestSkipKeysDoNothingInStandby(t *testing.T) {
	t.Parallel()

	spk := speakerstate.New()
	called := false

	spk.OnTransport(func(string) error {
		called = true

		return nil
	})
	spk.ApplyKey("NEXT_TRACK")

	if called {
		t.Fatal("skip reached the transport hook from standby")
	}
}

func TestSkipKeysAreHarmlessWithoutAHook(t *testing.T) {
	t.Parallel()

	s := speakerstate.New()
	s.ApplyKey("SOURCE_TOGGLE")
	s.ApplyKey("NEXT_TRACK") // must not panic on a nil hook
}

// Selecting Bluetooth runs the select hook once per switch to it, whichever path picked
// it; staying on Bluetooth does not run it again.
func TestBluetoothSelectHook(t *testing.T) {
	t.Parallel()

	spk := speakerstate.New()
	calls := make(chan struct{}, 4)

	spk.OnBluetoothSelect(func() { calls <- struct{}{} })
	spk.ApplyKey("AUX_INPUT")
	spk.ApplyKey("BLUETOOTH")
	spk.ApplyKey("BLUETOOTH")

	err := spk.SetSource(context.Background(), "BLUETOOTH")
	if err != nil {
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

	spk.ApplyKey("SOURCE_TOGGLE") // to AUX
	spk.ApplyKey("SOURCE_TOGGLE") // back to Bluetooth

	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("the hook did not run on the way back to Bluetooth")
	}
}

// The AUX input plays under the name the person gave it, and as "AUX" without one.
func TestAuxName(t *testing.T) {
	t.Parallel()

	spk := speakerstate.New()
	name := ""

	spk.AuxName(func() string { return name })
	spk.ApplyKey("AUX_INPUT")

	station := func() string {
		np, err := spk.NowPlaying(context.Background())
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
	t.Parallel()

	spk := speakerstate.New()

	ctx := context.Background()

	err := spk.SetVolume(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}

	fired := 0

	spk.OnVolume(func(int) { fired++ })
	spk.MarkMuted()

	st := spk.StatusSnapshot()
	if !st.Muted || st.Volume != 6 || fired != 0 {
		t.Fatalf("muted=%v volume=%d hooks=%d", st.Muted, st.Volume, fired)
	}

	err = spk.SetVolume(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}

	if st := spk.StatusSnapshot(); st.Muted || st.Output() != 6 {
		t.Fatalf("after unmute muted=%v output=%d", st.Muted, st.Output())
	}
}
