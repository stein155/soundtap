// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func TestWLALEDPattern(t *testing.T) {
	t.Parallel()

	awake := snapshot("", "", 0)
	standby := snapshot("", "", snapStandby)

	cases := []struct {
		name     string
		st       *speakerstate.StatusSnapshot
		link     Link
		booting  bool
		updating bool
		want     wlaLEDs
	}{
		{
			"setup AP is solid amber", awake, LinkSetupAP, false, false,
			wlaLEDs{wifiWhite: nil, wifiAmber: wlaSolid(wlaOnLevel), btWhite: nil, btBlue: nil},
		},
		{
			"connected is solid white", awake, LinkUp, false, false,
			wlaLEDs{wifiWhite: wlaSolid(wlaOnLevel), wifiAmber: nil, btWhite: nil, btBlue: nil},
		},
		{
			"standby dims the same lamp", standby, LinkUp, false, false,
			wlaLEDs{wifiWhite: wlaSolid(wlaStandbyLevel), wifiAmber: nil, btWhite: nil, btBlue: nil},
		},
		{
			"connecting blinks white fast", awake, LinkConnecting, false, false,
			wlaLEDs{wifiWhite: wlaBlink(wlaFastBlinkMS, wlaOnLevel), wifiAmber: nil, btWhite: nil, btBlue: nil},
		},
		{
			"booting looks like connecting", awake, LinkUp, true, false,
			wlaLEDs{wifiWhite: wlaBlink(wlaFastBlinkMS, wlaOnLevel), wifiAmber: nil, btWhite: nil, btBlue: nil},
		},
		{
			"no network blinks amber slowly", awake, LinkDown, false, false,
			wlaLEDs{wifiWhite: nil, wifiAmber: wlaBlink(wlaSlowBlinkMS, wlaOnLevel), btWhite: nil, btBlue: nil},
		},
		{
			"an update outranks the network", awake, LinkUp, false, true,
			wlaLEDs{
				wifiWhite: []wlaStep{{3000, wlaOnLevel}, {250, 0}, {250, wlaOnLevel}, {250, 0}},
				wifiAmber: nil, btWhite: nil, btBlue: nil,
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := wlaLEDPattern(testCase.st, testCase.link, testCase.booting, testCase.updating, false)
			if !sameWLALEDs(&got, &testCase.want) {
				t.Fatalf("pattern = %+v, want %+v", got, testCase.want)
			}
		})
	}
}

func TestWLALEDBluetoothLamp(t *testing.T) {
	t.Parallel()

	playing := snapshot("BLUETOOTH", "PLAY_STATE", 0)
	if got := wlaLEDPattern(playing, LinkUp, false, false, false); !sameWLASteps(got.btWhite, wlaSolid(wlaOnLevel)) {
		t.Fatalf("playing over Bluetooth should light the lamp solid white, got %+v", got)
	}

	waiting := snapshot("BLUETOOTH", "", 0)
	if got := wlaLEDPattern(waiting, LinkUp, false, false, false); len(got.btBlue) == 0 {
		t.Fatalf("waiting for a phone should show blue, got %+v", got)
	}
	// Every other source leaves the Bluetooth lamp alone.
	other := snapshot("SPOTIFY", "PLAY_STATE", 0)
	if got := wlaLEDPattern(other, LinkUp, false, false, false); len(got.btWhite)+len(got.btBlue) != 0 {
		t.Fatalf("a non-Bluetooth source should leave the lamp dark, got %+v", got)
	}
}

// Stock acknowledges a transport key with one blink of the Bluetooth lamp: 250 ms
// dark, then 250 ms lit, once. It replaces whatever that lamp was showing.
func TestWLATransportAckBlinksTheSourceLamp(t *testing.T) {
	t.Parallel()

	playing := snapshot("BLUETOOTH", "PLAY_STATE", snapTransportAck)
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
	other := snapshot("SPOTIFY", "", snapTransportAck)
	if got := wlaLEDPattern(other, LinkUp, false, false, false); len(got.btWhite)+len(got.btBlue) != 0 {
		t.Fatalf("only Bluetooth acknowledges, got %+v", got)
	}
}

func TestWLALevelAt(t *testing.T) {
	t.Parallel()

	blink := wlaBlink(wlaFastBlinkMS, wlaOnLevel)
	for _, testCase := range []struct {
		at   time.Duration
		want int
	}{
		{0, wlaOnLevel},
		{349 * time.Millisecond, wlaOnLevel},
		{350 * time.Millisecond, 0},
		{699 * time.Millisecond, 0},
		{700 * time.Millisecond, wlaOnLevel}, // wraps
	} {
		if got := wlaLevelAt(blink, testCase.at); got != testCase.want {
			t.Fatalf("level at %s = %d, want %d", testCase.at, got, testCase.want)
		}
	}

	if got := wlaLevelAt(nil, time.Second); got != 0 {
		t.Fatalf("a channel with no animation is dark, got %d", got)
	}
}

func TestWLANextStep(t *testing.T) {
	t.Parallel()

	millisecond := time.Millisecond

	blink := wlaBlink(350, 100)
	for _, testCase := range []struct {
		steps   []wlaStep
		elapsed time.Duration
		want    time.Duration
	}{
		{nil, 0, 0},
		{wlaSolid(100), 5 * time.Second, 0},
		{blink, 0, 350 * millisecond},
		{blink, 100 * millisecond, 250 * millisecond},
		{blink, 400 * millisecond, 300 * millisecond},
		{blink, 700 * millisecond, 350 * millisecond}, // wrapped: the next period begins
	} {
		if got := wlaNextStep(testCase.steps, testCase.elapsed); got != testCase.want {
			t.Errorf("wlaNextStep(%v, %v) = %v, want %v", testCase.steps, testCase.elapsed, got, testCase.want)
		}
	}
}
