// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package panel draws the ST20's front panel (the OLED, app/oled): the boot and setup
// screens, a screen per source while playing, overlays for what the keys do, and the
// standby clock, in the speaker's language.
package panel

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/display"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/oled"
	"github.com/stein155/soundtap/app/oled/screen"
)

// The ST20 panel follows the project's design (app/oled/design/design-screens.js): a screen per
// source while playing, overlays for volume, presets and storing a preset, and a dimmed
// clock in standby (clock.go). Wi-Fi is not on it: the speaker has a lamp for that.

const (
	overlayFor     = 1500 * time.Millisecond // volume and preset overlays
	NoticeFor      = 3 * time.Second         // Bluetooth connect and disconnect, a cleared list
	savedFor       = 1800 * time.Millisecond
	holdShowsAfter = 350 * time.Millisecond // a preset press becomes "storing" after this
	dimAfter       = 30 * time.Second       // without a key press, the panel dims...
	dimLevel       = 6                      // ...to at most this grey level
	fullLevel      = 15                     // the panel's brightest grey level: Pix draws it undimmed
	// overlayHold is how long one look at an overlay keeps it up: two ticks, so it lasts
	// until the next look renews it.
	overlayHold = 2 * panelTick
	// msPerSecond turns the position's milliseconds into the seconds the screen shows.
	msPerSecond = 1000
	panelTick   = 100 * time.Millisecond
	// In standby the panel shows the clock, which changes once a minute: a tick per second
	// is plenty. A key, an overlay or a state change wakes the loop at once, and it
	// runs at panelTick again from there.
	panelStandbyTick = time.Second

	wordLive      = "LIVE" // the radio badges, keys of Text
	wordBuffering = "BUFFERING"
)

// The source names and play statuses the panel tells apart, as they appear in the API.
const (
	sourceAirPlay   = "AIRPLAY"
	sourceAux       = "AUX"
	sourceBluetooth = "BLUETOOTH"
	sourceMultiroom = "MULTIROOM"
	sourceSpotify   = "SPOTIFY"
	sourceStandby   = "STANDBY"
	sourceTuneIn    = "TUNEIN"
	sourceUPnP      = "UPNP"
	playBuffering   = "BUFFERING_STATE"
	playStopped     = "STOP_STATE"
	statusPaused    = "PAUSE_STATE"
)

// Panel is the panel while the speaker is on: what the keys tell it, and what it shows
// besides the speaker's state. The keys write it, Run reads it.
type Panel struct {
	// Pairing reports the open Bluetooth pairing window, Calling that the speaker is calling
	// its paired phones. Set them before Run.
	Pairing, Calling func() bool
	// Battery, when set, is the pack's level and whether it charges, for the status bar; ok
	// is false while there is no reading. The Portable sets it.
	Battery func() (level int, charging, ok bool)

	holdToStore time.Duration // how long a preset key is held to store
	// channel is the stored channel ("left", "right", ...): half of a stereo pair puts
	// its side in the status bar, as stock marks each speaker of a pair.
	channel atomic.Value
	// wake wakes Run out of its slow standby tick when a key shows something.
	wake chan struct{}

	mu      sync.Mutex
	input   time.Time // last key press
	holdN   int       // preset key held down, 0 for none
	holdAt  time.Time
	chord   string        // what a held key combination does (a key of Text), "" for none
	chordAt time.Time     // since when it is held
	chordOf time.Duration // how long it must be held
	overlay func() *screen.Frame
	until   time.Time
	presets []speaker.Preset // as runDisplay last read them, for the preset overlay's name
}

// New is the panel of a speaker whose preset keys store after holdToStore.
func New(holdToStore time.Duration) *Panel {
	never := func() bool { return false }

	return &Panel{
		Pairing:     never,
		Calling:     never,
		Battery:     nil,
		holdToStore: holdToStore,
		channel:     atomic.Value{},
		wake:        make(chan struct{}, 1),
		mu:          sync.Mutex{},
		input:       time.Time{},
		holdN:       0,
		holdAt:      time.Time{},
		chord:       "",
		chordAt:     time.Time{},
		chordOf:     0,
		overlay:     nil,
		until:       time.Time{},
		presets:     nil,
	}
}

// SetChannel is the stored channel ("left", "right", ...), for the status bar.
func (p *Panel) SetChannel(name string) { p.channel.Store(name) }

// Touch is a key press: the panel lights up again.

func (p *Panel) Touch() {
	p.mu.Lock()
	p.input = time.Now()
	p.mu.Unlock()
}

// Show shows f over everything for d.
func (p *Panel) Show(d time.Duration, f func() *screen.Frame) {
	f = sync.OnceValue(f) // drawn once, shown every tick

	p.mu.Lock()
	p.overlay, p.until, p.input = f, time.Now().Add(d), time.Now()
	p.mu.Unlock()
	p.nudge()
}

// ChordDown starts the count of a key combination held for hold: what it does (word, a key of
// Text) over the seconds left and a bar, above every other overlay, until ChordUp. The owner
// let go of the setup combo at 7 and 9 s while the panel showed nothing (ST20, 2026-11-27).
func (p *Panel) ChordDown(word string, hold time.Duration) {
	p.mu.Lock()
	p.chord, p.chordAt, p.chordOf, p.input = word, time.Now(), hold, time.Now()
	p.mu.Unlock()
	p.nudge()
}

// Counting says a held combination's count is up.
func (p *Panel) Counting() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.chord != "" && p.chordOf > 0
}

// ChordUp ends the count: the combination fired or was let go.
func (p *Panel) ChordUp() {
	p.mu.Lock()
	p.chord = ""
	p.mu.Unlock()
}

// PresetDown starts the "storing" bar; PresetUp ends it, whatever came of the press.
func (p *Panel) PresetDown(slot int) {
	p.mu.Lock()
	p.holdN, p.holdAt, p.input = slot, time.Now(), time.Now()
	p.mu.Unlock()
	p.nudge()
}

// PresetUp ends the "storing" bar.
func (p *Panel) PresetUp() {
	p.mu.Lock()
	p.holdN = 0
	p.mu.Unlock()
}

// PresetTapped shows the preset's number and name while its station starts.
func (p *Panel) PresetTapped(n int) {
	p.mu.Lock()
	name := presetName(p.presets, n)
	p.mu.Unlock()
	p.PresetUp()
	p.Show(overlayFor, func() *screen.Frame { return screen.Preset(Text("PRESET"), n, name) })
}

// PresetStored confirms a held preset key stored what plays.
func (p *Panel) PresetStored() {
	p.PresetUp()
	p.Show(savedFor, func() *screen.Frame { return screen.Saved(Text("Saved as preset")) })
}

// Current is the overlay to draw now, nil for none, and when the last key was pressed.
func (p *Panel) Current(now time.Time) (func() *screen.Frame, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.chord != "" && p.chordOf > 0 {
		word, held, of := p.chord, now.Sub(p.chordAt), p.chordOf
		left := int(math.Ceil((of - held).Seconds()))

		return func() *screen.Frame {
			return screen.Holding(Text(word), max(left, 0), float64(held)/float64(of))
		}, p.input
	}

	if p.holdN > 0 && now.Sub(p.holdAt) >= holdShowsAfter {
		n, progress := p.holdN, float64(now.Sub(p.holdAt)-holdShowsAfter)/float64(p.holdToStore-holdShowsAfter)

		return func() *screen.Frame { return screen.Holding(Text("Save as preset"), n, progress) }, p.input
	}

	if p.overlay != nil && now.Before(p.until) {
		return p.overlay, p.input
	}

	p.overlay = nil

	return nil, p.input
}

// npInfo is what the playing screen needs beyond the status, read once a second: reading
// it on every frame would ask the backend ten times a second for what changes rarely.
type npInfo struct {
	np      *speaker.NowPlaying
	presets []speaker.Preset
	room    string
	pairing bool // the Bluetooth pairing window is open
	calling bool // the speaker is calling its paired phones
	cycle   int  // which metadata field the second line shows: one more every subCycle
	channel string
}

// subCycle is how long the second line shows one field before the next, as stock cycles
// station, track, artist and album under the source. A new track starts over.
const subCycle = 4 * time.Second

// cycled is the field the second line shows at step n, skipping empty ones and repeats.
func cycled(step int, fields ...string) string {
	var seen []string
	for _, f := range fields {
		if f != "" && !slices.Contains(seen, f) {
			seen = append(seen, f)
		}
	}

	if len(seen) == 0 {
		return ""
	}

	return seen[step%len(seen)]
}

// Run drives the ST20 panel while the speaker is on, until ctx ends. In standby it clears,
// so the display manager falls back to the standby clock. No-op on models without the panel.
func (p *Panel) Run(
	ctx context.Context, spk *speakerstate.Speaker, disp *display.Manager, clock func() (time.Time, bool),
	log *slog.Logger,
) {
	if !disp.Available() {
		return
	}

	log.Info("oled now-playing driver up")

	every := panelTick

	tick := time.NewTicker(every)
	defer tick.Stop()

	var (
		info npInfo
		read time.Time
	)

	seen := seenStatus{volume: -1, muted: false, empty: false}
	standby, playing := true, ""
	wired := -1 // the cable as last seen: -1 not yet, 0 out, 1 in

	var started time.Time // when what plays last changed: the metadata cycle starts there

	var drawn playingMemo

	for p.wait(ctx, spk, tick, every) {
		now := time.Now()
		status := spk.StatusSnapshot()
		seen.overlays(p, &status)
		overlay, input := p.Current(now)
		retick(tick, &every, panelTickFor(status.Standby && overlay == nil))

		switch {
		case p.Counting():
			// A held combination's count outranks even the boot and setup screens.
			disp.SetAlert(display.Content{Frame: overlay().Pix(fullLevel)}, overlayHold)
		case overlay != nil:
			// Overlays outrank the standby clock too: a preset press wakes the speaker,
			// and its number must show before the speaker has left standby.
			disp.SetTransient(display.Content{Frame: overlay().Pix(fullLevel)}, overlayHold)
		default:
			// Nothing over the screen.
		}

		if now.Sub(read) >= time.Second {
			info, read = p.refresh(ctx, spk, &info, &wired), now
		}

		if status.Standby || info.np == nil || info.np.Source == sourceStandby {
			if !standby {
				disp.ClearNowPlaying()

				standby = true
			}

			continue
		}
		// Waking up or a new track counts as use: the panel lights up for it, then dims.
		if what := info.np.Source + "\x00" + info.np.Station + "\x00" + info.np.Track; standby || what != playing {
			p.Touch()

			playing, input, started = what, now, now
		}

		info.cycle = int(now.Sub(started) / subCycle)
		standby = false
		bar := p.statusBar(clock)
		dim := dimFor(now, input)

		_, posMs, durMs := spk.Position()
		orbit := int(now.Unix() / int64(orbitStep/time.Second))
		disp.SetNowPlaying(display.Content{Frame: drawn.playing(&info, &bar, posMs, durMs, dim, orbit)})
	}
}

func (p *Panel) nudge() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *Panel) setPresets(presets []speaker.Preset) {
	p.mu.Lock()
	p.presets = presets
	p.mu.Unlock()
}

// statusBar is the status line over the playing screen: the time and, on a speaker with a
// pack, its level.
func (p *Panel) statusBar(clock func() (time.Time, bool)) screen.Bar {
	at, hour12 := clock()
	bar := screen.Bar{
		Source: "", Room: "", Time: clockText(at, hour12), State: "", Mark: "", Battery: 0, Charging: false,
	}

	if p.Battery != nil {
		if level, charging, ok := p.Battery(); ok {
			bar.Battery, bar.Charging = max(level, 1), charging
		}
	}

	return bar
}

// dimFor is the brightest grey level the panel may show: dimmed a while after the last key.
func dimFor(now, input time.Time) int {
	if now.Sub(input) > dimAfter {
		return dimLevel
	}

	return fullLevel
}

// wait waits for the next tick, reporting false once ctx ends. At the slow standby rate a
// key, an overlay or a state change wakes the loop at once; at panelTick the nil channels
// leave it as it was.
func (p *Panel) wait(ctx context.Context, spk *speakerstate.Speaker, tick *time.Ticker, every time.Duration) bool {
	var (
		changed <-chan struct{}
		nudged  chan struct{}
	)
	if every == panelStandbyTick {
		changed, nudged = spk.Changed(), p.wake
	}

	select {
	case <-ctx.Done():
		return false
	case <-tick.C:
	case <-changed:
	case <-nudged:
	}

	return true
}

// refresh reads what plays again, and says on the panel when the cable or the Bluetooth
// device changed. wired is the cable as last seen: -1 not yet, 0 out, 1 in.
func (p *Panel) refresh(ctx context.Context, spk *speakerstate.Speaker, last *npInfo, wired *int) npInfo {
	if msg := linkSwitch(wired, speakerstate.Wired()); msg != "" {
		p.Show(NoticeFor, func() *screen.Frame { return MessageScreen(msg) })
	}

	info := p.readNowPlaying(ctx, spk, last)
	if msg := bluetoothChange(last.np, info.np); msg != "" {
		p.Show(NoticeFor, func() *screen.Frame { return MessageScreen(msg) })
	}

	p.setPresets(info.presets)

	return info
}

func (p *Panel) readNowPlaying(ctx context.Context, spk *speakerstate.Speaker, last *npInfo) npInfo {
	nowPlaying, err := spk.NowPlaying(ctx)
	if err != nil {
		nowPlaying = nil
	}

	out := npInfo{
		np:      nowPlaying,
		presets: last.presets,
		room:    last.room,
		pairing: p.Pairing(),
		calling: p.Calling(),
		cycle:   0,
		channel: "",
	}

	out.channel, _ = p.channel.Load().(string)

	presets, err := spk.Presets(ctx)
	if err == nil {
		out.presets = presets
	}

	info, err := spk.Info(ctx)
	if err == nil && info != nil {
		out.room = info.Name
	}

	return out
}

//nolint:revive // flag-parameter: standby picks the period, the one thing this says
func panelTickFor(standby bool) time.Duration {
	if standby {
		return panelStandbyTick
	}

	return panelTick
}

// seenStatus is the status the last tick saw, to show an overlay when it changes.
type seenStatus struct {
	volume       int
	muted, empty bool
}

func (s *seenStatus) overlays(front *Panel, status *speakerstate.StatusSnapshot) {
	if !status.Standby && s.volume >= 0 && (status.Volume != s.volume || status.Muted != s.muted) {
		if status.Muted {
			front.Show(overlayFor, func() *screen.Frame { return screen.Mute(Text("Muted")) })
		} else {
			v := status.Volume

			front.Show(overlayFor, func() *screen.Frame { return screen.Volume(v) })
		}
	}

	if status.EmptyPreset && !s.empty {
		front.Show(overlayFor, func() *screen.Frame { return MessageScreen(Text("Preset empty")) })
	}

	s.volume, s.muted, s.empty = status.Volume, status.Muted, status.EmptyPreset
}

// playingMemo keeps the last playing screen: the ST20 takes milliseconds to draw one, so
// it is drawn again only when what it shows changes.
type playingMemo struct {
	key string
	pix []byte
}

func (m *playingMemo) playing(info *npInfo, bar *screen.Bar, posMs, durMs, dim, orbit int) []byte {
	posMs -= posMs % msPerSecond // the screen shows whole seconds; redraw once a second at most

	key := fmt.Sprintf("%+v|%+v|%v|%v|%d|%d|%d|%d",
		*info.np, info.presets, info.pairing, *bar, posMs, durMs, dim, orbit)
	if key != m.key {
		m.pix, m.key = orbitFrame(playingScreen(info, bar, posMs, durMs).Pix(dim), orbit), key
	}

	return m.pix
}

// orbitStep is how long the playing screen stays put before it moves one pixel.
const orbitStep = time.Minute

// orbitPath is the playing screen's offsets, one per orbitStep: a pixel around its place
// and back. The layout stays where it is to the eye, yet no edge of a letter, the progress
// bar or a badge lights the same pixels for more than a minute at a time: the OLED's
// burn-in comes from what stays lit in one place. The standby clock walks the whole panel
// instead (walkFrame); a playing screen fills it and has nowhere to walk.
//
//nolint:gochecknoglobals // a constant table
var orbitPath = [...][2]int{{0, 0}, {1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}}

// orbitFrame moves what pix shows to the step-th place of orbitPath; what moves off the
// panel is dropped (the screens keep 3 px clear of every edge).
func orbitFrame(pix []byte, step int) []byte {
	const width, height = oled.Width, oled.Height

	offset := orbitPath[(step%len(orbitPath)+len(orbitPath))%len(orbitPath)]
	if offset == [2]int{} {
		return pix
	}

	out := make([]byte, len(pix))

	for row := range height {
		toRow := row + offset[1]
		if toRow < 0 || toRow >= height {
			continue
		}

		for col := range width {
			if toCol := col + offset[0]; toCol >= 0 && toCol < width {
				out[toRow*width+toCol] = pix[row*width+col]
			}
		}
	}

	return out
}

//nolint:revive // flag-parameter: hour12 is the owner's clock setting, passed through as data
func clockText(t time.Time, hour12 bool) string {
	if hour12 {
		return t.Format("3:04")
	}

	return t.Format("15:04")
}

// playingScreen picks the design's screen for what plays.
func playingScreen(info *npInfo, base *screen.Bar, posMs, durMs int) *screen.Frame {
	bar := *base // marked up for this screen; the caller's stays as it was
	nowPlaying := info.np

	switch info.channel {
	case "left":
		bar.Mark = "L"
	case "right":
		bar.Mark = "R"
	default:
		// Not half of a stereo pair: no mark.
	}

	switch nowPlaying.PlayStatus {
	case statusPaused:
		bar.State = screen.StatePaused
	case playStopped:
		bar.State = screen.StateStopped
	default:
		// Playing or buffering: the source shows in the icon's place.
	}

	switch nowPlaying.Source {
	case sourceTuneIn:
		return stationScreen(info, &bar)
	case sourceBluetooth:
		return bluetoothScreen(info, &bar)
	case sourceAux:
		return screen.Aux(bar, Text("External input"))
	default:
		bar.Source = prettySource(nowPlaying.Source)

		title, sub := nowPlaying.Track, cycled(info.cycle, nowPlaying.Artist, nowPlaying.Album, nowPlaying.Station)
		if title == "" {
			title, sub = nowPlaying.Station, ""
		}

		if title == "" {
			title = prettySource(nowPlaying.Source)
		}

		return screen.Track(bar, title, stateLine(nowPlaying, sub), float64(posMs)/msPerSecond, durMs/msPerSecond)
	}
}

// bluetoothScreen is Bluetooth's screen: the device that plays, or what the speaker is
// doing to get one.
func bluetoothScreen(info *npInfo, bar *screen.Bar) *screen.Frame {
	nowPlaying := info.np
	bar.Source, bar.Room = screen.SourceBT, info.room

	device := nowPlaying.Station
	if device == "" && info.pairing {
		return screen.Pairing(Text("Pairing…"), Text("Look for %s", info.room))
	}

	if device == "" && info.calling {
		device = Text("Reconnecting…")
	}

	if device == "" {
		device = Text("Waiting for device")
	}

	return screen.Bluetooth(*bar, prettySource(nowPlaying.Source), device)
}

// stationScreen is the radio's screen: the station, what plays on it, its preset and the
// live badge.
func stationScreen(info *npInfo, bar *screen.Bar) *screen.Frame {
	nowPlaying := info.np

	badge, level := wordLive, 10
	if nowPlaying.PlayStatus == playBuffering || nowPlaying.Unheard {
		badge, level = wordBuffering, 7
	}

	if bar.State != "" {
		badge = "" // not live while paused or stopped: the bar's icon says so
	}

	name := nowPlaying.Station
	if name == "" {
		name = prettySource(nowPlaying.Source)
	}

	line := stateLine(nowPlaying, cycled(info.cycle, nowPlaying.Track, nowPlaying.Artist, nowPlaying.Album))

	return screen.Station(*bar, name, line, presetOf(info.presets, nowPlaying), Text(badge), level)
}

// linkSwitch is what the panel says when the cable goes in or out, as stock says it
// switches between Wi-Fi and Ethernet; nothing on the first look.
//
//nolint:revive // flag-parameter: wired is the cable's state, data to compare, not a mode
func linkSwitch(last *int, wired bool) string {
	now := 0
	if wired {
		now = 1
	}

	was := *last

	*last = now
	switch {
	case was < 0 || was == now:
		return ""
	case wired:
		return Text("Switching to Ethernet")
	default:
		return Text("Switching to Wi-Fi")
	}
}

// bluetoothChange is what the panel says when the Bluetooth device changes, as stock
// does: connected to a device, or disconnected from it while Bluetooth stays the source.
// Leaving Bluetooth for another source says nothing.
func bluetoothChange(prev, cur *speaker.NowPlaying) string {
	device := func(np *speaker.NowPlaying) string {
		if np == nil || np.Source != sourceBluetooth {
			return ""
		}

		return np.Station
	}

	was, is := device(prev), device(cur)
	switch {
	case is != "" && is != was:
		return Text("Connected to %s", is)
	case was != "" && is == "" && cur != nil && cur.Source == sourceBluetooth:
		return Text("Disconnected from %s", was)
	}

	return ""
}

// stateLine is the second line: what plays, unless it failed. Pause and stop are an
// icon in the status bar, so what is paused stays readable.
func stateLine(np *speaker.NowPlaying, line string) string {
	if !np.Failed {
		return line
	}

	if np.Source == sourceTuneIn {
		return Text("Station unavailable")
	}

	return Text("Playback error")
}

// presetOf is the preset slot that holds what plays, 0 when none does.
func presetOf(presets []speaker.Preset, np *speaker.NowPlaying) int {
	for _, p := range presets {
		if (np.Location != "" && p.Location == np.Location) || (np.StationID != "" && p.StationID == np.StationID) {
			return p.Slot
		}
	}

	return 0
}

func presetName(presets []speaker.Preset, slot int) string {
	for _, p := range presets {
		if p.Slot == slot {
			return p.Name
		}
	}

	return ""
}

func prettySource(source string) string {
	switch source {
	case sourceTuneIn:
		return "Radio"
	case sourceAirPlay:
		return "AirPlay"
	case sourceBluetooth:
		return "Bluetooth"
	case sourceSpotify:
		return "Spotify"
	case sourceUPnP:
		return "UPnP"
	case sourceMultiroom:
		return "Multiroom"
	case sourceAux:
		return sourceAux
	default:
		return source
	}
}

// MessageScreen is any other short message (a confirmation, an error), in the design's
// type: up to three centred lines.
func MessageScreen(text string) *screen.Frame {
	return screen.Message(text)
}

// Message is MessageScreen as the display manager takes it.
func Message(text string) []byte { return MessageScreen(text).Pix(fullLevel) }

// retick moves t to want when that differs from the period it runs at; an unchanged
// period leaves the ticker's phase alone.
func retick(t *time.Ticker, every *time.Duration, want time.Duration) {
	if want != *every {
		*every = want
		t.Reset(want)
	}
}
