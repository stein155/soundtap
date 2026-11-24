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
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/display"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
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
	panelTick      = 100 * time.Millisecond
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
	overlay func() *screen.Frame
	until   time.Time
	presets []speaker.Preset // as runDisplay last read them, for the preset overlay's name
}

// New is the panel of a speaker whose preset keys store after holdToStore.
func New(holdToStore time.Duration) *Panel {
	never := func() bool { return false }
	return &Panel{Pairing: never, Calling: never, holdToStore: holdToStore, wake: make(chan struct{}, 1)}
}

func (p *Panel) nudge() {
	select {
	case p.wake <- struct{}{}:
	default:
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

// PresetDown starts the "storing" bar; PresetUp ends it, whatever came of the press.
func (p *Panel) PresetDown(n int) {
	p.mu.Lock()
	p.holdN, p.holdAt, p.input = n, time.Now(), time.Now()
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

func (p *Panel) setPresets(presets []speaker.Preset) {
	p.mu.Lock()
	p.presets = presets
	p.mu.Unlock()
}

// Current is the overlay to draw now, nil for none, and when the last key was pressed.
func (p *Panel) Current(now time.Time) (func() *screen.Frame, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
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
func cycled(n int, fields ...string) string {
	var seen []string
	for _, f := range fields {
		if f != "" && !slices.Contains(seen, f) {
			seen = append(seen, f)
		}
	}
	if len(seen) == 0 {
		return ""
	}
	return seen[n%len(seen)]
}

// Run drives the ST20 panel while the speaker is on, until ctx ends. In standby it clears,
// so the display manager falls back to the standby clock. No-op on models without the panel.
func (p *Panel) Run(ctx context.Context, sp *speakerstate.Speaker, disp *display.Manager, clock func() (time.Time, bool), log *slog.Logger) {
	if !disp.Available() {
		return
	}
	log.Info("oled now-playing driver up")
	every := panelTick
	tick := time.NewTicker(every)
	defer tick.Stop()
	var info npInfo
	var read time.Time
	seen := seenStatus{volume: -1}
	standby, playing := true, ""
	wired := -1           // the cable as last seen: -1 not yet, 0 out, 1 in
	var started time.Time // when what plays last changed: the metadata cycle starts there
	var drawn playingMemo
	for p.wait(ctx, sp, tick, every) {
		now := time.Now()
		status := sp.StatusSnapshot()
		seen.overlays(p, status)
		overlay, input := p.Current(now)
		retick(tick, &every, panelTickFor(status.Standby && overlay == nil))
		if overlay != nil {
			// Overlays outrank the standby clock too: a preset press wakes the speaker,
			// and its number must show before the speaker has left standby.
			disp.SetTransient(display.Content{Frame: overlay().Pix(15)}, 2*panelTick)
		}
		if now.Sub(read) >= time.Second {
			info, read = p.refresh(ctx, sp, info, &wired), now
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
		at, hour12 := clock()
		bar := screen.Bar{Time: clockText(at, hour12)}
		if p.Battery != nil {
			if level, charging, ok := p.Battery(); ok {
				bar.Battery, bar.Charging = max(level, 1), charging
			}
		}
		dim := 15
		if now.Sub(input) > dimAfter {
			dim = dimLevel
		}
		_, posMs, durMs := sp.Position()
		disp.SetNowPlaying(display.Content{Frame: drawn.playing(info, bar, posMs, durMs, dim)})
	}
}

// wait waits for the next tick, reporting false once ctx ends. At the slow standby rate a
// key, an overlay or a state change wakes the loop at once; at panelTick the nil channels
// leave it as it was.
func (p *Panel) wait(ctx context.Context, sp *speakerstate.Speaker, tick *time.Ticker, every time.Duration) bool {
	var changed <-chan struct{}
	var nudged chan struct{}
	if every == panelStandbyTick {
		changed, nudged = sp.Changed(), p.wake
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
func (p *Panel) refresh(ctx context.Context, sp *speakerstate.Speaker, info npInfo, wired *int) npInfo {
	if msg := linkSwitch(wired, speakerstate.Wired()); msg != "" {
		p.Show(NoticeFor, func() *screen.Frame { return MessageScreen(msg) })
	}
	prev := info.np
	info = p.readNowPlaying(ctx, sp, info)
	if msg := bluetoothChange(prev, info.np); msg != "" {
		p.Show(NoticeFor, func() *screen.Frame { return MessageScreen(msg) })
	}
	p.setPresets(info.presets)
	return info
}

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

func (s *seenStatus) overlays(p *Panel, status speakerstate.StatusSnapshot) {
	if !status.Standby && s.volume >= 0 && (status.Volume != s.volume || status.Muted != s.muted) {
		if status.Muted {
			p.Show(overlayFor, func() *screen.Frame { return screen.Mute(Text("Muted")) })
		} else {
			v := status.Volume
			p.Show(overlayFor, func() *screen.Frame { return screen.Volume(v) })
		}
	}
	if status.EmptyPreset && !s.empty {
		p.Show(overlayFor, func() *screen.Frame { return MessageScreen(Text("Preset empty")) })
	}
	s.volume, s.muted, s.empty = status.Volume, status.Muted, status.EmptyPreset
}

// playingMemo keeps the last playing screen: the ST20 takes milliseconds to draw one, so
// it is drawn again only when what it shows changes.
type playingMemo struct {
	key string
	pix []byte
}

func (m *playingMemo) playing(info npInfo, bar screen.Bar, posMs, durMs, dim int) []byte {
	posMs -= posMs % 1000 // the screen shows whole seconds; redraw once a second at most
	key := fmt.Sprintf("%+v|%+v|%v|%v|%d|%d|%d", *info.np, info.presets, info.pairing, bar, posMs, durMs, dim)
	if key != m.key {
		m.pix, m.key = playingScreen(info, bar, posMs, durMs).Pix(dim), key
	}
	return m.pix
}

func (p *Panel) readNowPlaying(ctx context.Context, sp *speakerstate.Speaker, last npInfo) npInfo {
	np, err := sp.NowPlaying(ctx)
	if err != nil {
		np = nil
	}
	out := npInfo{np: np, presets: last.presets, room: last.room, pairing: p.Pairing(), calling: p.Calling()}
	out.channel, _ = p.channel.Load().(string)
	if presets, err := sp.Presets(ctx); err == nil {
		out.presets = presets
	}
	if i, err := sp.Info(ctx); err == nil && i != nil {
		out.room = i.Name
	}
	return out
}

func clockText(t time.Time, hour12 bool) string {
	if hour12 {
		return t.Format("3:04")
	}
	return t.Format("15:04")
}

// playingScreen picks the design's screen for what plays.
func playingScreen(info npInfo, bar screen.Bar, posMs, durMs int) *screen.Frame {
	np := info.np
	switch info.channel {
	case "left":
		bar.Mark = "L"
	case "right":
		bar.Mark = "R"
	}
	switch np.PlayStatus {
	case statusPaused:
		bar.State = screen.StatePaused
	case playStopped:
		bar.State = screen.StateStopped
	}
	switch np.Source {
	case sourceTuneIn:
		return stationScreen(info, bar)
	case sourceBluetooth:
		return bluetoothScreen(info, bar)
	case sourceAux:
		return screen.Aux(bar, Text("External input"))
	default:
		bar.Source = prettySource(np.Source)
		title, sub := np.Track, cycled(info.cycle, np.Artist, np.Album, np.Station)
		if title == "" {
			title, sub = np.Station, ""
		}
		if title == "" {
			title = prettySource(np.Source)
		}
		return screen.Track(bar, title, stateLine(np, sub), float64(posMs)/1000, durMs/1000)
	}
}

// bluetoothScreen is Bluetooth's screen: the device that plays, or what the speaker is
// doing to get one.
func bluetoothScreen(info npInfo, bar screen.Bar) *screen.Frame {
	np := info.np
	bar.Source, bar.Room = screen.SourceBT, info.room
	device := np.Station
	if device == "" && info.pairing {
		return screen.Pairing(Text("Pairing…"), Text("Look for %s", info.room))
	}
	if device == "" && info.calling {
		device = Text("Reconnecting…")
	}
	if device == "" {
		device = Text("Waiting for device")
	}
	return screen.Bluetooth(bar, prettySource(np.Source), device)
}

// stationScreen is the radio's screen: the station, what plays on it, its preset and the
// live badge.
func stationScreen(info npInfo, bar screen.Bar) *screen.Frame {
	np := info.np
	badge, level := wordLive, 10
	if np.PlayStatus == playBuffering || np.Unheard {
		badge, level = wordBuffering, 7
	}
	if bar.State != "" {
		badge = "" // not live while paused or stopped: the bar's icon says so
	}
	name := np.Station
	if name == "" {
		name = prettySource(np.Source)
	}
	return screen.Station(bar, name, stateLine(np, cycled(info.cycle, np.Track, np.Artist, np.Album)), presetOf(info.presets, np),
		Text(badge), level)
}

// linkSwitch is what the panel says when the cable goes in or out, as stock says it
// switches between Wi-Fi and Ethernet; nothing on the first look.
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

func prettySource(s string) string {
	switch s {
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
		return s
	}
}

// MessageScreen is any other short message (a confirmation, an error), in the design's
// type: up to three centred lines.
func MessageScreen(text string) *screen.Frame {
	return screen.Message(text)
}

// Message is MessageScreen as the display manager takes it.
func Message(text string) []byte { return MessageScreen(text).Pix(15) }

// retick moves t to want when that differs from the period it runs at; an unchanged
// period leaves the ticker's phase alone.
func retick(t *time.Ticker, every *time.Duration, want time.Duration) {
	if want != *every {
		*every = want
		t.Reset(want)
	}
}
