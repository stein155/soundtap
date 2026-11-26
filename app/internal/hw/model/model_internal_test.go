// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package model

import (
	"os"
	"slices"
	"testing"

	"github.com/stein155/soundtap/app/internal/install"
)

// Every model with an analogue input must offer it over the LAN API. This was a name check
// on "st10" in main.go; wla() and st20() derive from st10() and rename themselves, so both
// lost the list and setUserSource then rejected every source. On the Wireless Link Adapter
// that meant AUX -- its only analogue input -- could not be selected from the web UI.
func TestModelsWithAnalogueInputOfferIt(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"st10", "st20", "wla"} {
		profile, ok := profiles()[name]
		if !ok {
			t.Fatalf("model %q is gone; this test names the models on purpose", name)
		}

		model := profile()

		var aux, bluetooth bool
		for _, source := range model.UserSources {
			aux = aux || source == "AUX"
			bluetooth = bluetooth || source == "BLUETOOTH"
		}

		if !aux {
			t.Errorf("%s does not offer AUX over the LAN API", name)
		}

		if !bluetooth {
			t.Errorf("%s does not offer BLUETOOTH over the LAN API", name)
		}
	}
}

// Capabilities describe hardware, so they come from the profile. A model union would be
// wrong the day a new board appears, and the Wireless Link already breaks it: same
// profile ancestry as the ST10, none of its keypad, infrared or display.
func TestCapabilitiesComeFromTheProfile(t *testing.T) {
	t.Parallel()

	st10 := st10().Capabilities()
	for _, want := range []string{"bluetooth", "ir", "keypad", "aux"} {
		if !slices.Contains(st10, want) {
			t.Errorf("st10 capabilities %v are missing %q", st10, want)
		}
	}

	wla := wla().Capabilities()
	if !slices.Contains(wla, "bluetooth") {
		t.Errorf("wla capabilities %v are missing bluetooth; its radio is attached by the "+
			"kernel, not by hciattach, so an empty BTUart does not mean no Bluetooth", wla)
	}

	for _, unwanted := range []string{"ir", "keypad"} {
		if slices.Contains(wla, unwanted) {
			t.Errorf("wla capabilities %v claim %q, which this board does not have", wla, unwanted)
		}
	}
	// Never nil: the JSON has to be [] rather than null, or every client has to guard it.
	if Unknown().Capabilities() == nil {
		t.Error("an unknown model returns nil capabilities instead of an empty list")
	}
}

// AUX is offered only where the stock source node routes it to the amplifier.
func TestAuxNeedsARoute(t *testing.T) {
	t.Parallel()

	profile := st10()
	dir := t.TempDir()
	profile.ShelbySource = dir + "/source"

	profile.AuxCapture = dir + "/pcmC0D0c"

	got := withoutUnroutedAux(&profile)
	if slices.Contains(got.UserSources, sourceAux) || slices.Contains(got.Capabilities(), "aux") {
		t.Errorf("AUX offered with no route: %v", got.UserSources)
	}

	err := os.WriteFile(profile.ShelbySource, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got = withoutUnroutedAux(&profile)
	if !slices.Contains(got.UserSources, sourceAux) || !slices.Contains(got.UserSources, sourceBluetooth) {
		t.Errorf("routed model lost a source: %v", got.UserSources)
	}
	// Under the mainline kernel the capture node is the route.
	profile.ShelbySource = ""

	err = os.WriteFile(profile.AuxCapture, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if got := withoutUnroutedAux(&profile); !slices.Contains(got.UserSources, sourceAux) {
		t.Errorf("AUX dropped although the line input can be captured: %v", got.UserSources)
	}
}

// No identity is no hardware: the old fallback to the ST10 profile drove the ST10's
// GPIOs and codec on whatever board it ran on.
func TestCurrentModelFailsLoudlyWhenUnknown(t *testing.T) {
	if len(install.LiveVariants()) > 0 {
		t.Skip("this host reports a kernel variant")
	}

	for _, env := range []string{"", "does-not-exist", "unknown"} {
		t.Setenv("MODEL", env)

		profile, err := currentModel()
		if err == nil || profile.Name != "unknown" {
			t.Fatalf("MODEL=%q gave %q, err %v; want unknown with an error", env, profile.Name, err)
		}

		if profile.AudioDev != "" || profile.MuteGPIO != "" || profile.AmpPowerGPIO != "" ||
			profile.AICConfig != "" || profile.KeypadDev != "" || profile.LEDDir != "" ||
			profile.IRDev != "" || profile.OLED != "" || profile.BTUart != "" {
			t.Fatalf("MODEL=%q: unknown model names hardware: %+v", env, profile)
		}
	}

	t.Setenv("MODEL", " ST20 ")

	profile, err := currentModel()
	if err != nil || profile.Name != ST20 {
		t.Fatalf("MODEL=ST20 gave %q, err %v", profile.Name, err)
	}
}

func TestUnprovenModelsEnableNoHardwareControls(t *testing.T) {
	t.Parallel()

	if model := wla(); model.AudioDev != "alsa:plughw:0,0" || model.AICConfig != "" || model.MuteGPIO != "" ||
		model.KeypadDev != "" || model.LEDDir != "" || model.BTUart != "" {
		t.Fatalf("WLA enabled unverified hardware controls: %+v", model)
	}

	if model := st300(); model.AudioDev != "alsa:plughw:0,0" ||
		!slices.Equal(model.UserSources, []string{sourceTV, sourceHDMI, sourceBluetooth}) ||
		!slices.Equal(model.Capabilities(), []string{"bluetooth"}) {
		t.Fatalf("ST300 enabled unverified hardware controls: %+v", model)
	}
}

// Stock plays chimes at a per-model level: APConfig-rhino.xml 41,
// APConfig-spotty.xml 40.
func TestChimeVolumeIsPerModel(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		profile Model
		want    int
	}{
		{st10(), 41},
		{st20(), 40},
		{wla(), 40},
		{st300(), 40},
		{st30(), 28},
		{seriesIII("spotty3"), 40},
		{seriesIII("mojo3"), 28},
		{Unknown(), 40},
	} {
		if testCase.profile.ChimeVolume != testCase.want {
			t.Errorf("%s ChimeVolume = %d, want %d", testCase.profile.Name, testCase.profile.ChimeVolume, testCase.want)
		}
	}
}

// Stock's per-source input gain: network sources about 8 dB under AUX on the
// ST10 and ST20; the WLA lifts AUX by 1.22 and the ST300 keeps both at unity.
func TestSourceGainPerModel(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		model     Model
		wifi, aux float64
	}{
		{st10(), 0.39810699, 1},
		{st20(), 0.39810699, 1},
		{wla(), 1, 1.22},
		{st300(), 1, 1},
		{Unknown(), 1, 1},
	} {
		if got := testCase.model.SourceGain("TUNEIN"); got != testCase.wifi {
			t.Errorf("%s network gain = %v, want %v", testCase.model.Name, got, testCase.wifi)
		}

		if got := testCase.model.SourceGain(sourceAux); got != testCase.aux {
			t.Errorf("%s AUX gain = %v, want %v", testCase.model.Name, got, testCase.aux)
		}
	}
}

// The SoundTouch 30 Series I is the ST20 but for its name and its codec script.
func TestST30IsTheST20Board(t *testing.T) {
	t.Parallel()

	profile, want := st30(), st20()
	if profile.Name != ST20 || profile.Type != "SoundTouch 30" || profile.AICConfig != "mojo_scm_normal.cfg" {
		t.Fatalf("st30() = %q %q %q", profile.Name, profile.Type, profile.AICConfig)
	}

	profile.Type, profile.AICConfig = want.Type, want.AICConfig
	if profile.KeypadDev != want.KeypadDev || profile.AudioDev != want.AudioDev || profile.MuteGPIO != want.MuteGPIO {
		t.Fatal("st30() differs from st20() beyond its name and codec script")
	}
}

// The Series III is the ST10's module: its keypad, amplifier and IR, with the ST20's panel
// on the ST10's other lamp pads.
func TestSeriesIIIIsTheST10Module(t *testing.T) {
	t.Parallel()

	for variant, want := range map[string][2]string{
		"spotty3": {"SoundTouch 20", "spotty_sm2_normal.cfg"},
		"mojo3":   {"SoundTouch 30", "mojo_sm2_normal.cfg"},
	} {
		profile, st10 := seriesIII(variant), st10()
		if profile.Name != ST10 || profile.Type != want[0] || profile.AICConfig != want[1] ||
			profile.LEDDir != "/sys/class/leds" ||
			profile.OLED != "spidev:/dev/spidev1.0?ce=gpio0_12&reset=gpio2_9&dc=gpio2_12" {
			t.Errorf("seriesIII(%s) = %q %q %q LEDDir %q OLED %q",
				variant, profile.Name, profile.Type, profile.AICConfig, profile.LEDDir, profile.OLED)
		}

		if profile.KeypadDev != st10.KeypadDev || profile.MuteGPIO != st10.MuteGPIO ||
			profile.IRDev != st10.IRDev || profile.BTUart != st10.BTUart {
			t.Errorf("seriesIII(%s) differs from st10() in its board wiring", variant)
		}
	}
}

// Stock starts the ST10 and the Wireless Link at 50 and the rest at 30 (BoseApp-<variant>.xml).
func TestDefaultVolumeIsStocks(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		profile Model
		want    int
	}{
		"st10":            {st10(), 50},
		"wla":             {wla(), 50},
		"st20":            {st20(), 30},
		"st30":            {st30(), 30},
		"st300":           {st300(), 30},
		"st20 series III": {seriesIII("spotty3"), 30},
		"st30 series III": {seriesIII("mojo3"), 30},
	} {
		if testCase.profile.DefaultVolume != testCase.want {
			t.Errorf("%s = %d, want %d", name, testCase.profile.DefaultVolume, testCase.want)
		}
	}
}
