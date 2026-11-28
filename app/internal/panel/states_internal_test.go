// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package panel

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
	playingPage := func(info npInfo, page int) []byte {
		moment := view{page: page, dimmed: false, orbit: 0, drift: 0, dotStep: 0, fade: 0}
		corner := noCorner()

		return playingScreen(&info, &corner, 61000, 214000, &moment).Pix(fullLevel)
	}
	playing := func(info npInfo) []byte { return playingPage(info, 0) }

	mid := bootAt(hw.LinkUp)
	mid.booting, mid.waiting, mid.since = true, true, 1300*time.Millisecond
	mid.uptime = func() (time.Duration, bool) { return 0, false }
	bootMid, _, _ := bootOverlay(mid)
	offline, _, offlineKey := bootOverlay(bootAt(hw.LinkDown))

	connecting, _, connectingKey := bootOverlay(bootAt(hw.LinkConnecting))
	if offlineKey == connectingKey {
		t.Error("connecting and offline share a screen")
	}

	joining := bootAt(hw.LinkConnecting)
	joining.network = "Thuis"

	named, _, namedKey := bootOverlay(joining)
	if namedKey == connectingKey || bytes.Equal(named, connecting) {
		t.Error("connecting does not name the network it joins")
	}

	installing := bootAt(hw.LinkDown)
	installing.update = 40
	update, _, _ := bootOverlay(installing)
	writing := bootAt(hw.LinkDown)
	writing.netcard = 90 * time.Second
	netcard, _, _ := bootOverlay(writing)
	marking := bootAt(hw.LinkUp)
	marking.mark = 3 * time.Second
	mark, _, _ := bootOverlay(marking)
	standbyDraw, _ := standbyFrame(time.Date(2026, 9, 25, 8, 42, 0, 0, time.UTC), false)
	standby12Draw, _ := standbyFrame(time.Date(2026, 9, 25, 20, 42, 0, 0, time.UTC), true)
	standby, standby12 := standbyDraw(), standby12Draw()
	pairing := playingInfo("BLUETOOTH", "", "", "", "STOP_STATE")

	pairing.pairing = true

	want := notice(screen.IconBluetooth, Text("Pairing…"), Text("Look for %s", "Keuken")).Pix(fullLevel)
	if !bytes.Equal(playing(pairing), want) {
		t.Error("Bluetooth with the pairing window open and no phone: want the pairing screen")
	}

	pairLeft := playingInfo("SPOTIFY", "", "Nightcall", "Kavinsky", "PLAY_STATE")
	pairLeft.channel = "left"
	reconnecting := playingInfo("BLUETOOTH", "", "", "", "STOP_STATE")
	reconnecting.calling = true
	icy := playingInfo("TUNEIN", "NPO Radio 2", "Radar Love", "Golden Earring", "PLAY_STATE")
	failed := playingInfo("TUNEIN", "Sky Radio", "", "", "PLAY_STATE")
	failed.np.Failed = true

	states := []struct {
		name  string
		frame []byte
	}{
		{"boot-start", bootFrame(0)},
		{"boot-mid", bootMid},
		{"boot-mark", mark},
		{"setup-ap", setupFrame(0)},
		{"setup-ap-open", setupFrame(1)},
		{"offline", offline},
		{"connecting", connecting},
		{"connecting-named", named},
		{"updating", update},
		{"netcard", netcard},
		{"preset-empty", Say("Preset empty")},
		{"confirm", Say("Reset?")},
		{"radio", playing(playingInfo("TUNEIN", "NPO Radio 2", "", "", "PLAY_STATE"))},
		{"radio-station-page", playing(icy)},
		{"radio-now-page", playingPage(icy, 1)},
		{"radio-buffering", playing(playingInfo("TUNEIN", "NPO Radio 2", "", "", "BUFFERING_STATE"))},
		{"radio-failed", playing(failed)},
		{"radio-long-name", playing(playingInfo(
			"TUNEIN", "A Very Long Radio Station Name That Keeps Going", "Song", "Band", "PLAY_STATE",
		))},
		{"spotify", playing(playingInfo("SPOTIFY", "", "Nightcall", "Kavinsky", "PLAY_STATE"))},
		{"airplay", playing(playingInfo("AIRPLAY", "", "Nightcall", "Kavinsky", "PLAY_STATE"))},
		{"upnp", playing(playingInfo("UPNP", "", "Nightcall", "Kavinsky", "PLAY_STATE"))},
		{"following", playing(playingInfo("MULTIROOM", "", "Nightcall", "Kavinsky", "PLAY_STATE"))},
		{"following-quiet", playing(playingInfo("MULTIROOM", "", "", "", "PLAY_STATE"))},
		{"paused", playing(playingInfo("SPOTIFY", "", "Nightcall", "Kavinsky", "PAUSE_STATE"))},
		{"stopped", playing(playingInfo("TUNEIN", "NPO Radio 2", "", "", "STOP_STATE"))},
		{"pair-left", playing(pairLeft)},
		{"aux", playing(playingInfo("AUX", "", "", "", "STOP_STATE"))},
		{"bluetooth", playing(playingInfo("BLUETOOTH", "iPhone van Sanne", "", "", "PLAY_STATE"))},
		{"bluetooth-waiting", playing(playingInfo("BLUETOOTH", "", "", "", "STOP_STATE"))},
		{"bluetooth-reconnecting", playing(reconnecting)},
		{"greek", playing(playingInfo("TUNEIN", "Ελληνικά", "", "", "PLAY_STATE"))},
		{"dimmed", screen.Ambient("NPO Radio 2", 4, 1).Pix(fullLevel)},
		{"volume", screen.Volume(35).Pix(fullLevel)},
		{"mute", notice(screen.IconMute, Text("Muted"), "").Pix(fullLevel)},
		{"preset", screen.Preset(Text("PRESET"), 2, "NPO Radio 2").Pix(fullLevel)},
		{"holding", screen.Holding(Text("Save as preset"), 2, 0.3).Pix(fullLevel)},
		{"chord-setup", screen.Countdown(7, 0.3, Text("Setup mode")).Pix(fullLevel)},
		{"chord-reset", screen.Countdown(10, 0, Text("Factory reset")).Pix(fullLevel)},
		{"setup-on", Say("Setup mode on")},
		{"saved", screen.Saved(Text("Saved")).Pix(fullLevel)},
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
			// The bottom row is the panel's edge; a glyph reaching it is what a clipped
			// layout looks like on hardware.
			for x := range oled.Width {
				if state.frame[(oled.Height-1)*oled.Width+x] > 0 {
					t.Errorf("lit pixel at (%d,%d): content runs off the bottom", x, oled.Height-1)

					return
				}
			}

			if dir != "" {
				writePNG(t, filepath.Join(dir, state.name+".png"), state.frame)
			}
		})
	}
}

// Every language's words fit where the design puts them: the pixel-font labels, the
// countdown's one line and the small lines must not be cut, the titles take at most two
// lines.
func TestST20WordsFit(t *testing.T) {
	t.Parallel()

	for lang, table := range st20Words {
		for en, want := range map[string]float64{"PRESET": 120, "LIVE": 90, "BUFFERING": 90, "CONFIRM": 120} {
			if w := screen.Width("pix-8", table.words[en]); w > want {
				t.Errorf("%s %q is %.0f px wide, more than %.0f", lang, table.words[en], w, want)
			}
		}

		if len(table.words) != len(st20Words["nl"].words) {
			t.Errorf("%s has %d words, want all %d", lang, len(table.words), len(st20Words["nl"].words))
		}
		// A held combination's count says what it does on one line, down to 11 px.
		for _, en := range []string{"Setup mode", "Factory reset"} {
			if w := screen.Width("m700-11", table.words[en]); w > 120 {
				t.Errorf("%s %q is %.0f px wide, more than a line", lang, table.words[en], w)
			}
		}
		// The small lines that run along one line.
		for _, english := range []string{"Do not unplug", "Then open", "Join Wi-Fi"} {
			word := table.words[english]
			if w := screen.Width("m700-11", "2 · "+word); english != "Do not unplug" && w > 120 {
				t.Errorf("%s %q is %.0f px wide, more than the setup step's line", lang, word, w)
			}

			if w := screen.Width("m600-11", word); w > 120 {
				t.Errorf("%s %q is %.0f px wide, more than a small line", lang, word, w)
			}
		}

		for _, english := range titleWords() {
			word := table.words[english]
			if strings.Contains(SayScreenTitle(word), "…") && !strings.HasSuffix(word, "…") {
				t.Errorf("%s %q does not fit a title's two lines", lang, word)
			}
		}
		// An install's title runs along one line, down to 12 px.
		if w := screen.Width("m700-12", table.words["Updating"]); w > 120 {
			t.Errorf("%s %q is %.0f px wide, more than the install's title line", lang, table.words["Updating"], w)
		}
	}
}

// titleWords are the texts the panel shows as a title: its messages, questions and the
// playing screens' words.
func titleWords() []string {
	words := make([]string, 0, len(sayings)+len(questions)+11)
	words = append(words,
		"Muted", "Connecting…", "No connection", "Pairing…", "Waiting…", "Ethernet", "Wi-Fi",
		"Nothing to store", "Preset empty", "Saved", "Save as preset",
	)

	for english := range sayings {
		words = append(words, english)
	}

	for english := range questions {
		words = append(words, english)
	}

	return words
}

// SayScreenTitle is word laid out as a notice's title, its lines joined: an ellipsis in it
// that word does not have says it was cut.
func SayScreenTitle(word string) string { return strings.Join(screen.TitleLines(word), " ") }

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
	// Paused shows an icon in the corner, so the frame differs from playing.
	moment := view{page: 0, dimmed: false, orbit: 0, drift: 0, dotStep: 0, fade: 0}
	corner := noCorner()
	playingNP := infoOf(playingNow("SPOTIFY", "", "T", "A", "PLAY_STATE"), "")

	pausedNP := infoOf(playingNow("SPOTIFY", "", "T", "A", "PAUSE_STATE"), "")
	if bytes.Equal(playingScreen(&playingNP, &corner, 0, 0, &moment).Pix(fullLevel),
		playingScreen(&pausedNP, &corner, 0, 0, &moment).Pix(fullLevel)) {
		t.Error("paused looks the same as playing")
	}
	// The radio alternates only while it plays live and names its song.
	icy := playingNow(sourceTuneIn, "NPO Radio 2", "Radar Love", "Golden Earring", "PLAY_STATE")
	if !alternates(icy) {
		t.Error("a live station naming its song does not alternate")
	}

	for _, status := range []string{"PAUSE_STATE", "STOP_STATE", "BUFFERING_STATE"} {
		quiet := *icy
		quiet.PlayStatus = status

		if alternates(&quiet) {
			t.Errorf("%s alternates", status)
		}
	}

	if alternates(playingNow(sourceTuneIn, "NPO Radio 2", "", "", "PLAY_STATE")) {
		t.Error("a station without a song alternates")
	}
}

// The radio's pages take turns every radioPage, fading the panel out over the last
// radioFade of one and in over the first radioFade of the next; the first page shows at
// once.
func TestRadioPagesFade(t *testing.T) {
	t.Parallel()

	for _, step := range []struct {
		since time.Duration
		page  int
		fade  float64
	}{
		{0, 0, 0},
		{4 * time.Second, 0, 0},
		{radioPage - radioFade/2, 0, 0.5},
		{radioPage, 1, 1},
		{radioPage + radioFade/2, 1, 0.5},
		{radioPage + radioFade, 1, 0},
		{2*radioPage + time.Second, 0, 0},
	} {
		page, fade := radioView(step.since)
		if page != step.page || fade < step.fade-0.01 || fade > step.fade+0.01 {
			t.Errorf("at %v: page %d, fade %.2f; want %d, %.2f", step.since, page, fade, step.page, step.fade)
		}
	}
}

// A while after the last key the playing screen keeps its title alone, dim; the memo draws
// it again only when the drift moves it.
func TestDimmedKeepsTheTitle(t *testing.T) {
	t.Parallel()

	info := infoOf(playingNow("SPOTIFY", "", "Sun Goes Down", "Robin Schulz", "PLAY_STATE"), "")
	corner := noCorner()

	var memo playingMemo

	moment := viewAt(time.Unix(600, 0), time.Minute, true)
	dimmed := memo.playing(&info, &corner, 1000, 214000, &moment)

	dx, dy := driftAt(moment.drift)
	if !bytes.Equal(dimmed, screen.Ambient("Sun Goes Down", dx, dy).Pix(fullLevel)) {
		t.Error("the dimmed screen is not the title alone")
	}

	for _, v := range dimmed {
		if v > 8*17 {
			t.Fatalf("a dimmed pixel at level %d, brighter than 8", v/17)
		}
	}

	later := viewAt(time.Unix(610, 0), time.Minute, true)
	if again := memo.playing(&info, &corner, 9000, 214000, &later); !bytes.Equal(again, dimmed) {
		t.Error("the dimmed title moved within a minute, or followed the track's time")
	}
}

func TestLinkSwitch(t *testing.T) {
	t.Parallel()

	last := -1
	for i, c := range []struct {
		wired bool
		want  string
	}{{true, ""}, {true, ""}, {false, linkWifi}, {true, linkEthernet}} {
		if got := linkSwitch(&last, c.wired); got != c.want {
			t.Errorf("step %d: %q, want %q", i, got, c.want)
		}
	}

	if bytes.Equal(linkScreen(linkWifi).Pix(fullLevel), linkScreen(linkEthernet).Pix(fullLevel)) {
		t.Error("Wi-Fi and Ethernet look the same")
	}
}

func TestBluetoothChange(t *testing.T) {
	t.Parallel()

	onBluetooth := func(device string) *speaker.NowPlaying {
		return playingNow(sourceBluetooth, device, "", "", "")
	}

	radio := playingNow(sourceTuneIn, "NPO Radio 2", "", "", "")
	connected := func(device string) deviceChange { return deviceChange{icon: screen.IconCheck, device: device} }
	none := deviceChange{icon: screen.IconNone, device: ""}

	for _, test := range []struct {
		name      string
		prev, cur *speaker.NowPlaying
		want      deviceChange
	}{
		{"connect", onBluetooth(""), onBluetooth("iPhone"), connected("iPhone")},
		{"switch to bluetooth with a phone on", radio, onBluetooth("iPhone"), connected("iPhone")},
		{"other phone", onBluetooth("iPhone"), onBluetooth("Pixel"), connected("Pixel")},
		{
			"disconnect", onBluetooth("iPhone"), onBluetooth(""),
			deviceChange{icon: screen.IconBluetoothOff, device: "iPhone"},
		},
		{"leave bluetooth", onBluetooth("iPhone"), radio, none},
		{"standby", onBluetooth("iPhone"), nil, none},
		{"no change", onBluetooth("iPhone"), onBluetooth("iPhone"), none},
	} {
		if got := bluetoothChange(test.prev, test.cur); got != test.want {
			t.Errorf("%s: %+v, want %+v", test.name, got, test.want)
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
		{"setup", false, -1, hw.LinkSetupAP, "setup0"},
		{"connecting", false, -1, hw.LinkConnecting, "offlineConnecting…1"},
		{"down", false, -1, hw.LinkDown, "offlineNo connection1"},
		{"ready", false, -1, hw.LinkUp, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			state := bootAt(test.link)
			state.booting, state.waiting, state.update = test.booting, test.booting, test.update

			frame, _, key := bootOverlay(state)
			if key != test.want || (frame == nil) != (test.want == "") {
				t.Fatalf("key = %q, frame nil = %t; want key %q", key, frame == nil, test.want)
			}
		})
	}
}

// Through the boot, the mark and bootNetGrace after it, a network that is not up yet goes
// unmentioned: the home screen shows. Setup, an install and an amp fault still show.
func TestQuietBootSaysNothingOfTheNetwork(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		link    hw.Link
		update  int
		faulted bool
		want    string
	}{
		{"connecting", hw.LinkConnecting, -1, false, ""},
		{"down", hw.LinkDown, -1, false, ""},
		{"up", hw.LinkUp, -1, false, ""},
		{"setup", hw.LinkSetupAP, -1, false, "setup0"},
		{"update", hw.LinkDown, 40, false, "update40"},
		{"fault", hw.LinkConnecting, -1, true, "ampfault"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			state := bootAt(test.link)
			state.quiet, state.update, state.faulted = true, test.update, test.faulted

			frame, _, key := bootOverlay(state)
			if key != test.want || (frame == nil) != (test.want == "") {
				t.Fatalf("key = %q, frame nil = %t; want key %q", key, frame == nil, test.want)
			}
		})
	}
}

// quietBoot plays the boot screen of a speaker that serves while its link is link(since), from
// the moment it came to serve, looking every frame step, for as long as span. It returns the
// first time each overlay key showed, and what Quiet was told, in order.
func quietBoot(link func(since time.Duration) hw.Link, span time.Duration) (map[string]time.Duration, []bool) {
	start := time.Unix(1_000_000, 0)
	clock := bootClock{
		started: start.Add(-time.Minute), served: time.Time{}, netcard: time.Time{}, held: 0.5,
		markRun: 0, markLast: time.Time{}, markEnd: time.Time{},
	}

	var told []bool

	quiet := quietTeller(func(q bool) { told = append(told, q) })
	shown := map[string]time.Duration{}

	for since := time.Duration(0); since < span; since += frameStep {
		status := servingStatus()
		status.Link = func() hw.Link { return link(since) }

		boot := clock.state(status, start.Add(since))
		quiet(boot.quiet)

		_, _, key := bootOverlay(boot)
		if _, seen := shown[key]; !seen {
			shown[key] = since
		}
	}

	return shown, told
}

// A network that comes up within bootNetGrace of the mark never gets the connecting screen:
// the mark ends on the home screen (the owner saw "connecting" flash for about 2 s first).
func TestNoConnectingFlashAfterTheMark(t *testing.T) {
	t.Parallel()

	joins := bootMarkFor + 2*time.Second
	shown, told := quietBoot(func(at time.Duration) hw.Link {
		if at < joins {
			return hw.LinkConnecting
		}

		return hw.LinkUp
	}, bootMarkFor+3*bootNetGrace)

	for key := range shown {
		if strings.HasPrefix(key, "offline") {
			t.Errorf("%q showed at %v, while the network joined %v after serving", key, shown[key], joins)
		}
	}

	if _, home := shown[""]; !home {
		t.Error("the home screen never showed")
	}

	if len(told) != 2 || !told[0] || told[1] {
		t.Errorf("Quiet was told %v, want [true false]", told)
	}
}

// A network still not up bootNetGrace after the mark gets the connecting screen, and one
// that is not even trying "no connection", as before; never earlier.
func TestConnectingShowsAfterTheGrace(t *testing.T) {
	t.Parallel()

	for _, link := range []hw.Link{hw.LinkConnecting, hw.LinkDown} {
		shown, told := quietBoot(func(time.Duration) hw.Link { return link }, bootMarkFor+2*bootNetGrace)

		first := time.Duration(-1)

		for key, at := range shown {
			if strings.HasPrefix(key, "offline") && (first < 0 || at < first) {
				first = at
			}
		}

		if first < bootMarkFor+bootNetGrace || first > bootMarkFor+bootNetGrace+2*frameStep {
			t.Errorf("link %d: the offline screen first showed %v after serving, want %v", link, first,
				bootMarkFor+bootNetGrace)
		}

		if len(told) != 2 || !told[0] || told[1] {
			t.Errorf("link %d: Quiet was told %v, want [true false]", link, told)
		}
	}
}

// quietTeller tells only a change, and a nil listener is fine.
func TestQuietTellerTellsChanges(t *testing.T) {
	t.Parallel()

	var told []bool

	tell := quietTeller(func(q bool) { told = append(told, q) })
	for _, q := range []bool{true, true, false, false, true} {
		tell(q)
	}

	if want := []bool{true, false, true}; !slices.Equal(told, want) {
		t.Errorf("told %v, want %v", told, want)
	}

	quietTeller(nil)(true)
}

// The setup screen takes turns between its pages.
func TestSetupPagesTakeTurns(t *testing.T) {
	t.Parallel()

	state := bootAt(hw.LinkSetupAP)
	first, _, firstKey := bootOverlay(state)

	state.since = setupPage + time.Second
	second, _, secondKey := bootOverlay(state)

	if firstKey == secondKey || bytes.Equal(first, second) {
		t.Errorf("setup pages %q and %q look the same", firstKey, secondKey)
	}
}

// An amp fault shows over the network screens, not over an update, and goes when it clears.
func TestAmpFaultShowsOnThePanel(t *testing.T) {
	t.Parallel()

	state := func(link hw.Link, update int, faulted bool) *bootState {
		at := bootAt(link)
		at.update, at.faulted = update, faulted

		return at
	}

	if _, _, key := bootOverlay(state(hw.LinkDown, -1, true)); key != "ampfault" {
		t.Errorf("fault while down: %q", key)
	}

	if _, _, key := bootOverlay(state(hw.LinkUp, -1, true)); key != "ampfault" {
		t.Errorf("fault while up: %q", key)
	}

	if _, _, key := bootOverlay(state(hw.LinkUp, 40, true)); key != "update40" {
		t.Errorf("fault during an update: %q", key)
	}

	if frame, _, key := bootOverlay(state(hw.LinkUp, -1, false)); frame != nil || key != "" {
		t.Errorf("after the fault: %q", key)
	}
}

// The boot bar follows the uptime, holds short of full until the speaker serves, and
// goes full once it does.
func TestBootBarFills(t *testing.T) {
	t.Parallel()

	const row = 50 // the middle of the bar's fill

	lit := func(up time.Duration, waiting bool) int {
		state := bootAt(hw.LinkUp)
		state.booting, state.waiting = true, waiting
		state.uptime = func() (time.Duration, bool) { return up, true }
		frame, _, _ := bootOverlay(state)
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

// Once the speaker serves, the bar fills from where it held, the mark draws itself, and
// the panel fades out at the end by its contrast, frame by frame at 20 a second.
func TestBootMarkPlaysAndFades(t *testing.T) {
	t.Parallel()

	state := bootAt(hw.LinkUp)
	state.held = 0.5

	seen := map[string]bool{}
	lastFade := 0.0

	for since := time.Duration(0); since < bootMarkFor; since += frameStep {
		state.mark = since

		frame, fade, key := bootOverlay(state)
		if frame == nil || seen[key] {
			t.Fatalf("at %v: frame nil %v, key %q seen before", since, frame == nil, key)
		}

		if fade < lastFade {
			t.Fatalf("at %v: the fade went back from %.2f to %.2f", since, lastFade, fade)
		}

		seen[key], lastFade = true, fade
	}

	if lastFade < 0.85 {
		t.Errorf("the mark ends faded %.2f, want almost dark", lastFade)
	}

	state.mark = 0
	if frame, _, _ := bootOverlay(state); !bytes.Equal(frame, bootFrame(0.5)) {
		t.Error("the mark does not start from the bar where it held")
	}
}

// servingStatus is a speaker that serves, on its network, with nothing else going on.
func servingStatus() *BootStatus {
	return &BootStatus{
		Up:      func() bool { return true },
		Link:    func() hw.Link { return hw.LinkUp },
		Update:  func() int { return -1 },
		NetCard: nil,
		Faulted: func() bool { return false },
		Network: nil,
		Quiet:   nil,
	}
}

// nameRows is whether the mark's "SoundTap" is lit under the note, in rows 78 to 92.
func nameRows(frame []byte) bool {
	for y := 78; y <= 92; y++ {
		for x := range oled.Width {
			if frame[y*oled.Width+x] > 0 {
				return true
			}
		}
	}

	return false
}

// markRun plays the boot screen from the moment the speaker serves, looking every step, with
// one look stalled by stall once the mark reaches stallAt (0: none). It returns how long the
// panel showed the mark's name at full brightness before the fade, and when the mark ended.
func markRun(t *testing.T, step, stallAt, stall time.Duration) (time.Duration, time.Duration) {
	t.Helper()

	start := time.Unix(1_000_000, 0)
	clock := bootClock{
		started: start.Add(-time.Minute), served: time.Time{}, netcard: time.Time{}, held: 0.5,
		markRun: 0, markLast: time.Time{}, markEnd: time.Time{},
	}

	var named time.Duration

	stalled := false

	for now := start; now.Sub(start) < time.Minute; now = now.Add(step) {
		boot := clock.state(servingStatus(), now)
		if boot.mark < 0 {
			return named, now.Sub(start)
		}

		frame, fade, _ := bootOverlay(boot)
		shown := step

		if stallAt > 0 && !stalled && boot.mark >= stallAt {
			// The look after this one comes stall late: what decoding the atlas in the
			// middle of the mark did on the ST20. The panel keeps this frame meanwhile.
			stalled, now, shown = true, now.Add(stall), step+stall
		}

		if fade == 0 && boot.mark-bootFill >= 2400*time.Millisecond && nameRows(frame) {
			named += shown
		}
	}

	t.Fatal("the mark never ended")

	return 0, 0
}

// The mark runs on the wall clock, not on frames: at the panel's 20 frames a second or at
// half that, it ends when its time is up, and its name shows at full brightness for at least
// 1.2 s before the fade (the design's boot(): in at 2.0 s, full at 2.4 s, fading from 3.6 s).
func TestBootMarkRunsOnTheClock(t *testing.T) {
	t.Parallel()

	for _, step := range []time.Duration{frameStep, 2 * frameStep} {
		named, ended := markRun(t, step, 0, 0)
		if ended < bootMarkFor || ended > bootMarkFor+step {
			t.Errorf("a look every %v: the mark ended after %v, want %v", step, ended, bootMarkFor)
		}

		if named < 1200*time.Millisecond {
			t.Errorf("a look every %v: the name showed %v at full, want at least 1.2 s", step, named)
		}
	}
}

// A look that comes seconds late, as the first text of a power-on decoding the font atlas did
// on the ST20, does not skip the rest of the mark: the owner saw the note and its holes but
// never the name under it. The mark moves on by markStepMax at most and plays out.
func TestBootMarkSurvivesAStall(t *testing.T) {
	t.Parallel()

	for _, after := range []time.Duration{time.Second, 2 * time.Second, 3 * time.Second} {
		stallAt := bootFill + after

		named, ended := markRun(t, frameStep, stallAt, 3*time.Second)
		if named < 1200*time.Millisecond {
			t.Errorf("stalled at %v: the name showed %v at full, want at least 1.2 s", stallAt, named)
		}

		if ended > bootMarkFor+3*time.Second+markStepMax {
			t.Errorf("stalled at %v: the mark ended after %v, longer than it and the stall", stallAt, ended)
		}
	}
}

// soundtapd's messages get the design's layout by their English text: a question asks for a
// held key under CONFIRM, a known message has its icon, another is shown alone.
func TestSayLayouts(t *testing.T) {
	t.Parallel()

	if bytes.Equal(Say("Install?"), Message(Text("Install?"))) {
		t.Error("a question is shown as a plain message")
	}

	if bytes.Equal(Say("Update failed"), Message(Text("Update failed"))) {
		t.Error("a failed update has no icon")
	}

	if !bytes.Equal(Say("Nothing to store"), Message(Text("Nothing to store"))) {
		t.Error("a message without an icon is not shown alone")
	}

	for english := range questions {
		if _, ok := st20Words["nl"].words[english]; !ok {
			t.Errorf("the question %q has no words", english)
		}
	}

	for english, say := range sayings {
		if say.line != "" {
			if _, ok := st20Words["nl"].words[say.line]; !ok {
				t.Errorf("%q's line %q has no words", english, say.line)
			}
		}
	}
}

// The setup screen is the only instruction an owner gets when the speaker has no
// Wi-Fi, so the network's name has to fit on it as the Wi-Fi manager spells it.
func TestSetupNetworkNameFits(t *testing.T) {
	t.Parallel()

	for _, line := range screen.TitleLines("SoundTap ST20 A1B2C3") {
		if strings.Contains(line, "…") {
			t.Errorf("the default name is cut: %q", line)
		}
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
	return npInfo{np: nowPlaying, presets: nil, room: room, pairing: false, calling: false, channel: ""}
}

// noCorner is a corner with nothing in it.
func noCorner() screen.Corner {
	return screen.Corner{State: "", Preset: 0, Badge: "", BadgeLevel: 0, Mark: "", Battery: 0, Charging: false}
}

// bootAt is the boot screen's state past booting with nothing going on but the link, the
// uptime at zero.
func bootAt(link hw.Link) *bootState {
	return &bootState{
		booting: false, waiting: false, since: 0, mark: -1, held: 0, update: -1, netcard: -1, faulted: false,
		link: link, network: "", uptime: func() (time.Duration, bool) { return 0, true }, quiet: false,
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

// textFaces are the faces the atlas bakes the panel's words into (atlas.py's TEXT faces).
//
//nolint:gochecknoglobals // a fixed list, only read
var textFaces = []string{
	"m700-11", "m700-12", "m700-13", "m700-14", "m700-16", "m700-18", "m700-20", "m600-11", "pix-8",
}

// Every letter of every text the panel shows, in every language, has a glyph of its own in
// every text face: a letter the atlas lacks draws as '?'. Thai, Japanese, Korean and
// Chinese are baked only as far as words.go uses them, so a new word needs a new atlas
// (ATLAS_ONLY=1 app/oled/design/golden.sh). The network card's greetings too.
func TestST20WordsHaveGlyphs(t *testing.T) {
	t.Parallel()

	for lang, table := range st20Words {
		for english, word := range table.words {
			for _, text := range []string{english, word} {
				for _, face := range textFaces {
					if r, ok := missingGlyph(face, text); !ok {
						t.Errorf("%s %q: %s has no glyph for %q (U+%04X)", lang, text, face, r, r)
					}
				}
			}
		}
	}

	for _, words := range screen.Hello {
		for _, face := range []string{"m700-20", "m700-18"} {
			if r, ok := missingGlyph(face, words[0]); !ok {
				t.Errorf("%q: %s has no glyph for %q (U+%04X)", words[0], face, r, r)
			}
		}

		if r, ok := missingGlyph("m600-11", words[1]); !ok {
			t.Errorf("%q: m600-11 has no glyph for %q (U+%04X)", words[1], r, r)
		}
	}
}

// missingGlyph is the first letter of s the face has no glyph for.
func missingGlyph(face, text string) (rune, bool) {
	for _, letter := range text {
		if letter == ' ' {
			continue
		}

		if !screen.Has(face, letter) {
			return letter, false
		}
	}

	return 0, true
}

// In standby the panel slows down; out of it it runs at 20 frames a second, and a key that
// shows something on the panel wakes runDisplay out of its slow tick.
func TestPanelSlowsInStandbyAndWakesOnAPreset(t *testing.T) {
	t.Parallel()

	if panelTickFor(false) != 50*time.Millisecond || panelTickFor(true) != time.Second {
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

// While the ST20's BCO app is written the network is down on purpose: the panel shows the
// network card screen instead of "Connecting…", moving at the frame rate, an install's own
// screen still outranks it, and it goes when the write ends.
func TestNetworkCardUpdateShowsOnThePanel(t *testing.T) {
	t.Parallel()

	state := func(link hw.Link, update int, netcard time.Duration) *bootState {
		at := bootAt(link)
		at.update, at.netcard = update, netcard

		return at
	}

	first, _, key := bootOverlay(state(hw.LinkConnecting, -1, 0))
	if !strings.HasPrefix(key, "netcard") {
		t.Errorf("BCO flash while connecting: %q", key)
	}

	later, _, laterKey := bootOverlay(state(hw.LinkConnecting, -1, 300*time.Millisecond))
	if laterKey == key || bytes.Equal(first, later) {
		t.Error("the network card screen does not move")
	}

	if _, _, key := bootOverlay(state(hw.LinkDown, 40, time.Second)); key != "update40" {
		t.Errorf("BCO flash during an install: %q", key)
	}

	if _, _, key := bootOverlay(state(hw.LinkConnecting, -1, -1)); strings.HasPrefix(key, "netcard") {
		t.Errorf("no BCO flash: %q", key)
	}
}
