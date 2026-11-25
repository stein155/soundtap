// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func TestWLALEDPattern(t *testing.T) {
	on := speakerstate.StatusSnapshot{}
	standby := speakerstate.StatusSnapshot{Standby: true}

	cases := []struct {
		name     string
		st       speakerstate.StatusSnapshot
		link     Link
		booting  bool
		updating bool
		want     wlaLEDs
	}{
		{
			"setup AP is solid amber", on, LinkSetupAP, false, false,
			wlaLEDs{wifiAmber: wlaSolid(wlaOnLevel)},
		},
		{
			"connected is solid white", on, LinkUp, false, false,
			wlaLEDs{wifiWhite: wlaSolid(wlaOnLevel)},
		},
		{
			"standby dims the same lamp", standby, LinkUp, false, false,
			wlaLEDs{wifiWhite: wlaSolid(wlaStandbyLevel)},
		},
		{
			"connecting blinks white fast", on, LinkConnecting, false, false,
			wlaLEDs{wifiWhite: wlaBlink(wlaFastBlinkMS, wlaOnLevel)},
		},
		{
			"booting looks like connecting", on, LinkUp, true, false,
			wlaLEDs{wifiWhite: wlaBlink(wlaFastBlinkMS, wlaOnLevel)},
		},
		{
			"no network blinks amber slowly", on, LinkDown, false, false,
			wlaLEDs{wifiAmber: wlaBlink(wlaSlowBlinkMS, wlaOnLevel)},
		},
		{
			"an update outranks the network", on, LinkUp, false, true,
			wlaLEDs{wifiWhite: []wlaStep{{3000, wlaOnLevel}, {250, 0}, {250, wlaOnLevel}, {250, 0}}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wlaLEDPattern(c.st, c.link, c.booting, c.updating, false); !sameWLALEDs(got, c.want) {
				t.Fatalf("pattern = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestWLALEDBluetoothLamp(t *testing.T) {
	playing := speakerstate.StatusSnapshot{Source: "BLUETOOTH", PlayStatus: "PLAY_STATE"}
	if got := wlaLEDPattern(playing, LinkUp, false, false, false); !sameWLASteps(got.btWhite, wlaSolid(wlaOnLevel)) {
		t.Fatalf("playing over Bluetooth should light the lamp solid white, got %+v", got)
	}

	waiting := speakerstate.StatusSnapshot{Source: "BLUETOOTH"}
	if got := wlaLEDPattern(waiting, LinkUp, false, false, false); len(got.btBlue) == 0 {
		t.Fatalf("waiting for a phone should show blue, got %+v", got)
	}
	// Every other source leaves the Bluetooth lamp alone.
	other := speakerstate.StatusSnapshot{Source: "SPOTIFY", PlayStatus: "PLAY_STATE"}
	if got := wlaLEDPattern(other, LinkUp, false, false, false); len(got.btWhite)+len(got.btBlue) != 0 {
		t.Fatalf("a non-Bluetooth source should leave the lamp dark, got %+v", got)
	}
}

// Stock acknowledges a transport key with one blink of the Bluetooth lamp: 250 ms
// dark, then 250 ms lit, once. It replaces whatever that lamp was showing.
func TestWLATransportAckBlinksTheSourceLamp(t *testing.T) {
	playing := speakerstate.StatusSnapshot{
		Source: "BLUETOOTH", PlayStatus: "PLAY_STATE", TransportAck: true,
	}
	got := wlaLEDPattern(playing, LinkUp, false, false, false)

	want := []wlaStep{{250, 0}, {250, wlaOnLevel}}
	if !sameWLASteps(got.btWhite, want) {
		t.Fatalf("btWhite = %+v, want %+v", got.btWhite, want)
	}

	if len(got.btBlue) != 0 {
		t.Fatalf("the blue channel must be dark during the blink, got %+v", got.btBlue)
	}
	// Dark first: a blink that starts lit is invisible against a lamp already lit.
	if lvl := wlaLevelAt(got.btWhite, 0); lvl != 0 {
		t.Fatalf("the blink starts at %d, want it to start dark", lvl)
	}
	// Once the window lapses the lamp goes back to reporting the source.
	playing.TransportAck = false
	if got := wlaLEDPattern(playing, LinkUp, false, false, false); !sameWLASteps(got.btWhite, wlaSolid(wlaOnLevel)) {
		t.Fatalf("after the window the lamp should be solid again, got %+v", got.btWhite)
	}
	// No acknowledgement on a source stock does not acknowledge.
	other := speakerstate.StatusSnapshot{Source: "SPOTIFY", TransportAck: true}
	if got := wlaLEDPattern(other, LinkUp, false, false, false); len(got.btWhite)+len(got.btBlue) != 0 {
		t.Fatalf("only Bluetooth acknowledges, got %+v", got)
	}
}

func TestWLALevelAt(t *testing.T) {
	blink := wlaBlink(wlaFastBlinkMS, wlaOnLevel)
	for _, c := range []struct {
		at   time.Duration
		want int
	}{
		{0, wlaOnLevel},
		{349 * time.Millisecond, wlaOnLevel},
		{350 * time.Millisecond, 0},
		{699 * time.Millisecond, 0},
		{700 * time.Millisecond, wlaOnLevel}, // wraps
	} {
		if got := wlaLevelAt(blink, c.at); got != c.want {
			t.Fatalf("level at %s = %d, want %d", c.at, got, c.want)
		}
	}

	if got := wlaLevelAt(nil, time.Second); got != 0 {
		t.Fatalf("a channel with no animation is dark, got %d", got)
	}
}

func TestWLANextStep(t *testing.T) {
	ms := time.Millisecond

	blink := wlaBlink(350, 100)
	for _, c := range []struct {
		steps   []wlaStep
		elapsed time.Duration
		want    time.Duration
	}{
		{nil, 0, 0},
		{wlaSolid(100), 5 * time.Second, 0},
		{blink, 0, 350 * ms},
		{blink, 100 * ms, 250 * ms},
		{blink, 400 * ms, 300 * ms},
		{blink, 700 * ms, 350 * ms}, // wrapped: the next period begins
	} {
		if got := wlaNextStep(c.steps, c.elapsed); got != c.want {
			t.Errorf("wlaNextStep(%v, %v) = %v, want %v", c.steps, c.elapsed, got, c.want)
		}
	}
}
