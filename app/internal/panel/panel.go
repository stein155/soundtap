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
	"sync"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/display"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/oled"
	"github.com/stein155/soundtap/app/oled/screen"
)

// The ST20 panel follows the project's design (app/oled/design/design-screens.js): one thing
// a screen, a screen per source while playing, overlays for volume, presets and storing a
// preset, and a dim clock in standby (clock.go). Wi-Fi is not on it: the speaker has a lamp
// for that.

const (
	overlayFor     = 1500 * time.Millisecond // volume and preset overlays
	NoticeFor      = 3 * time.Second         // Bluetooth connect and disconnect, a cleared list
	savedFor       = 1800 * time.Millisecond
	holdShowsAfter = 350 * time.Millisecond // a preset press becomes "storing" after this
	dimAfter       = 30 * time.Second       // without a key press, the panel dims to its title
	fullLevel      = 15                     // the panel's brightest grey level: Pix draws it undimmed
	// overlayHold is how long one look at an overlay keeps it up. It must outlast the time
	// between two looks, and a panel write takes 80-200 ms on the ST20: a hold of two 50 ms
	// ticks let a held combination's count lapse between looks, so the playing screen
	// flashed through it.
	overlayHold = 500 * time.Millisecond
	// msPerSecond turns the position's milliseconds into the seconds the screen shows.
	msPerSecond = 1000
	// panelTick is the panel's frame rate while awake: 20 a second, for the radio's fade and
	// the animations. A frame that did not change is not drawn again (playingMemo), and the
	// display sends only the pixels that changed.
	panelTick = 50 * time.Millisecond
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
	p.Show(savedFor, func() *screen.Frame { return screen.Saved(Text("Saved")) })
}

// Current is the overlay to draw now, nil for none, and when the last key was pressed.
func (p *Panel) Current(now time.Time) (func() *screen.Frame, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.chord != "" && p.chordOf > 0 {
		word, held, of := p.chord, now.Sub(p.chordAt), p.chordOf
		left := int(math.Ceil((of - held).Seconds()))

		return func() *screen.Frame {
			return screen.Countdown(max(left, 0), float64(held)/float64(of), Text(word))
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
	channel string
}

// The radio alternates its station page and what the station says plays every radioPage,
// with a short fade of the whole panel between them: the panel's contrast, no redraw.
const (
	radioPage = 8 * time.Second
	radioFade = 200 * time.Millisecond
)

// radioPages is how many pages the radio takes turns between.
const radioPages = 2

// radioView is the page the radio shows at since into a station (0 the station, 1 what
// plays) and how far the panel is faded: out over the last radioFade of a page, in over the
// first radioFade of the next. The first page shows at once.
func radioView(since time.Duration) (int, float64) {
	page := int(since/radioPage) % radioPages
	into := since % radioPage

	switch {
	case since >= radioPage && into < radioFade:
		return page, 1 - float64(into)/float64(radioFade)
	case radioPage-into < radioFade:
		return page, 1 - float64(radioPage-into)/float64(radioFade)
	default:
		return page, 0
	}
}

// view is what the playing screen shows at one moment.
type view struct {
	page    int     // the radio's page
	dimmed  bool    // a while after the last key: the title alone, dim
	orbit   int     // the burn-in orbit's step
	drift   int     // the dim title's drift step
	dotStep int     // the waiting dots' step
	fade    float64 // the whole panel faded, 0 to 1
}

// Run drives the ST20 panel while the speaker is on, until ctx ends. In standby it clears,
// so the display manager falls back to the standby clock. No-op on models without the panel.
func (p *Panel) Run(
	ctx context.Context, spk *speakerstate.Speaker, disp *display.Manager, log *slog.Logger,
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
	standby := true
	wired := -1 // the cable as last seen: -1 not yet, 0 out, 1 in

	var (
		drawn playingMemo
		track playingTrack
	)

	for p.wait(ctx, spk, tick, every) {
		now := time.Now()
		status := spk.StatusSnapshot()
		seen.overlays(p, &status)
		overlay, input := p.Current(now)
		retick(tick, &every, panelTickFor(status.Standby && overlay == nil))

		p.showOverlay(disp, overlay)

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

		if track.update(p, info.np, standby, now) {
			input = now
		}

		standby = false
		corner := p.corner()

		_, posMs, durMs := spk.Position()
		moment := viewAt(now, now.Sub(track.started), now.Sub(input) > dimAfter)

		if moment.dimmed || !alternates(info.np) {
			moment.page, moment.fade = 0, 0
		}

		frame := drawn.playing(&info, &corner, posMs, durMs, &moment)
		disp.SetNowPlaying(display.Content{Frame: frame, Fade: moment.fade})
	}
}

// showOverlay puts what a key or a held combination shows over the panel.
func (p *Panel) showOverlay(disp *display.Manager, overlay func() *screen.Frame) {
	switch {
	case overlay != nil && p.Counting():
		// A held combination's count outranks even the boot and setup screens. Current and
		// Counting take the lock apart, so a combination pressed between them can leave
		// overlay nil here: then nothing is drawn until the next tick.
		disp.SetAlert(display.Content{Frame: overlay().Pix(fullLevel), Fade: 0}, overlayHold)
	case overlay != nil:
		// Overlays outrank the standby clock too: a preset press wakes the speaker,
		// and its number must show before the speaker has left standby.
		disp.SetTransient(display.Content{Frame: overlay().Pix(fullLevel), Fade: 0}, overlayHold)
	default:
		// Nothing over the screen.
	}
}

// playingTrack is what the playing screen last showed: the track, to light the panel up for
// a new one, and the station, whose radio pages start when it does.
type playingTrack struct {
	what, where string
	started     time.Time
}

// update notes what plays now and says whether that is new: waking up or a new track counts
// as use, and the panel lights up for it, then dims.
//
//nolint:revive // flag-parameter: waking is the speaker's state, data to compare
func (t *playingTrack) update(front *Panel, np *speaker.NowPlaying, waking bool, now time.Time) bool {
	if where := np.Source + "\x00" + np.Station; waking || where != t.where {
		t.where, t.started = where, now
	}

	what := np.Source + "\x00" + np.Station + "\x00" + np.Track
	if !waking && what == t.what {
		return false
	}

	t.what = what

	front.Touch()

	return true
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

// corner is what every playing screen keeps in its corners: on a speaker with a pack, its
// level.
func (p *Panel) corner() screen.Corner {
	corner := screen.Corner{State: "", Preset: 0, Badge: "", BadgeLevel: 0, Mark: "", Battery: 0, Charging: false}

	if p.Battery != nil {
		if level, charging, ok := p.Battery(); ok {
			corner.Battery, corner.Charging = max(level, 1), charging
		}
	}

	return corner
}

// dotStep is how long each of the waiting dots stays lit.
const dotStep = 500 * time.Millisecond

// viewAt is the playing screen's moment: now, since into the station, and whether the
// panel has dimmed.
func viewAt(now time.Time, since time.Duration, dimmed bool) view {
	page, fade := radioView(since)
	minute := int(now.Unix() / int64(orbitStep/time.Second))

	return view{
		page: page, dimmed: dimmed, orbit: minute, drift: minute,
		dotStep: int(now.UnixMilli() / dotStep.Milliseconds()), fade: fade,
	}
}

// alternates says the radio shows what plays on its own page: a live station that names
// its song. The dimmed screen and a paused, buffering or failed station keep one page.
func alternates(np *speaker.NowPlaying) bool {
	return np.Source == sourceTuneIn && np.Track != "" && !np.Failed && !np.Unheard &&
		np.PlayStatus != statusPaused && np.PlayStatus != playStopped && np.PlayStatus != playBuffering
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
	if kind := linkSwitch(wired, speakerstate.Wired()); kind != "" {
		p.Show(NoticeFor, func() *screen.Frame { return linkScreen(kind) })
	}

	info := p.readNowPlaying(ctx, spk, last)
	if change := bluetoothChange(last.np, info.np); change.device != "" {
		p.Show(NoticeFor, func() *screen.Frame {
			return screen.Notice(change.icon, change.device, "", screen.NoticeOpt{IconLevel: 0, Small: true, Dots: 0})
		})
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
			front.Show(overlayFor, func() *screen.Frame { return notice(screen.IconMute, Text("Muted"), "") })
		} else {
			v := status.Volume

			front.Show(overlayFor, func() *screen.Frame { return screen.Volume(v) })
		}
	}

	if status.EmptyPreset && !s.empty {
		slot := status.EmptySlot

		front.Show(overlayFor, func() *screen.Frame {
			if slot <= 0 {
				return MessageScreen(Text("Preset empty"))
			}

			return screen.PresetEmpty(slot, Text("Preset empty"))
		})
	}

	s.volume, s.muted, s.empty = status.Volume, status.Muted, status.EmptyPreset
}

// playingMemo keeps the last playing screen: the ST20 takes milliseconds to draw one, so
// it is drawn again only when what it shows changes.
type playingMemo struct {
	key string
	pix []byte
}

func (m *playingMemo) playing(info *npInfo, corner *screen.Corner, posMs, durMs int, at *view) []byte {
	posMs -= posMs % msPerSecond // the screen shows whole seconds; redraw once a second at most

	look := *at
	look.fade = 0 // the panel's contrast, not the frame

	if look.dimmed {
		look.orbit, look.page, posMs = 0, 0, 0 // the dim title drifts instead
	} else {
		look.drift = 0
	}

	if !waits(info) {
		look.dotStep = 0
	}

	key := fmt.Sprintf("%+v|%+v|%v|%v|%+v|%d|%d|%+v",
		*info.np, info.presets, info.pairing, info.calling, *corner, posMs, durMs, look)
	if key != m.key {
		var frame []byte

		if look.dimmed {
			dx, dy := driftAt(look.drift)
			frame = screen.Ambient(ambientTitle(info), dx, dy).Pix(fullLevel)
		} else {
			frame = orbitFrame(playingScreen(info, corner, posMs, durMs, &look).Pix(fullLevel), look.orbit)
		}

		m.pix, m.key = frame, key
	}

	return m.pix
}

// orbitStep is how long the playing screen stays put before it moves one pixel.
const orbitStep = time.Minute

// orbitPath is the playing screen's offsets, one per orbitStep: a pixel around its place
// and back. The layout stays where it is to the eye, yet no edge of a letter, the progress
// bar or a badge lights the same pixels for more than a minute at a time: the OLED's
// burn-in comes from what stays lit in one place. The dim title and the standby clock drift
// further (driftPath).
//
//nolint:gochecknoglobals // a constant table
var orbitPath = [...][2]int{{0, 0}, {1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}}

// orbitFrame moves what pix shows to the step-th place of orbitPath; what moves off the
// panel is dropped (the screens keep 1 px clear of every edge).
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

// driftPath is where the dim title and the standby clock stand, one place a minute: never
// more than 4 px from the middle, so the layout stays put to the eye while every lit pixel
// moves on.
//
//nolint:gochecknoglobals // a constant table
var driftPath = [...][2]int{{0, 0}, {-3, -3}, {3, 3}, {-4, 1}, {4, -2}, {0, -4}, {-2, 4}, {4, 1}, {1, 4}}

// driftAt is driftPath's step-th place, as x and y offsets.
func driftAt(step int) (int, int) {
	place := driftPath[(step%len(driftPath)+len(driftPath))%len(driftPath)]

	return place[0], place[1]
}

//nolint:revive // flag-parameter: hour12 is the owner's clock setting, passed through as data
func clockText(t time.Time, hour12 bool) string {
	if hour12 {
		return t.Format("3:04")
	}

	return t.Format("15:04")
}

// playingScreen picks the design's screen for what plays: one thing a screen, no source
// name over it.
func playingScreen(info *npInfo, base *screen.Corner, posMs, durMs int, moment *view) *screen.Frame {
	nowPlaying := info.np
	corner := *base // marked up for this screen; the caller's stays as it was

	switch info.channel {
	case "left":
		corner.Mark = "L"
	case "right":
		corner.Mark = "R"
	default:
		// Not half of a stereo pair: no mark.
	}

	switch nowPlaying.PlayStatus {
	case statusPaused:
		corner.State = screen.StatePaused
	case playStopped:
		corner.State = screen.StateStopped
	default:
		// Playing or buffering: nothing in the corner for it.
	}

	switch nowPlaying.Source {
	case sourceTuneIn:
		return stationScreen(info, &corner, moment.page)
	case sourceBluetooth:
		return bluetoothScreen(info, moment.dotStep)
	case sourceAux:
		return notice(screen.IconAux, "AUX", Text("External input"))
	}

	if nowPlaying.Failed {
		return notice(screen.IconWarn, Text("Playback error"), "")
	}

	title, artist := nowPlaying.Track, nowPlaying.Artist
	if artist == "" {
		artist = nowPlaying.Album
	}

	if title == "" && nowPlaying.Source == sourceMultiroom {
		// Following a group whose leader names nothing: the group, and its station if any.
		return notice(screen.IconGroup, firstOf(nowPlaying.Station, prettySource(nowPlaying.Source)), "")
	}

	if title == "" {
		title, artist = firstOf(nowPlaying.Station, prettySource(nowPlaying.Source)), ""
	}

	return screen.Song(corner, title, artist, float64(posMs)/msPerSecond, durMs/msPerSecond)
}

// firstOf is the first of texts that is not empty.
func firstOf(texts ...string) string {
	for _, text := range texts {
		if text != "" {
			return text
		}
	}

	return ""
}

// waits says the playing screen shows the waiting dots: Bluetooth waiting for a phone or
// calling one.
func waits(info *npInfo) bool {
	return info.np.Source == sourceBluetooth && info.np.Station == "" && !info.pairing
}

// waitingDots is the waiting dots' lit one, 1 to 3, at step.
func waitingDots(step int) int { return step%offlineDots + 1 }

// bluetoothScreen is Bluetooth's screen: the device that plays, or what the speaker is
// doing to get one.
func bluetoothScreen(info *npInfo, dotStep int) *screen.Frame {
	device := info.np.Station

	switch {
	case device != "":
		return notice(screen.IconBluetooth, device, "")
	case info.pairing:
		return notice(screen.IconBluetooth, Text("Pairing…"), Text("Look for %s", info.room))
	case info.calling:
		return screen.Notice(screen.IconBluetooth, Text("Connecting…"), "",
			screen.NoticeOpt{IconLevel: 0, Small: false, Dots: waitingDots(dotStep)})
	default:
		return screen.Notice(screen.IconBluetooth, Text("Waiting…"), "",
			screen.NoticeOpt{IconLevel: 0, Small: false, Dots: waitingDots(dotStep)})
	}
}

// liveLevel and bufferingLevel are the radio badge's greys.
const (
	liveLevel      = 15
	bufferingLevel = 12
)

// stationScreen is the radio's screen: the station with its preset and the live badge, and
// on its other page what the station says plays.
func stationScreen(info *npInfo, base *screen.Corner, page int) *screen.Frame {
	corner := *base
	nowPlaying := info.np
	name := firstOf(nowPlaying.Station, prettySource(nowPlaying.Source))

	if nowPlaying.Failed {
		plain := screen.Corner{
			State: "", Preset: 0, Badge: "", BadgeLevel: 0,
			Mark: corner.Mark, Battery: corner.Battery, Charging: corner.Charging,
		}

		return screen.Station(plain, name, Text("Station unavailable"), true, -1)
	}

	if corner.State != "" {
		return screen.Station(corner, name, "", false, -1) // not live while paused or stopped
	}

	corner.Preset = presetOf(info.presets, nowPlaying)
	corner.Badge, corner.BadgeLevel = Text(wordLive), liveLevel

	if nowPlaying.PlayStatus == playBuffering || nowPlaying.Unheard {
		corner.Badge, corner.BadgeLevel = Text(wordBuffering), bufferingLevel

		return screen.Station(corner, name, "", false, -1)
	}

	if !alternates(nowPlaying) {
		return screen.Station(corner, name, "", false, -1)
	}

	if page == 1 {
		return screen.RadioNow(nowPlaying.Track, nowPlaying.Artist)
	}

	return screen.Station(corner, name, "", false, 0)
}

// ambientTitle is what the dimmed panel keeps: the playing screen's title.
func ambientTitle(info *npInfo) string {
	nowPlaying := info.np

	switch nowPlaying.Source {
	case sourceTuneIn, sourceBluetooth:
		return firstOf(nowPlaying.Station, prettySource(nowPlaying.Source))
	case sourceAux:
		return "AUX"
	default:
		return firstOf(nowPlaying.Track, nowPlaying.Station, prettySource(nowPlaying.Source))
	}
}

// The links linkSwitch tells apart.
const (
	linkEthernet = "Ethernet"
	linkWifi     = "Wi-Fi"
)

// linkSwitch is what the panel says when the cable goes in or out, as stock says it
// switches between Wi-Fi and Ethernet: linkEthernet or linkWifi; nothing on the first look.
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
		return linkEthernet
	default:
		return linkWifi
	}
}

// linkScreen is the link the speaker switched to, its icon over its name.
func linkScreen(kind string) *screen.Frame {
	if kind == linkEthernet {
		return notice(screen.IconCable, Text(linkEthernet), "")
	}

	return notice(screen.IconWifi, Text(linkWifi), "")
}

// deviceChange is a Bluetooth device that came or went, and the icon that says which.
type deviceChange struct {
	icon   screen.Icon
	device string
}

// bluetoothChange is what the panel says when the Bluetooth device changes, as stock
// does: connected to a device (a check over its name), or disconnected from it while
// Bluetooth stays the source (Bluetooth struck through). Leaving Bluetooth for another
// source says nothing.
func bluetoothChange(prev, cur *speaker.NowPlaying) deviceChange {
	device := func(np *speaker.NowPlaying) string {
		if np == nil || np.Source != sourceBluetooth {
			return ""
		}

		return np.Station
	}

	was, is := device(prev), device(cur)
	switch {
	case is != "" && is != was:
		return deviceChange{icon: screen.IconCheck, device: is}
	case was != "" && is == "" && cur != nil && cur.Source == sourceBluetooth:
		return deviceChange{icon: screen.IconBluetoothOff, device: was}
	}

	return deviceChange{icon: screen.IconNone, device: ""}
}

// notice is a notice with an icon, a title and a line, nothing else.
func notice(kind screen.Icon, title, line string) *screen.Frame {
	return screen.Notice(kind, title, line, screen.NoticeOpt{IconLevel: 0, Small: false, Dots: 0})
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

// MessageScreen is any other short message, already in the panel's language, alone in the
// middle of the panel.
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
