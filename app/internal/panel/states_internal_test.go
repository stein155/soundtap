// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package panel

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/oled"
	"github.com/stein155/soundtap/app/oled/screen"
)

// Every screen the ST20 panel can show, rendered and checked: something is lit, nothing
// runs off the bottom, and the frame is exactly panel-sized. app/oled/screen holds the
// screens themselves to the design pixel for pixel; this covers which one each state
// gets and that real text fits.
//
// Set SOUNDTAP_OLED_PNG=<dir> to also write each frame as a 4x PNG and look at it.
func TestPanelStates(t *testing.T) {
	t.Parallel()

	playingInfo := func(source, station, track, artist, status string) npInfo {
		return infoOf(playingNow(source, station, track, artist, status), "Keuken")
	}
	playing := func(info npInfo) []byte {
		return playingScreen(&info, barAt("08:42"), 61000, 214000).Pix(fullLevel)
	}

	mid := bootAt(hw.LinkUp, "08:42")
	mid.booting, mid.waiting, mid.since = true, true, 1300*time.Millisecond
	mid.uptime = func() (time.Duration, bool) { return 0, false }
	bootMid, _ := bootOverlay(mid)
	offline, offlineKey := bootOverlay(bootAt(hw.LinkDown, "08:42"))

	connecting, connectingKey := bootOverlay(bootAt(hw.LinkConnecting, "08:42"))
	if offlineKey == connectingKey {
		t.Error("connecting and offline share a screen")
	}

	installing := bootAt(hw.LinkDown, "08:42")
	installing.update = 40
	update, _ := bootOverlay(installing)
	standbyDraw, _ := standbyFrame(time.Date(2026, 9, 25, 8, 42, 0, 0, time.UTC), false)
	standby12Draw, _ := standbyFrame(time.Date(2026, 9, 25, 20, 42, 0, 0, time.UTC), true)
	standby, standby12 := standbyDraw(), standby12Draw()
	pairing := playingInfo("BLUETOOTH", "", "", "", "STOP_STATE")

	pairing.pairing = true
	if !bytes.Equal(playing(pairing), screen.Pairing(Text("Pairing…"), Text("Look for %s", "Keuken")).Pix(fullLevel)) {
		t.Error("Bluetooth with the pairing window open and no phone: want the pairing screen")
	}

	pairLeft := playingInfo("SPOTIFY", "", "Nightcall", "Kavinsky", "PLAY_STATE")
	pairLeft.channel = "left"
	reconnecting := playingInfo("BLUETOOTH", "", "", "", "STOP_STATE")
	reconnecting.calling = true

	states := []struct {
		name  string
		frame []byte
	}{
		{"boot-start", bootFrame(0)},
		{"boot-mid", bootMid},
		{"setup-ap", setupFrame()},
		{"offline", offline},
		{"connecting", connecting},
		{"updating", update},
		{"preset-empty", Message("Preset empty")},
		{"confirm", Message(Text("Hold a button to reset"))},
		{"radio", playing(playingInfo("TUNEIN", "NPO Radio 2", "", "", "PLAY_STATE"))},
		{"radio-buffering", playing(playingInfo("TUNEIN", "NPO Radio 2", "", "", "BUFFERING_STATE"))},
		{"radio-long-name", playing(playingInfo(
			"TUNEIN", "A Very Long Radio Station Name That Keeps Going", "Song", "Band", "PLAY_STATE",
		))},
		{"spotify", playing(playingInfo("SPOTIFY", "", "Nightcall", "Kavinsky", "PLAY_STATE"))},
		{"airplay", playing(playingInfo("AIRPLAY", "", "Nightcall", "Kavinsky", "PLAY_STATE"))},
		{"upnp", playing(playingInfo("UPNP", "", "Nightcall", "Kavinsky", "PLAY_STATE"))},
		{"following", playing(playingInfo("MULTIROOM", "", "Nightcall", "Kavinsky", "PLAY_STATE"))},
		{"paused", playing(playingInfo("SPOTIFY", "", "Nightcall", "Kavinsky", "PAUSE_STATE"))},
		{"stopped", playing(playingInfo("TUNEIN", "NPO Radio 2", "", "", "STOP_STATE"))},
		{"pair-left", playing(pairLeft)},
		{"aux", playing(playingInfo("AUX", "", "", "", "STOP_STATE"))},
		{"bluetooth", playing(playingInfo("BLUETOOTH", "iPhone van Sanne", "", "", "PLAY_STATE"))},
		{"bluetooth-waiting", playing(playingInfo("BLUETOOTH", "", "", "", "STOP_STATE"))},
		{"bluetooth-reconnecting", playing(reconnecting)},
		{"greek", playing(playingInfo("TUNEIN", "Ελληνικά", "Радио", "", "PLAY_STATE"))},
		{"volume", screen.Volume(35).Pix(fullLevel)},
		{"mute", screen.Mute(Text("Muted")).Pix(fullLevel)},
		{"preset", screen.Preset(Text("PRESET"), 2, "NPO Radio 2").Pix(fullLevel)},
		{"holding", screen.Holding(Text("Save as preset"), 2, 0.3).Pix(fullLevel)},
		{"saved", screen.Saved(Text("Saved as preset")).Pix(fullLevel)},
		{"pairing", playing(pairing)},
		{"standby", standby},
		{"standby-12h", standby12},
	}

	dir := os.Getenv("SOUNDTAP_OLED_PNG")

	for _, state := range states {
		t.Run(state.name, func(t *testing.T) {
			t.Parallel()

			if len(state.frame) != oled.Width*oled.Height {
				t.Fatalf("frame is %d bytes, want %d", len(state.frame), oled.Width*oled.Height)
			}

			if litPixels(state.frame) < 40 {
				t.Errorf("only %d pixels lit — this screen is effectively blank", litPixels(state.frame))
			}
			// The bottom two rows are the panel's edge; a glyph or a bar reaching them
			// is what a clipped layout looks like on hardware.
			for y := oled.Height - 2; y < oled.Height; y++ {
				for x := range oled.Width {
					if state.frame[y*oled.Width+x] > 0 {
						t.Errorf("lit pixel at (%d,%d): content runs off the bottom", x, y)

						return
					}
				}
			}

			if dir != "" {
				writePNG(t, filepath.Join(dir, state.name+".png"), state.frame)
			}
		})
	}
}

// Every language's words fit where the design puts them: the pixel-font labels and the
// one-line titles must not be cut.
func TestST20WordsFit(t *testing.T) {
	t.Parallel()

	for lang, table := range st20Words {
		for en, want := range map[string]float64{"PRESET": 124, "LIVE": 90, "BUFFERING": 90} {
			if w := screen.Width("pix-8", table.words[en]); w > want {
				t.Errorf("%s %q is %.0f px wide, more than %.0f", lang, table.words[en], w, want)
			}
		}

		for _, en := range []string{"Muted", "Pairing…", "Starting…", "Connecting…"} {
			if w := screen.Width("j800-13", table.words[en]); w > 124 {
				t.Errorf("%s %q is %.0f px wide", lang, table.words[en], w)
			}
		}

		all := ""

		var allSb129 strings.Builder
		for _, w := range table.words {
			allSb129.WriteString(w)
		}

		all += allSb129.String()

		for _, face := range []string{"j600-9", "j800-13", "pix-8"} {
			if r, ok := missingGlyph(face, all); !ok {
				t.Errorf("%s: %s has no glyph for %q", lang, face, r)
			}
		}

		if len(table.words) != 50 {
			t.Errorf("%s has %d words, want all 50", lang, len(table.words))
		}
		// The notice sublines run along one line; the messages wrap to three.
		for _, en := range []string{"Do not unplug", "Network card", "No sound", "Join Wi-Fi", "Then open"} {
			if w := screen.Width("j500-7.5", table.words[en]); w > 124 {
				t.Errorf("%s %q is %.0f px wide, more than the panel's lines", lang, table.words[en], w)
			}
		}

		for _, english := range []string{
			"Updating", "Update failed", "Pairing list cleared", "Can't connect", "Switching to Ethernet",
			"Switching to Wi-Fi", "Amp fault", "Setup", "Hold to reset", "Hold for setup", "Preset store failed",
			"Nothing to store", "Preset empty", "Playback error", "Hold a button", "Hold a button to reset",
			"Hold a button to restore", "Hold a button to restart", "Hold a button for Wi-Fi",
			"Hold a button for setup", "Hold a button to unpair", "Hold a button to install", "Hold a button for logs",
			"Confirmed", "Not confirmed", "Resetting", "Station unavailable", "Action unavailable",
		} {
			for _, line := range screen.Wrap("j800-11", table.words[english], 120, 3) {
				if strings.HasSuffix(line, "…") {
					t.Errorf("%s %q does not fit in three lines", lang, table.words[english])
				}
			}
		}
	}
}

func TestPlayingScreenState(t *testing.T) {
	t.Parallel()

	station := playingNow(sourceTuneIn, "", "", "", "")
	station.Location = "/v1/playback/station/s1"

	presets := []speaker.Preset{{
		Slot: 4, Name: "NPO Radio 2", StationID: "", Source: "", Type: "",
		Location: "/v1/playback/station/s1", Logo: "",
	}}
	if got := presetOf(presets, station); got != 4 {
		t.Errorf("preset of a preset's station = %d, want 4", got)
	}

	other := playingNow("", "", "", "", "")
	other.Location = "/v1/playback/station/s2"

	if got := presetOf(presets, other); got != 0 {
		t.Errorf("preset of another station = %d, want 0", got)
	}

	for _, status := range []string{"PLAY_STATE", "PAUSE_STATE", "STOP_STATE"} {
		station.PlayStatus = status
		if got := stateLine(station, "line"); got != "line" {
			t.Errorf("%s = %q, want the metadata", status, got)
		}
	}
	// The second line steps through the fields there are, skipping empty ones and repeats.
	for n, want := range []string{"Song", "Band", "Album", "Song"} {
		if got := cycled(n, "Song", "", "Band", "Song", "Album"); got != want {
			t.Errorf("cycle step %d = %q, want %q", n, got, want)
		}
	}

	if cycled(3) != "" {
		t.Error("no fields: want an empty line")
	}
	// Paused shows an icon where the source was, so the frame differs from playing.
	playingNP := infoOf(playingNow("SPOTIFY", "", "T", "A", "PLAY_STATE"), "")

	pausedNP := infoOf(playingNow("SPOTIFY", "", "T", "A", "PAUSE_STATE"), "")
	if bytes.Equal(playingScreen(&playingNP, barAt(""), 0, 0).Pix(fullLevel),
		playingScreen(&pausedNP, barAt(""), 0, 0).Pix(fullLevel)) {
		t.Error("paused looks the same as playing")
	}

	station.Failed = true
	if got := stateLine(station, "line"); got != "Station unavailable" {
		t.Errorf("a station that never played = %q", got)
	}
}

func TestLinkSwitch(t *testing.T) {
	t.Parallel()

	last := -1
	for i, c := range []struct {
		wired bool
		want  string
	}{{true, ""}, {true, ""}, {false, "Switching to Wi-Fi"}, {true, "Switching to Ethernet"}} {
		if got := linkSwitch(&last, c.wired); got != c.want {
			t.Errorf("step %d: %q, want %q", i, got, c.want)
		}
	}
}

func TestBluetoothChange(t *testing.T) {
	t.Parallel()

	onBluetooth := func(device string) *speaker.NowPlaying {
		return playingNow(sourceBluetooth, device, "", "", "")
	}

	radio := playingNow(sourceTuneIn, "NPO Radio 2", "", "", "")
	for _, test := range []struct {
		name      string
		prev, cur *speaker.NowPlaying
		want      string
	}{
		{"connect", onBluetooth(""), onBluetooth("iPhone"), "Connected to iPhone"},
		{"switch to bluetooth with a phone on", radio, onBluetooth("iPhone"), "Connected to iPhone"},
		{"other phone", onBluetooth("iPhone"), onBluetooth("Pixel"), "Connected to Pixel"},
		{"disconnect", onBluetooth("iPhone"), onBluetooth(""), "Disconnected from iPhone"},
		{"leave bluetooth", onBluetooth("iPhone"), radio, ""},
		{"standby", onBluetooth("iPhone"), nil, ""},
		{"no change", onBluetooth("iPhone"), onBluetooth("iPhone"), ""},
	} {
		if got := bluetoothChange(test.prev, test.cur); got != test.want {
			t.Errorf("%s: %q, want %q", test.name, got, test.want)
		}
	}
}

func TestBootOverlayCoverage(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		booting bool
		update  int
		link    hw.Link
		want    string
	}{
		{"startup outranks network", true, -1, hw.LinkUp, "boot0"},
		{"update outranks network", false, 40, hw.LinkDown, "update40"},
		{"setup", false, -1, hw.LinkSetupAP, "setup"},
		{"connecting", false, -1, hw.LinkConnecting, "offlineConnecting…08:420"},
		{"down", false, -1, hw.LinkDown, "offlineNo connection08:420"},
		{"ready", false, -1, hw.LinkUp, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			state := bootAt(test.link, "08:42")
			state.booting, state.waiting, state.update = test.booting, test.booting, test.update

			frame, key := bootOverlay(state)
			if key != test.want || (frame == nil) != (test.want == "") {
				t.Fatalf("key = %q, frame nil = %t; want key %q", key, frame == nil, test.want)
			}
		})
	}
}

// An amp fault shows over the network screens, not over an update, and goes when it clears.
func TestAmpFaultShowsOnThePanel(t *testing.T) {
	t.Parallel()

	state := func(link hw.Link, update int, faulted bool) *bootState {
		at := bootAt(link, "")
		at.update, at.faulted = update, faulted

		return at
	}

	if _, key := bootOverlay(state(hw.LinkDown, -1, true)); key != "ampfault" {
		t.Errorf("fault while down: %q", key)
	}

	if _, key := bootOverlay(state(hw.LinkUp, -1, true)); key != "ampfault" {
		t.Errorf("fault while up: %q", key)
	}

	if _, key := bootOverlay(state(hw.LinkUp, 40, true)); key != "update40" {
		t.Errorf("fault during an update: %q", key)
	}

	if frame, key := bootOverlay(state(hw.LinkUp, -1, false)); frame != nil || key != "" {
		t.Errorf("after the fault: %q", key)
	}
}

// The boot bar follows the uptime, holds short of full until the speaker serves, and
// goes full once it does.
func TestBootBarFills(t *testing.T) {
	t.Parallel()

	const row = 49

	lit := func(up time.Duration, waiting bool) int {
		state := bootAt(hw.LinkUp, "")
		state.booting, state.waiting = true, waiting
		state.uptime = func() (time.Duration, bool) { return up, true }
		frame, _ := bootOverlay(state)
		count := 0

		for x := range oled.Width {
			if frame[row*oled.Width+x] == 255 {
				count++
			}
		}

		return count
	}

	start, mid, held := lit(0, true), lit(typicalBoot/2, true), lit(typicalBoot, true)
	late, served := lit(10*typicalBoot, true), lit(typicalBoot, false)

	if start >= mid || mid >= held || held != late || late >= served {
		t.Fatalf("lit bar pixels at 0, half, full boot, long after, served: %d, %d, %d, %d, %d",
			start, mid, held, late, served)
	}
}

// The setup screen is the only instruction an owner gets when the speaker has no
// Wi-Fi, so the network's name has to fit on it as the Wi-Fi manager spells it.
func TestSetupNetworkNameFits(t *testing.T) {
	t.Parallel()

	if w := screen.Width("j500-8", "SoundTap ST20 A1B2C3"); w > 124 {
		t.Errorf("the default name is %.0f wide", w)
	}
}

func litPixels(frame []byte) int {
	count := 0

	for _, p := range frame {
		if p > 0 {
			count++
		}
	}

	return count
}

// playingNow is what plays, every other field empty.
func playingNow(source, station, track, artist, status string) *speaker.NowPlaying {
	return &speaker.NowPlaying{
		Source: source, Track: track, Artist: artist, Album: "", Station: station, StationID: "", Location: "",
		Art: "", PlayStatus: status, CanSkip: false, Failed: false, Unheard: false,
	}
}

// infoOf is the playing screen's input for nowPlaying in room, nothing else going on.
func infoOf(nowPlaying *speaker.NowPlaying, room string) npInfo {
	return npInfo{
		np: nowPlaying, presets: nil, room: room, pairing: false, calling: false, cycle: 0, channel: "",
	}
}

// barAt is a status bar with the time alone.
func barAt(clock string) *screen.Bar {
	return &screen.Bar{Source: "", Room: "", Time: clock, State: "", Mark: "", Battery: 0, Charging: false}
}

// bootAt is the boot screen's state past booting with nothing going on but the link, the
// uptime at zero.
func bootAt(link hw.Link, clock string) *bootState {
	return &bootState{
		booting: false, waiting: false, since: 0, update: -1, netcard: false, faulted: false,
		link: link, clock: clock, uptime: func() (time.Duration, bool) { return 0, true },
	}
}

// writePNG saves a frame at 4x so it can be looked at like the panel, not like a
// stamp.
func writePNG(t *testing.T, path string, frame []byte) {
	t.Helper()

	const scale = 4

	img := image.NewGray(image.Rect(0, 0, oled.Width*scale, oled.Height*scale))
	for y := range oled.Height * scale {
		for x := range oled.Width * scale {
			img.Pix[y*oled.Width*scale+x] = frame[(y/scale)*oled.Width+x/scale]
		}
	}

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	err = png.Encode(file, img)
	if err != nil {
		t.Fatal(err)
	}
}

// missingGlyph is the first letter of s the face has no glyph for.
func missingGlyph(face, s string) (rune, bool) {
	for _, r := range s {
		if !unicode.IsSpace(r) && !unicode.Is(unicode.Mn, r) && screen.Width(face, string(r)) == 0 {
			return r, false
		}
	}

	return 0, true
}

// In standby the panel slows down; out of it it runs as before, and a key that shows
// something on the panel wakes runDisplay out of its slow tick.
func TestPanelSlowsInStandbyAndWakesOnAPreset(t *testing.T) {
	t.Parallel()

	if panelTickFor(false) != 100*time.Millisecond || panelTickFor(true) != time.Second {
		t.Fatalf("panel tick = %v awake, %v in standby", panelTickFor(false), panelTickFor(true))
	}

	front := New(time.Second)
	select {
	case <-front.wake:
	default:
	}

	front.PresetDown(1)
	front.PresetUp()

	select {
	case <-front.wake:
	default:
		t.Fatal("preset press did not nudge the panel loop")
	}
}

// While the ST20's BCO app is written the network is down on purpose: the panel says so
// instead of "Connecting…", an install's own screen still outranks it, and it goes when
// the write ends.
func TestNetworkCardUpdateShowsOnThePanel(t *testing.T) {
	t.Parallel()

	state := func(link hw.Link, update int, netcard bool) *bootState {
		at := bootAt(link, "")
		at.update, at.netcard = update, netcard

		return at
	}

	if _, key := bootOverlay(state(hw.LinkConnecting, -1, true)); key != "netcard" {
		t.Errorf("BCO flash while connecting: %q", key)
	}

	if _, key := bootOverlay(state(hw.LinkDown, 40, true)); key != "update40" {
		t.Errorf("BCO flash during an install: %q", key)
	}

	if _, key := bootOverlay(state(hw.LinkConnecting, -1, false)); key == "netcard" {
		t.Errorf("no BCO flash: %q", key)
	}
}
