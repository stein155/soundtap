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

// lamp is one field of ledState, for litLamps.
type lamp uint8

const (
	lampWifiWhite lamp = 1 << iota
	lampWifiAmber
	lampBTWhite
	lampSTWhite
	lampSTAmber
	lampBTBlue
	lampAuxWhite
	lampWifiDim
)

// litLamps is the lamp state with exactly the lamps named on.
func litLamps(lamps lamp) ledState {
	return ledState{
		wifiWhite: lamps&lampWifiWhite != 0,
		wifiAmber: lamps&lampWifiAmber != 0,
		btWhite:   lamps&lampBTWhite != 0,
		stWhite:   lamps&lampSTWhite != 0,
		stAmber:   lamps&lampSTAmber != 0,
		btBlue:    lamps&lampBTBlue != 0,
		auxWhite:  lamps&lampAuxWhite != 0,
		wifiDim:   lamps&lampWifiDim != 0,
	}
}

// snapshotFlag is one of the StatusSnapshot switches the lamps read, for snapshot.
type snapshotFlag uint8

const (
	snapStandby snapshotFlag = 1 << iota
	snapNotification
	snapEmptyPreset
	snapTransportAck
)

// snapshot is a playback state with source, play status and the flags named; the rest
// is what the lamps do not read.
func snapshot(source, playStatus string, flags snapshotFlag) *speakerstate.StatusSnapshot {
	return &speakerstate.StatusSnapshot{
		Source:       source,
		PlayStatus:   playStatus,
		Standby:      flags&snapStandby != 0,
		Volume:       0,
		Muted:        false,
		Bass:         0,
		Notification: flags&snapNotification != 0,
		EmptyPreset:  flags&snapEmptyPreset != 0,
		TransportAck: flags&snapTransportAck != 0,
		IP:           "",
	}
}

// gpioLampNames are the mainline gpio-leds labels of the ST10's seven lamps.
func gpioLampNames() []string {
	return []string{
		"wifi-white", "wifi-amber", "bt-white", "soundtouch-white", "soundtouch-amber", "bt-blue", "aux-white",
	}
}

func TestLEDPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state *speakerstate.StatusSnapshot
		link  Link
		blink bool
		want  ledState
	}{
		{"standby", snapshot("", "", snapStandby), LinkUp, true, litLamps(lampWifiDim)},
		{"standby notification", snapshot("", "", snapStandby|snapNotification), LinkUp, true, litLamps(lampWifiDim)},
		{"tunein", snapshot("TUNEIN", "PLAY_STATE", 0), LinkUp, false, litLamps(lampWifiWhite | lampSTWhite)},
		{"airplay", snapshot("AIRPLAY", "PLAY_STATE", 0), LinkUp, false, litLamps(lampWifiWhite | lampSTWhite)},
		{"spotify", snapshot("SPOTIFY", "PLAY_STATE", 0), LinkUp, false, litLamps(lampWifiWhite | lampSTWhite)},
		{"aux", snapshot("AUX", "PLAY_STATE", 0), LinkUp, false, litLamps(lampWifiWhite | lampAuxWhite)},
		{
			"bluetooth playing", snapshot("BLUETOOTH", "PLAY_STATE", 0), LinkUp, false,
			litLamps(lampWifiWhite | lampBTWhite),
		},
		{"buffering on", snapshot("TUNEIN", "BUFFERING_STATE", 0), LinkUp, true, litLamps(lampWifiWhite | lampSTWhite)},
		{"buffering off", snapshot("TUNEIN", "BUFFERING_STATE", 0), LinkUp, false, litLamps(lampWifiWhite)},
		{"paused", snapshot("TUNEIN", "PAUSE_STATE", 0), LinkUp, false, litLamps(lampWifiWhite | lampSTAmber)},
		{"stopped", snapshot("TUNEIN", "STOP_STATE", 0), LinkUp, false, litLamps(lampWifiWhite | lampSTAmber)},
		{"upnp", snapshot("UPNP", "PLAY_STATE", 0), LinkUp, false, litLamps(lampWifiWhite | lampSTWhite)},
		{
			"multiroom follower", snapshot("MULTIROOM", "PLAY_STATE", 0), LinkUp, false,
			litLamps(lampWifiWhite | lampSTWhite),
		},
		{
			"notification on", snapshot("", "", snapNotification), LinkUp, true,
			litLamps(lampWifiWhite | lampSTAmber | lampSTWhite),
		},
		{"notification off", snapshot("", "", snapNotification), LinkUp, false, litLamps(lampWifiWhite)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := ledPattern(test.state, test.link, test.blink); got != test.want {
				t.Fatalf("got %+v, want %+v", got, test.want)
			}
		})
	}
}

// AUX is a line-in with no transport, so it must not be gated on a play status
// the source will never report.
func TestAUXLampIgnoresPlayStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"", "PLAY_STATE", "STOP_STATE", "PAUSE_STATE", "BUFFERING_STATE"} {
		st := snapshot("AUX", status, 0)
		if got := ledPattern(st, LinkUp, false); !got.auxWhite || got.stAmber {
			t.Fatalf("AUX with playStatus %q lit %+v", status, got)
		}
	}
}

// Selecting Bluetooth and waiting for a phone must look different from a dead
// source: blue blinks while discoverable, white is solid once audio arrives.
func TestBluetoothShowsPairingSeparatelyFromPlaying(t *testing.T) {
	t.Parallel()

	waiting := ledPattern(snapshot("BLUETOOTH", "STOP_STATE", 0), LinkUp, true)
	if !waiting.btBlue || waiting.btWhite {
		t.Fatalf("waiting for a device = %+v, want blue blinking", waiting)
	}

	if off := ledPattern(snapshot("BLUETOOTH", "STOP_STATE", 0), LinkUp, false); off.btBlue {
		t.Fatalf("blue did not blink: %+v", off)
	}

	playing := ledPattern(snapshot("BLUETOOTH", "PLAY_STATE", 0), LinkUp, true)
	if !playing.btWhite || playing.btBlue {
		t.Fatalf("playing = %+v, want solid white", playing)
	}
}

// The Wi-Fi lamp must distinguish the four network states, and a speaker waiting
// to be set up must keep signalling even in standby — that is exactly the
// speaker whose owner has nothing else to go on.
func TestWifiLampReportsTheLink(t *testing.T) {
	t.Parallel()

	idle := snapshot("TUNEIN", "STOP_STATE", 0)
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

	standby := snapshot("", "", snapStandby)
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
	t.Parallel()

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
	t.Parallel()

	setup := linkFromFacts(true, true, false)
	if setup != LinkSetupAP {
		t.Fatalf("setup AP after prior IP = %v, want linkSetupAP", setup)
	}

	if lamps := ledPattern(snapshot("", "", 0), setup, false); !lamps.wifiAmber || lamps.wifiWhite {
		t.Fatalf("setup AP lamps = %+v, want solid amber", lamps)
	}

	if got := linkFromFacts(true, false, true); got != LinkUp {
		t.Fatalf("associated station with IP = %v, want linkUp", got)
	}

	connecting := linkFromFacts(false, false, true)
	if connecting != LinkConnecting {
		t.Fatalf("station reconnect after prior IP = %v, want linkConnecting", connecting)
	}

	if lamps := ledPattern(snapshot("", "", 0), connecting, true); !lamps.wifiWhite || lamps.wifiAmber {
		t.Fatalf("station reconnect lamps = %+v, want white blink on", lamps)
	}

	if lamps := ledPattern(snapshot("", "", 0), connecting, false); lamps.wifiWhite || lamps.wifiAmber {
		t.Fatalf("station reconnect lamps = %+v, want white blink off", lamps)
	}
}

func TestApplyLEDState(t *testing.T) {
	t.Parallel()

	t.Run("stock", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		for _, name := range []string{"uledw", "uledy", "rled3", "rled4", "rled5", "rled6", "rled7"} {
			err := os.WriteFile(filepath.Join(dir, name), nil, 0o644)
			if err != nil {
				t.Fatal(err)
			}
		}

		err := applyLEDState(dir, litLamps(lampWifiWhite|lampAuxWhite|lampSTAmber|lampSTWhite))
		if err != nil {
			t.Fatal(err)
		}

		assertChannels(t, dir, false, map[string]bool{
			"uledw": true, "uledy": false, "rled3": false,
			"rled4": true, "rled5": true, "rled6": false, "rled7": true,
		})
	})

	t.Run("gpio-leds", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		for _, name := range gpioLampNames() {
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

		err := applyLEDState(dir, litLamps(lampWifiAmber|lampBTWhite|lampAuxWhite))
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
	t.Parallel()

	want := []struct{ mainline, stock string }{
		{"wifi-white", "uledw"},       // gpio0_19
		{"wifi-amber", "uledy"},       // gpio2_17
		{"bt-white", "rled3"},         // gpio0_4
		{"soundtouch-white", "rled4"}, // gpio0_12
		{"soundtouch-amber", "rled5"}, // gpio2_12
		{"bt-blue", "rled6"},          // gpio2_9
		{"aux-white", "rled7"},        // gpio2_23
	}

	got := ledChannels(litLamps(0))
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
	t.Parallel()

	all := litLamps(lampWifiWhite | lampWifiAmber | lampBTWhite | lampSTWhite | lampSTAmber | lampBTBlue | lampAuxWhite)
	for _, channel := range ledChannels(all) {
		if !channel.on {
			t.Errorf("%s stayed off with every field set", channel.mainline)
		}
	}

	for _, channel := range ledChannels(litLamps(0)) {
		if channel.on {
			t.Errorf("%s lit with no field set", channel.mainline)
		}
	}
}

//nolint:revive // flag-parameter: gpio says which kernel's lamp files dir holds
func assertChannels(t *testing.T, dir string, gpio bool, states map[string]bool) {
	t.Helper()

	for name, lampOn := range states {
		path := filepath.Join(dir, name)
		want := []byte{0x00}

		if gpio {
			path = filepath.Join(path, "brightness")
			want = []byte("0")
		}

		if lampOn {
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
	t.Parallel()

	standby := snapshot("", "", snapStandby)
	// linkDown → fully dark.
	for _, blink := range []bool{true, false} {
		if got := ledPattern(standby, LinkDown, blink); got != litLamps(0) {
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

	if got := ledPattern(standby, LinkConnecting, false); got != litLamps(0) {
		t.Fatalf("standby joining Wi-Fi blink phase = %+v, want dark", got)
	}
}

// The keypad reports raw matrix indexes, so a change here silently remaps the
// speaker's buttons.
func TestDefaultKeymapIsStable(t *testing.T) {
	t.Parallel()

	want := map[int]string{
		0: "VOLUME_UP", 1: "PRESET_3", 2: "POWER",
		4: "VOLUME_DOWN", 5: "PRESET_2", 6: "PRESET_1",
		8: "PRESET_6", 9: "PRESET_5", 10: "PRESET_4",
		12: "SOURCE_TOGGLE",
	}
	if len(defaultKeymap()) != len(want) {
		t.Fatalf("keymap has %d entries, want %d", len(defaultKeymap()), len(want))
	}

	for index, action := range want {
		if got := defaultKeymap()[index]; got != action {
			t.Errorf("index %d = %q, want %q", index, got, action)
		}
	}
}

// The ST20 has one lamp, the Wi-Fi lamp above the OLED. With an empty LEDDir
// runLEDs returned at once and that lamp never lit under SoundTap.
func TestST20DrivesItsWiFiLamp(t *testing.T) {
	t.Parallel()

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

		lampless := model.Named(model.WLA).LEDDir
		RunLEDs(context.Background(), lampless, testLamps{speakerstate.New()}, slog.New(slog.DiscardHandler))
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
	t.Parallel()

	want := []string{"wifi-white", "bt-white", "aux-white", "soundtouch-white", "aux-white", "bt-white"}
	for step, name := range want {
		state := bootChase(step)
		lit := []string{}

		for _, channel := range ledChannels(state) {
			if channel.on {
				lit = append(lit, channel.mainline)
			}
		}

		if len(lit) != 1 || lit[0] != name {
			t.Fatalf("step %d lights %v, want exactly [%s]", step, lit, name)
		}
	}

	for step := 1; step < len(want); step++ {
		if bootChase(step) == bootChase(step-1) {
			t.Fatalf("step %d repeats step %d", step, step-1)
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
	t.Parallel()

	emptyPreset := snapshot("", "", snapEmptyPreset)
	for _, testCase := range []struct {
		link  Link
		blink bool
	}{
		{LinkUp, true},
		{LinkUp, false},
		{LinkDown, true},
	} {
		got := ledPattern(emptyPreset, testCase.link, testCase.blink)
		if got.stAmber != testCase.blink {
			t.Errorf("EmptyPreset link=%v blink=%v: stAmber=%v, want %v",
				testCase.link, testCase.blink, got.stAmber, testCase.blink)
		}

		if got.stWhite || got.btBlue || got.btWhite || got.auxWhite {
			t.Errorf("EmptyPreset lit unexpected lamps: %+v", got)
		}
	}
	// EmptyPreset overrides standby dark state.
	standby := snapshot("", "", snapEmptyPreset|snapStandby)
	if got := ledPattern(standby, LinkUp, true); !got.stAmber {
		t.Errorf("EmptyPreset in standby: stAmber not lit: %+v", got)
	}
}

// In standby on a healthy network the Wi-Fi white lamp should be dim so the speaker
// is distinguishable from unplugged. Stock shows the same faint pilot lamp.
func TestDimStandbyLampOnHealthyNetwork(t *testing.T) {
	t.Parallel()

	standby := snapshot("", "", snapStandby)

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
	t.Parallel()

	t.Run("stock-dim", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		for _, name := range []string{"uledw", "uledy", "rled3", "rled4", "rled5", "rled6", "rled7"} {
			err := os.WriteFile(filepath.Join(dir, name), nil, 0o644)
			if err != nil {
				t.Fatal(err)
			}
		}

		err := applyLEDState(dir, litLamps(lampWifiDim))
		if err != nil {
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
		t.Parallel()

		dir := t.TempDir()
		for _, name := range gpioLampNames() {
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

		err := applyLEDState(dir, litLamps(lampWifiDim))
		if err != nil {
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
	t.Parallel()

	// The actual pattern is driven directly in RunLEDs from Updating, not through
	// ledPattern; test it at the applyLEDState level via a temp LED dir.
	dir := t.TempDir()
	for _, name := range []string{"uledw", "uledy", "rled3", "rled4", "rled5", "rled6", "rled7"} {
		err := os.WriteFile(filepath.Join(dir, name), nil, 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}

	fwPattern := litLamps(lampSTAmber | lampSTWhite) // both lit = "blink on" phase

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
	standbyPattern := ledPattern(snapshot("", "", snapStandby), LinkUp, false)
	if standbyPattern == fwPattern {
		t.Error("firmware pattern matches standby — they should be distinct")
	}
}
