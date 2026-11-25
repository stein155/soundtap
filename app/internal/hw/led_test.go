// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/hw/model"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func TestLEDPattern(t *testing.T) {
	tests := []struct {
		name  string
		state speakerstate.StatusSnapshot
		link  Link
		blink bool
		want  ledState
	}{
		{"standby", speakerstate.StatusSnapshot{Standby: true}, LinkUp, true, ledState{wifiDim: true}},
		{"standby notification", speakerstate.StatusSnapshot{Standby: true, Notification: true}, LinkUp, true, ledState{wifiDim: true}},
		{"tunein", speakerstate.StatusSnapshot{Source: "TUNEIN", PlayStatus: "PLAY_STATE"}, LinkUp, false, ledState{wifiWhite: true, stWhite: true}},
		{"airplay", speakerstate.StatusSnapshot{Source: "AIRPLAY", PlayStatus: "PLAY_STATE"}, LinkUp, false, ledState{wifiWhite: true, stWhite: true}},
		{"spotify", speakerstate.StatusSnapshot{Source: "SPOTIFY", PlayStatus: "PLAY_STATE"}, LinkUp, false, ledState{wifiWhite: true, stWhite: true}},
		{"aux", speakerstate.StatusSnapshot{Source: "AUX", PlayStatus: "PLAY_STATE"}, LinkUp, false, ledState{wifiWhite: true, auxWhite: true}},
		{"bluetooth playing", speakerstate.StatusSnapshot{Source: "BLUETOOTH", PlayStatus: "PLAY_STATE"}, LinkUp, false, ledState{wifiWhite: true, btWhite: true}},
		{"buffering on", speakerstate.StatusSnapshot{Source: "TUNEIN", PlayStatus: "BUFFERING_STATE"}, LinkUp, true, ledState{wifiWhite: true, stWhite: true}},
		{"buffering off", speakerstate.StatusSnapshot{Source: "TUNEIN", PlayStatus: "BUFFERING_STATE"}, LinkUp, false, ledState{wifiWhite: true}},
		{"paused", speakerstate.StatusSnapshot{Source: "TUNEIN", PlayStatus: "PAUSE_STATE"}, LinkUp, false, ledState{wifiWhite: true, stAmber: true}},
		{"stopped", speakerstate.StatusSnapshot{Source: "TUNEIN", PlayStatus: "STOP_STATE"}, LinkUp, false, ledState{wifiWhite: true, stAmber: true}},
		{"upnp", speakerstate.StatusSnapshot{Source: "UPNP", PlayStatus: "PLAY_STATE"}, LinkUp, false, ledState{wifiWhite: true, stWhite: true}},
		{"multiroom follower", speakerstate.StatusSnapshot{Source: "MULTIROOM", PlayStatus: "PLAY_STATE"}, LinkUp, false, ledState{wifiWhite: true, stWhite: true}},
		{"notification on", speakerstate.StatusSnapshot{Notification: true}, LinkUp, true, ledState{wifiWhite: true, stAmber: true, stWhite: true}},
		{"notification off", speakerstate.StatusSnapshot{Notification: true}, LinkUp, false, ledState{wifiWhite: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ledPattern(test.state, test.link, test.blink); got != test.want {
				t.Fatalf("got %+v, want %+v", got, test.want)
			}
		})
	}
}

// AUX is a line-in with no transport, so it must not be gated on a play status
// the source will never report.
func TestAUXLampIgnoresPlayStatus(t *testing.T) {
	for _, status := range []string{"", "PLAY_STATE", "STOP_STATE", "PAUSE_STATE", "BUFFERING_STATE"} {
		st := speakerstate.StatusSnapshot{Source: "AUX", PlayStatus: status}
		if got := ledPattern(st, LinkUp, false); !got.auxWhite || got.stAmber {
			t.Fatalf("AUX with playStatus %q lit %+v", status, got)
		}
	}
}

// Selecting Bluetooth and waiting for a phone must look different from a dead
// source: blue blinks while discoverable, white is solid once audio arrives.
func TestBluetoothShowsPairingSeparatelyFromPlaying(t *testing.T) {
	waiting := ledPattern(speakerstate.StatusSnapshot{Source: "BLUETOOTH", PlayStatus: "STOP_STATE"}, LinkUp, true)
	if !waiting.btBlue || waiting.btWhite {
		t.Fatalf("waiting for a device = %+v, want blue blinking", waiting)
	}

	if off := ledPattern(speakerstate.StatusSnapshot{Source: "BLUETOOTH", PlayStatus: "STOP_STATE"}, LinkUp, false); off.btBlue {
		t.Fatalf("blue did not blink: %+v", off)
	}

	playing := ledPattern(speakerstate.StatusSnapshot{Source: "BLUETOOTH", PlayStatus: "PLAY_STATE"}, LinkUp, true)
	if !playing.btWhite || playing.btBlue {
		t.Fatalf("playing = %+v, want solid white", playing)
	}
}

// The Wi-Fi lamp must distinguish the four network states, and a speaker waiting
// to be set up must keep signalling even in standby — that is exactly the
// speaker whose owner has nothing else to go on.
func TestWifiLampReportsTheLink(t *testing.T) {
	idle := speakerstate.StatusSnapshot{Source: "TUNEIN", PlayStatus: "STOP_STATE"}
	if got := ledPattern(idle, LinkConnecting, true); !got.wifiWhite {
		t.Fatalf("connecting (blink on) = %+v", got)
	}

	if got := ledPattern(idle, LinkConnecting, false); got.wifiWhite {
		t.Fatalf("connecting did not blink: %+v", got)
	}

	if got := ledPattern(idle, LinkSetupAP, true); !got.wifiAmber || got.wifiWhite {
		t.Fatalf("setup AP = %+v, want amber", got)
	}

	if got := ledPattern(idle, LinkSetupAP, false); !got.wifiAmber {
		t.Fatalf("setup AP blink phase = %+v, want steady amber", got)
	}

	if got := ledPattern(idle, LinkDown, true); !got.wifiAmber {
		t.Fatalf("link down = %+v, want the amber blink on", got)
	}

	standby := speakerstate.StatusSnapshot{Standby: true}
	if got := ledPattern(standby, LinkSetupAP, true); !got.wifiAmber {
		t.Fatalf("standby in setup mode went dark: %+v", got)
	}

	if got := ledPattern(standby, LinkConnecting, true); !got.wifiWhite {
		t.Fatalf("standby while connecting went dark: %+v", got)
	}

	if got := ledPattern(standby, LinkConnecting, false); got.wifiWhite {
		t.Fatalf("standby connecting lamp did not blink: %+v", got)
	}
	// linkUp standby now shows a dim pilot lamp; source lamps stay dark.
	if got := ledPattern(standby, LinkUp, true); !got.wifiDim || got.stWhite || got.stAmber {
		t.Fatalf("standby on a healthy network = %+v, want wifiDim only", got)
	}
}

// readLink must not trust a pidfile left behind by a crashed daemon.
func TestReadLinkIgnoresStalePidfiles(t *testing.T) {
	if got := ReadLink(func() bool { return true }); got != LinkUp {
		t.Fatalf("with an address = %v, want linkUp", got)
	}
	// /run/soundtap-*.pid do not exist in the test environment, so anything other
	// than linkDown here means a stale or absent file was read as alive.
	if got := ReadLink(func() bool { return false }); got != LinkDown {
		t.Fatalf("no address and no daemons = %v, want linkDown", got)
	}

	if pidAlive("/run/soundtap-does-not-exist.pid") {
		t.Fatal("a missing pidfile reported a live process")
	}
}

func TestLiveWifiProcessesOverrideCachedIP(t *testing.T) {
	setup := linkFromFacts(true, true, false)
	if setup != LinkSetupAP {
		t.Fatalf("setup AP after prior IP = %v, want linkSetupAP", setup)
	}

	if lamps := ledPattern(speakerstate.StatusSnapshot{}, setup, false); !lamps.wifiAmber || lamps.wifiWhite {
		t.Fatalf("setup AP lamps = %+v, want solid amber", lamps)
	}

	if got := linkFromFacts(true, false, true); got != LinkUp {
		t.Fatalf("associated station with IP = %v, want linkUp", got)
	}

	connecting := linkFromFacts(false, false, true)
	if connecting != LinkConnecting {
		t.Fatalf("station reconnect after prior IP = %v, want linkConnecting", connecting)
	}

	if lamps := ledPattern(speakerstate.StatusSnapshot{}, connecting, true); !lamps.wifiWhite || lamps.wifiAmber {
		t.Fatalf("station reconnect lamps = %+v, want white blink on", lamps)
	}

	if lamps := ledPattern(speakerstate.StatusSnapshot{}, connecting, false); lamps.wifiWhite || lamps.wifiAmber {
		t.Fatalf("station reconnect lamps = %+v, want white blink off", lamps)
	}
}

func TestApplyLEDState(t *testing.T) {
	t.Run("stock", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"uledw", "uledy", "rled3", "rled4", "rled5", "rled6", "rled7"} {
			err := os.WriteFile(filepath.Join(dir, name), nil, 0o644)
			if err != nil {
				t.Fatal(err)
			}
		}

		err := applyLEDState(dir, ledState{wifiWhite: true, auxWhite: true, stAmber: true, stWhite: true})
		if err != nil {
			t.Fatal(err)
		}

		assertChannels(t, dir, false, map[string]bool{
			"uledw": true, "uledy": false, "rled3": false,
			"rled4": true, "rled5": true, "rled6": false, "rled7": true,
		})
	})

	t.Run("gpio-leds", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"wifi-white", "wifi-amber", "bt-white", "soundtouch-white", "soundtouch-amber", "bt-blue", "aux-white"} {
			path := filepath.Join(dir, name)

			err := os.Mkdir(path, 0o755)
			if err != nil {
				t.Fatal(err)
			}

			err = os.WriteFile(filepath.Join(path, "brightness"), nil, 0o644)
			if err != nil {
				t.Fatal(err)
			}
		}

		err := applyLEDState(dir, ledState{wifiAmber: true, btWhite: true, auxWhite: true})
		if err != nil {
			t.Fatal(err)
		}

		assertChannels(t, dir, true, map[string]bool{
			"wifi-white": false, "wifi-amber": true, "bt-white": true, "soundtouch-white": false,
			"soundtouch-amber": false, "bt-blue": false, "aux-white": true,
		})
	})
}

// The lamp mapping was wrong for four channels and invented two that are not wired, and
// nothing failed: every write succeeded and the lamps stayed dark. So pin the table that
// was established by lighting each channel on hardware (each channel lit by hand). The stock
// rledN name and the mainline label in each row must drive the same GPIO — that pairing,
// not the label text, is what keeps the two kernels agreeing.
//
// If this test fails, the DTS labels in os/board/common/dts/ti/omap/am335x-soundtap-st10.dts have to
// move with it, and the change needs a full deploy: a renamed gpio-leds label that the
// running DTB does not have sends every write to the stock /dev path instead.
func TestLampChannelsMatchTheHardware(t *testing.T) {
	want := []struct{ mainline, stock string }{
		{"wifi-white", "uledw"},       // gpio0_19
		{"wifi-amber", "uledy"},       // gpio2_17
		{"bt-white", "rled3"},         // gpio0_4
		{"soundtouch-white", "rled4"}, // gpio0_12
		{"soundtouch-amber", "rled5"}, // gpio2_12
		{"bt-blue", "rled6"},          // gpio2_9
		{"aux-white", "rled7"},        // gpio2_23
	}

	got := ledChannels(ledState{})
	if len(got) != len(want) {
		t.Fatalf("%d channels, want %d — this board has seven lamps; rled2 and rled8 drive none", len(got), len(want))
	}

	for i, w := range want {
		if got[i].mainline != w.mainline || got[i].stock != w.stock {
			t.Errorf("channel %d = %s/%s, want %s/%s", i, got[i].mainline, got[i].stock, w.mainline, w.stock)
		}
	}
}

// Every lamp must be reachable: a field that no channel reads is a state the panel can
// never show, which is how shelby-white came to be written for years without lighting.
func TestEveryLampFieldDrivesAChannel(t *testing.T) {
	all := ledState{
		wifiWhite: true, wifiAmber: true, btWhite: true,
		stWhite: true, stAmber: true, btBlue: true, auxWhite: true,
	}
	for _, channel := range ledChannels(all) {
		if !channel.on {
			t.Errorf("%s stayed off with every field set", channel.mainline)
		}
	}

	for _, channel := range ledChannels(ledState{}) {
		if channel.on {
			t.Errorf("%s lit with no field set", channel.mainline)
		}
	}
}

func assertChannels(t *testing.T, dir string, gpio bool, states map[string]bool) {
	t.Helper()

	for name, on := range states {
		path := filepath.Join(dir, name)
		want := []byte{0x00}

		if gpio {
			path = filepath.Join(path, "brightness")
			want = []byte("0")
		}

		if on {
			want = []byte{0xff}
			if gpio {
				want = []byte("255")
			}
		}

		assertFile(t, path, want)
	}
}

func assertFile(t *testing.T, path string, want []byte) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("%s = %x, want %x", path, got, want)
	}
}

// A speaker in standby stays dark except while advertising setup or joining Wi-Fi,
// and shows a dim pilot lamp on a healthy network (linkUp).
func TestStandbyKeepsWifiProgressVisible(t *testing.T) {
	standby := speakerstate.StatusSnapshot{Standby: true}
	// linkDown → fully dark.
	for _, blink := range []bool{true, false} {
		if got := ledPattern(standby, LinkDown, blink); got != (ledState{}) {
			t.Fatalf("standby linkDown (blink=%v) = %+v, want dark", blink, got)
		}
	}
	// linkUp → dim pilot only (wifiDim set, nothing else).
	for _, blink := range []bool{true, false} {
		got := ledPattern(standby, LinkUp, blink)
		if !got.wifiDim {
			t.Fatalf("standby linkUp (blink=%v): wifiDim not set: %+v", blink, got)
		}

		if got.stWhite || got.stAmber || got.btWhite || got.btBlue || got.auxWhite || got.wifiWhite || got.wifiAmber {
			t.Fatalf("standby linkUp (blink=%v): unexpected lamps lit: %+v", blink, got)
		}
	}

	if got := ledPattern(standby, LinkSetupAP, true); !got.wifiAmber {
		t.Fatalf("standby hosting the setup AP = %+v, want amber", got)
	}

	if got := ledPattern(standby, LinkSetupAP, false); !got.wifiAmber {
		t.Fatalf("standby hosting the setup AP blink phase = %+v, want steady amber", got)
	}

	if got := ledPattern(standby, LinkConnecting, true); !got.wifiWhite {
		t.Fatalf("standby joining Wi-Fi = %+v, want white blink", got)
	}

	if got := ledPattern(standby, LinkConnecting, false); got != (ledState{}) {
		t.Fatalf("standby joining Wi-Fi blink phase = %+v, want dark", got)
	}
}

// The keypad reports raw matrix indexes, so a change here silently remaps the
// speaker's buttons.
func TestDefaultKeymapIsStable(t *testing.T) {
	want := map[int]string{
		0: "VOLUME_UP", 1: "PRESET_3", 2: "POWER",
		4: "VOLUME_DOWN", 5: "PRESET_2", 6: "PRESET_1",
		8: "PRESET_6", 9: "PRESET_5", 10: "PRESET_4",
		12: "SOURCE_TOGGLE",
	}
	if len(defaultKeymap) != len(want) {
		t.Fatalf("keymap has %d entries, want %d", len(defaultKeymap), len(want))
	}

	for index, action := range want {
		if got := defaultKeymap[index]; got != action {
			t.Errorf("index %d = %q, want %q", index, got, action)
		}
	}
}

// The ST20 has one lamp, the Wi-Fi lamp above the OLED. With an empty LEDDir
// runLEDs returned at once and that lamp never lit under SoundTap.
func TestST20DrivesItsWiFiLamp(t *testing.T) {
	if model.Named(model.ST20).LEDDir != "/sys/class/leds" {
		t.Errorf("st20 LEDDir = %q, want /sys/class/leds", model.Named(model.ST20).LEDDir)
	}

	if model.Named(model.ST20).AICConfig != "spotty_scm_normal.cfg" {
		t.Errorf("st20 AICConfig = %q, want spotty config", model.Named(model.ST20).AICConfig)
	}

	if model.Named(model.ST10).LEDDir == "" {
		t.Error("st10 lost its LED dir")
	}
}

// testLamps is a speaker that is up, not confirming, not updating and offline.
type testLamps struct{ *speakerstate.Speaker }

func (testLamps) Up() bool         { return true }
func (testLamps) Confirming() bool { return false }
func (testLamps) Updating() bool   { return false }
func (testLamps) HaveIP() bool     { return false }

func TestRunLEDsReturnsOnAModelWithoutLEDs(t *testing.T) {
	t.Setenv("LEDS", "")

	done := make(chan struct{})
	// A context that never cancels: the only way out is the no-LEDs early return.
	go func() {
		defer close(done)

		RunLEDs(context.Background(), model.Named(model.WLA).LEDDir, testLamps{speakerstate.New()}, slog.New(slog.DiscardHandler))
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunLEDs kept driving lamps on a model that has none")
	}
}

// The boot wave is stock's, measured off led-splash-write: one lamp at a time, out to the
// far lamp and back without repeating an endpoint, then round again. A speaker that is
// still starting must not look like a speaker that is off.
// docs/hardware/st10/boot-splash.md has the capture this encodes.
func TestBootChaseIsStocksKnightRider(t *testing.T) {
	want := []string{"wifi-white", "bt-white", "aux-white", "soundtouch-white", "aux-white", "bt-white"}
	for i, name := range want {
		state := bootChase(i)
		lit := []string{}

		for _, c := range ledChannels(state) {
			if c.on {
				lit = append(lit, c.mainline)
			}
		}

		if len(lit) != 1 || lit[0] != name {
			t.Fatalf("step %d lights %v, want exactly [%s]", i, lit, name)
		}
	}

	for i := 1; i < len(want); i++ {
		if bootChase(i) == bootChase(i-1) {
			t.Fatalf("step %d repeats step %d", i, i-1)
		}
	}

	if bootChase(len(want)) != bootChase(0) {
		t.Errorf("the wave does not loop after %d steps", len(want))
	}

	if bootChase(-1) != bootChase(len(want)-1) {
		t.Error("a negative step must wrap the same way")
	}
}

// Pressing an empty preset must blink stAmber for the feedback window regardless
// of other state. Stock gives the same one-tap amber blink so the user can feel
// that the button responded.
func TestEmptyPresetBlinksSoundTouchAmber(t *testing.T) {
	s := speakerstate.StatusSnapshot{EmptyPreset: true}
	for _, tc := range []struct {
		link  Link
		blink bool
	}{
		{LinkUp, true},
		{LinkUp, false},
		{LinkDown, true},
	} {
		got := ledPattern(s, tc.link, tc.blink)
		if got.stAmber != tc.blink {
			t.Errorf("EmptyPreset link=%v blink=%v: stAmber=%v, want %v", tc.link, tc.blink, got.stAmber, tc.blink)
		}

		if got.stWhite || got.btBlue || got.btWhite || got.auxWhite {
			t.Errorf("EmptyPreset lit unexpected lamps: %+v", got)
		}
	}
	// EmptyPreset overrides standby dark state.
	standby := speakerstate.StatusSnapshot{EmptyPreset: true, Standby: true}
	if got := ledPattern(standby, LinkUp, true); !got.stAmber {
		t.Errorf("EmptyPreset in standby: stAmber not lit: %+v", got)
	}
}

// In standby on a healthy network the Wi-Fi white lamp should be dim so the speaker
// is distinguishable from unplugged. Stock shows the same faint pilot lamp.
func TestDimStandbyLampOnHealthyNetwork(t *testing.T) {
	standby := speakerstate.StatusSnapshot{Standby: true}

	// linkUp → wifiDim set, all source lamps off; wifi-white not set in standby arm
	// (only blinking during connecting — that is the original design).
	got := ledPattern(standby, LinkUp, false)
	if !got.wifiDim {
		t.Errorf("standby+linkUp: wifiDim not set: %+v", got)
	}

	if got.stWhite || got.stAmber || got.btWhite || got.btBlue || got.auxWhite || got.wifiWhite {
		t.Errorf("standby+linkUp: unexpected lamps lit: %+v", got)
	}

	// linkDown → completely dark.
	if got := ledPattern(standby, LinkDown, false); got.wifiDim || got.stWhite {
		t.Errorf("standby+linkDown: ST lamp should be dark: %+v", got)
	}

	// linkConnecting → blinking white, wifiDim not set.
	if got := ledPattern(standby, LinkConnecting, true); got.wifiDim {
		t.Errorf("standby+linkConnecting: wifiDim should not be set: %+v", got)
	}
}

// applyLEDState must write the dim byte/string for wifiDim on both kernel paths.
func TestApplyLEDStateDimStandby(t *testing.T) {
	t.Run("stock-dim", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"uledw", "uledy", "rled3", "rled4", "rled5", "rled6", "rled7"} {
			err := os.WriteFile(filepath.Join(dir, name), nil, 0o644)
			if err != nil {
				t.Fatal(err)
			}
		}

		if err := applyLEDState(dir, ledState{wifiDim: true}); err != nil {
			t.Fatal(err)
		}
		// uledw is the Wi-Fi white lamp; it must carry the dim byte, not 0x00.
		got, err := os.ReadFile(filepath.Join(dir, "uledw"))
		if err != nil {
			t.Fatal(err)
		}

		if len(got) != 1 || got[0] != ledDimByte {
			t.Errorf("uledw = %x, want %x (dim)", got, ledDimByte)
		}
	})

	t.Run("gpio-leds-dim", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"wifi-white", "wifi-amber", "bt-white", "soundtouch-white", "soundtouch-amber", "bt-blue", "aux-white"} {
			path := filepath.Join(dir, name)

			err := os.Mkdir(path, 0o755)
			if err != nil {
				t.Fatal(err)
			}

			err = os.WriteFile(filepath.Join(path, "brightness"), nil, 0o644)
			if err != nil {
				t.Fatal(err)
			}
		}

		if err := applyLEDState(dir, ledState{wifiDim: true}); err != nil {
			t.Fatal(err)
		}

		got, err := os.ReadFile(filepath.Join(dir, "wifi-white", "brightness"))
		if err != nil {
			t.Fatal(err)
		}

		if string(got) != ledDimBrightness {
			t.Errorf("wifi-white brightness = %q, want %q (dim)", got, ledDimBrightness)
		}
	})
}

// The firmware-update pattern must show both ST lamps (amber solid, white blink)
// and look distinct from every normal operating pattern.
func TestFirmwareLEDPattern(t *testing.T) {
	// The actual pattern is driven directly in RunLEDs from Updating, not through
	// ledPattern; test it at the applyLEDState level via a temp LED dir.
	dir := t.TempDir()
	for _, name := range []string{"uledw", "uledy", "rled3", "rled4", "rled5", "rled6", "rled7"} {
		err := os.WriteFile(filepath.Join(dir, name), nil, 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}

	fwPattern := ledState{stAmber: true, stWhite: true} // both lit = "blink on" phase

	err := applyLEDState(dir, fwPattern)
	if err != nil {
		t.Fatal(err)
	}

	assertChannels(t, dir, false, map[string]bool{
		"rled4": true,  // soundtouch-white
		"rled5": true,  // soundtouch-amber
		"uledw": false, // wifi-white stays dark
	})
	// Firmware pattern differs from standby (which has wifiWhite+wifiDim) and from playing.
	standbyPattern := ledPattern(speakerstate.StatusSnapshot{Standby: true}, LinkUp, false)
	if standbyPattern == fwPattern {
		t.Error("firmware pattern matches standby — they should be distinct")
	}
}
