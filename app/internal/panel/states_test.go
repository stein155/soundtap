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
	"github.com/stein155/soundtap/app/internal/weather"
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
	np := func(source, station, track, artist, status string) npInfo {
		return npInfo{np: &speaker.NowPlaying{Source: source, Station: station, Track: track, Artist: artist, PlayStatus: status}, room: "Keuken"}
	}
	playing := func(i npInfo) []byte {
		return playingScreen(i, screen.Bar{Time: "08:42"}, 61000, 214000).Pix(15)
	}
	bootMid, _ := bootOverlay(true, true, 1300*time.Millisecond, -1, false, false, hw.LinkUp, "08:42")
	offline, _ := bootOverlay(false, false, 0, -1, false, false, hw.LinkDown, "08:42")
	connecting, connectingKey := bootOverlay(false, false, 0, -1, false, false, hw.LinkConnecting, "08:42")
	if _, offlineKey := bootOverlay(false, false, 0, -1, false, false, hw.LinkDown, "08:42"); offlineKey == connectingKey {
		t.Error("connecting and offline share a screen")
	}
	update, _ := bootOverlay(false, false, 0, 40, false, false, hw.LinkDown, "08:42")
	standbyDraw, _ := standbyFrame(time.Date(2026, 9, 25, 8, 42, 0, 0, time.UTC), false, nil)
	standbyTempDraw, _ := standbyFrame(time.Date(2026, 9, 25, 8, 42, 0, 0, time.UTC), false, &weather.Current{TempC: 14})
	standby12Draw, _ := standbyFrame(time.Date(2026, 9, 25, 20, 42, 0, 0, time.UTC), true, &weather.Current{TempC: -3})
	standby, standbyTemp, standby12 := standbyDraw(), standbyTempDraw(), standby12Draw()
	pairing := np("BLUETOOTH", "", "", "", "STOP_STATE")
	pairing.pairing = true
	if !bytes.Equal(playing(pairing), screen.Pairing(Text("Pairing…"), Text("Look for %s", "Keuken")).Pix(15)) {
		t.Error("Bluetooth with the pairing window open and no phone: want the pairing screen")
	}
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
		{"radio", playing(np("TUNEIN", "NPO Radio 2", "", "", "PLAY_STATE"))},
		{"radio-buffering", playing(np("TUNEIN", "NPO Radio 2", "", "", "BUFFERING_STATE"))},
		{"radio-long-name", playing(np("TUNEIN", "A Very Long Radio Station Name That Keeps Going", "Song", "Band", "PLAY_STATE"))},
		{"spotify", playing(np("SPOTIFY", "", "Nightcall", "Kavinsky", "PLAY_STATE"))},
		{"airplay", playing(np("AIRPLAY", "", "Nightcall", "Kavinsky", "PLAY_STATE"))},
		{"upnp", playing(np("UPNP", "", "Nightcall", "Kavinsky", "PLAY_STATE"))},
		{"following", playing(np("MULTIROOM", "", "Nightcall", "Kavinsky", "PLAY_STATE"))},
		{"paused", playing(np("SPOTIFY", "", "Nightcall", "Kavinsky", "PAUSE_STATE"))},
		{"stopped", playing(np("TUNEIN", "NPO Radio 2", "", "", "STOP_STATE"))},
		{"pair-left", playing(npInfo{np: &speaker.NowPlaying{Source: "SPOTIFY", Track: "Nightcall", Artist: "Kavinsky", PlayStatus: "PLAY_STATE"}, room: "Keuken", channel: "left"})},
		{"aux", playing(np("AUX", "", "", "", "STOP_STATE"))},
		{"bluetooth", playing(np("BLUETOOTH", "iPhone van Sanne", "", "", "PLAY_STATE"))},
		{"bluetooth-waiting", playing(np("BLUETOOTH", "", "", "", "STOP_STATE"))},
		{"bluetooth-reconnecting", playing(npInfo{np: &speaker.NowPlaying{Source: "BLUETOOTH", PlayStatus: "STOP_STATE"}, room: "Keuken", calling: true})},
		{"greek", playing(np("TUNEIN", "Ελληνικά", "Радио", "", "PLAY_STATE"))},
		{"volume", screen.Volume(35).Pix(15)},
		{"mute", screen.Mute(Text("Muted")).Pix(15)},
		{"preset", screen.Preset(Text("PRESET"), 2, "NPO Radio 2").Pix(15)},
		{"holding", screen.Holding(Text("Save as preset"), 2, 0.3).Pix(15)},
		{"saved", screen.Saved(Text("Saved as preset")).Pix(15)},
		{"pairing", playing(pairing)},
		{"standby", standby},
		{"standby-temp", standbyTemp},
		{"standby-12h", standby12},
	}

	dir := os.Getenv("SOUNDTAP_OLED_PNG")
	for _, s := range states {
		t.Run(s.name, func(t *testing.T) {
			if len(s.frame) != oled.Width*oled.Height {
				t.Fatalf("frame is %d bytes, want %d", len(s.frame), oled.Width*oled.Height)
			}
			if litPixels(s.frame) < 40 {
				t.Errorf("only %d pixels lit — this screen is effectively blank", litPixels(s.frame))
			}
			// The bottom two rows are the panel's edge; a glyph or a bar reaching them
			// is what a clipped layout looks like on hardware.
			for y := oled.Height - 2; y < oled.Height; y++ {
				for x := range oled.Width {
					if s.frame[y*oled.Width+x] > 0 {
						t.Errorf("lit pixel at (%d,%d): content runs off the bottom", x, y)
						return
					}
				}
			}
			if dir != "" {
				writePNG(t, filepath.Join(dir, s.name+".png"), s.frame)
			}
		})
	}
}

// Every language's words fit where the design puts them: the pixel-font labels and the
// one-line titles must not be cut.
func TestST20WordsFit(t *testing.T) {
	for lang, l := range st20Words {
		for en, want := range map[string]float64{"PRESET": 124, "LIVE": 90, "BUFFERING": 90} {
			if w := screen.Width("pix-8", l.words[en]); w > want {
				t.Errorf("%s %q is %.0f px wide, more than %.0f", lang, l.words[en], w, want)
			}
		}
		for _, en := range []string{"Muted", "Pairing…", "Starting…", "Connecting…"} {
			if w := screen.Width("j800-13", l.words[en]); w > 124 {
				t.Errorf("%s %q is %.0f px wide", lang, l.words[en], w)
			}
		}
		all := strings.Join(l.days[:], " ") + strings.Join(l.dshort[:], " ") + strings.Join(l.months[:], " ")
		for _, w := range l.words {
			all += w
		}
		for _, face := range []string{"j600-9", "j800-13", "pix-8"} {
			if r, ok := missingGlyph(face, all); !ok {
				t.Errorf("%s: %s has no glyph for %q", lang, face, r)
			}
		}
		if len(l.words) != 51 {
			t.Errorf("%s has %d words, want all 51", lang, len(l.words))
		}
		// The notice sublines run along one line; the messages wrap to three.
		for _, en := range []string{"Do not unplug", "Network card", "No sound", "Join Wi-Fi", "Then open"} {
			if w := screen.Width("j500-7.5", l.words[en]); w > 124 {
				t.Errorf("%s %q is %.0f px wide, more than the panel's lines", lang, l.words[en], w)
			}
		}
		for _, en := range []string{"Updating", "Update failed", "Pairing list cleared", "Can't connect", "Switching to Ethernet", "Switching to Wi-Fi", "Amp fault", "Setup", "Hold to reset", "Hold for setup", "Preset store failed",
			"Nothing to store", "Preset empty", "Playback error", "Hold a button", "Hold a button to reset", "Hold a button to restore",
			"Hold a button to restart", "Hold a button for Wi-Fi", "Hold a button for setup", "Hold a button to unpair",
			"Hold a button to install", "Hold a button for keys", "Hold a button for logs", "Confirmed", "Not confirmed", "Resetting",
			"Station unavailable", "Action unavailable"} {
			for _, line := range screen.Wrap("j800-11", l.words[en], 120, 3) {
				if strings.HasSuffix(line, "…") {
					t.Errorf("%s %q does not fit in three lines", lang, l.words[en])
				}
			}
		}
	}
}

func TestPlayingScreenState(t *testing.T) {
	np := &speaker.NowPlaying{Source: sourceTuneIn, Location: "/v1/playback/station/s1"}
	presets := []speaker.Preset{{Slot: 4, Location: "/v1/playback/station/s1", Name: "NPO Radio 2"}}
	if got := presetOf(presets, np); got != 4 {
		t.Errorf("preset of a preset's station = %d, want 4", got)
	}
	if got := presetOf(presets, &speaker.NowPlaying{Location: "/v1/playback/station/s2"}); got != 0 {
		t.Errorf("preset of another station = %d, want 0", got)
	}
	for _, status := range []string{"PLAY_STATE", "PAUSE_STATE", "STOP_STATE"} {
		np.PlayStatus = status
		if got := stateLine(np, "line"); got != "line" {
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
	playingNP := npInfo{np: &speaker.NowPlaying{Source: "SPOTIFY", Track: "T", Artist: "A", PlayStatus: "PLAY_STATE"}}
	pausedNP := npInfo{np: &speaker.NowPlaying{Source: "SPOTIFY", Track: "T", Artist: "A", PlayStatus: "PAUSE_STATE"}}
	if bytes.Equal(playingScreen(playingNP, screen.Bar{}, 0, 0).Pix(15), playingScreen(pausedNP, screen.Bar{}, 0, 0).Pix(15)) {
		t.Error("paused looks the same as playing")
	}
	np.Failed = true
	if got := stateLine(np, "line"); got != "Station unavailable" {
		t.Errorf("a station that never played = %q", got)
	}
}

func TestLinkSwitch(t *testing.T) {
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
	bt := func(device string) *speaker.NowPlaying {
		return &speaker.NowPlaying{Source: sourceBluetooth, Station: device}
	}
	radio := &speaker.NowPlaying{Source: sourceTuneIn, Station: "NPO Radio 2"}
	for _, c := range []struct {
		name      string
		prev, cur *speaker.NowPlaying
		want      string
	}{
		{"connect", bt(""), bt("iPhone"), "Connected to iPhone"},
		{"switch to bluetooth with a phone on", radio, bt("iPhone"), "Connected to iPhone"},
		{"other phone", bt("iPhone"), bt("Pixel"), "Connected to Pixel"},
		{"disconnect", bt("iPhone"), bt(""), "Disconnected from iPhone"},
		{"leave bluetooth", bt("iPhone"), radio, ""},
		{"standby", bt("iPhone"), nil, ""},
		{"no change", bt("iPhone"), bt("iPhone"), ""},
	} {
		if got := bluetoothChange(c.prev, c.cur); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBootOverlayCoverage(t *testing.T) {
	defer func(f func() (time.Duration, bool)) { uptime = f }(uptime)
	uptime = func() (time.Duration, bool) { return 0, true }
	for _, tc := range []struct {
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
		t.Run(tc.name, func(t *testing.T) {
			frame, key := bootOverlay(tc.booting, tc.booting, 0, tc.update, false, false, tc.link, "08:42")
			if key != tc.want || (frame == nil) != (tc.want == "") {
				t.Fatalf("key = %q, frame nil = %t; want key %q", key, frame == nil, tc.want)
			}
		})
	}
}

// An amp fault shows over the network screens, not over an update, and goes when it clears.
func TestAmpFaultShowsOnThePanel(t *testing.T) {
	faulted := true
	if _, key := bootOverlay(false, false, 0, -1, false, faulted, hw.LinkDown, ""); key != "ampfault" {
		t.Errorf("fault while down: %q", key)
	}
	if _, key := bootOverlay(false, false, 0, -1, false, faulted, hw.LinkUp, ""); key != "ampfault" {
		t.Errorf("fault while up: %q", key)
	}
	if _, key := bootOverlay(false, false, 0, 40, false, faulted, hw.LinkUp, ""); key != "update40" {
		t.Errorf("fault during an update: %q", key)
	}
	faulted = false
	if frame, key := bootOverlay(false, false, 0, -1, false, faulted, hw.LinkUp, ""); frame != nil || key != "" {
		t.Errorf("after the fault: %q", key)
	}
}

// The boot bar follows the uptime, holds short of full until the speaker serves, and
// goes full once it does.
func TestBootBarFills(t *testing.T) {
	const row = 49
	var now time.Duration
	defer func(f func() (time.Duration, bool)) { uptime = f }(uptime)
	uptime = func() (time.Duration, bool) { return now, true }
	lit := func(up time.Duration, waiting bool) int {
		now = up
		frame, _ := bootOverlay(true, waiting, 0, -1, false, false, hw.LinkUp, "")
		n := 0
		for x := range oled.Width {
			if frame[row*oled.Width+x] == 255 {
				n++
			}
		}
		return n
	}
	start, mid, held, late, served := lit(0, true), lit(typicalBoot/2, true), lit(typicalBoot, true), lit(10*typicalBoot, true), lit(typicalBoot, false)
	if start >= mid || mid >= held || held != late || late >= served {
		t.Fatalf("lit bar pixels at 0, half, full boot, long after, served: %d, %d, %d, %d, %d", start, mid, held, late, served)
	}
}

// The setup screen is the only instruction an owner gets when the speaker has no
// Wi-Fi, so the network's name has to fit on it as the Wi-Fi manager spells it.
func TestSetupNetworkNameFits(t *testing.T) {
	if w := screen.Width("j500-8", "SoundTap ST20 A1B2C3"); w > 124 {
		t.Errorf("the default name is %.0f wide", w)
	}
}

func litPixels(frame []byte) int {
	n := 0
	for _, p := range frame {
		if p > 0 {
			n++
		}
	}
	return n
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
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
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
	if panelTickFor(false) != 100*time.Millisecond || panelTickFor(true) != time.Second {
		t.Fatalf("panel tick = %v awake, %v in standby", panelTickFor(false), panelTickFor(true))
	}
	p := New(time.Second)
	select {
	case <-p.wake:
	default:
	}
	p.PresetDown(1)
	p.PresetUp()
	select {
	case <-p.wake:
	default:
		t.Fatal("preset press did not nudge the panel loop")
	}
}

// While the ST20's BCO app is written the network is down on purpose: the panel says so
// instead of "Connecting…", an install's own screen still outranks it, and it goes when
// the write ends.
func TestNetworkCardUpdateShowsOnThePanel(t *testing.T) {
	if _, key := bootOverlay(false, false, 0, -1, true, false, hw.LinkConnecting, ""); key != "netcard" {
		t.Errorf("BCO flash while connecting: %q", key)
	}
	if _, key := bootOverlay(false, false, 0, 40, true, false, hw.LinkDown, ""); key != "update40" {
		t.Errorf("BCO flash during an install: %q", key)
	}
	if _, key := bootOverlay(false, false, 0, -1, false, false, hw.LinkConnecting, ""); key == "netcard" {
		t.Errorf("no BCO flash: %q", key)
	}
}
