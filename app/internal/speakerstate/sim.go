// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package speakerstate is the in-process SoundTouch speaker: it holds all playback
// state (volume, presets, source, zones, wake-from-standby) behind plain Go methods.
// In soundtapd it IS the speaker -- no separate process on :8090.
package speakerstate

import (
	"context"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// Preset is one stored preset slot on the simulated speaker.
type Preset struct {
	Source, Type, Location, Name, Art string
}

// member is one speaker in the simulated multiroom zone.
type member struct{ deviceID, ip string }

// Speaker is a simulated SoundTouch speaker. The zero value is not usable; use New.
// All exported fields are part of the simulated /info identity and are safe to set
// before the server starts serving. Everything else is guarded by mu.
type Speaker struct {
	DeviceID string
	Name     string
	Type     string
	Software string
	IP       string

	mu sync.Mutex // through lock/unlock only, so a change wakes Changed (changed.go)
	// lockedKey is playKey as lock found it; unlock compares against it.
	lockedKey playKey
	sigMu     sync.Mutex
	changed   chan struct{}

	// volume is the level the speaker plays at; muted silences the output without
	// touching it, so unmuting comes back to the level (and the level is what persists).
	// Everything that sets the gain reads outputLocked, not volume.
	volume int
	muted  bool

	bassTarget  int
	bassActual  int
	bassMin     int
	bassMax     int
	bassDefault int

	wifiSSID string
	wifiPSK  string
	wifiSave func(ssid, security, psk string) error

	// Live association read from wpa_supplicant, cached for wifiLinkTTL — see wifilink.go.
	wifiLinkCache wifiLink
	wifiLinkOK    bool
	wifiLinkAt    time.Time

	presets map[int]Preset
	// presetChange persists preset changes. It runs under presetMu, not mu: presetMu
	// keeps disk and memory in the same order across concurrent writes, and a flash
	// write no longer holds up StatusSnapshot (amp mute loop, LEDs, display).
	presetChange func(slot int, preset *speaker.Preset) error
	presetSwap   func(a, b int) error // persists a swap of two slots in one write
	presetMu     sync.Mutex

	// transport carries NEXT_TRACK/PREV_TRACK to whatever is actually playing. It is
	// a hook because SoundTap does not own the playlist: only a push source (today
	// Bluetooth AVRCP) can skip, and that lives in the daemon, not in this state.
	transport func(action string) error
	// transportQ runs play and pause on the source in the order they were pressed;
	// one worker, started with the first, drains it (see queueTransport).
	transportQ    chan func()
	transportOnce sync.Once
	// forwardKey hands a key to the leader while this speaker follows one, and reports
	// whether the leader took it. Nil, or false, and the key acts locally.
	forwardKey func(key string) bool

	source     string // sourceStandby when idle/off
	lastSource string // last non-standby source, restored on wake
	// spotifyItem is what the web UI asked Spotify to play, applied when librespot
	// claims the amp (SetSource).
	spotifyItem Preset
	track       string
	artist      string
	album       string
	station     string
	location    string
	art         string
	playStatus  string
	// unheard is true from selecting a buffering source until its first audio: a pause
	// in between must not make it a recently heard item.
	unheard bool

	// Where posSource's player last said it was, and when. Kept across a source change: the
	// first track's duration arrives before Spotify owns the speaker.
	posSource              string
	positionMs, durationMs int
	positionAt             time.Time

	// playGen tags the current playback attempt. runPlayer confirms (or fails) a
	// BUFFERING_STATE with the generation it started from, so a late confirmation
	// from a superseded stream cannot resurrect a station the user already left.
	playGen uint64
	// failedGen is the attempt that never played; a new attempt moves playGen past it.
	failedGen    uint64
	selectionGen uint64        // each explicit selection, even when source and URL are unchanged
	startAt      time.Duration // how far into location the selection starts (UPnP seek)
	onPlayChange func()        // fired outside the lock when UI-visible state changes
	// onSpotifyPreset starts a stored Spotify URI when its preset key is pressed.
	// librespot claims the SPOTIFY source itself once playback starts.
	onSpotifyPreset func(uri string)
	// onBluetoothSelect runs when Bluetooth becomes the source, on its own goroutine.
	onBluetoothSelect func()
	// btPaired says whether any phone is paired; it runs under the lock. nil = none.
	btPaired func() bool
	// auxName is what the person called the AUX input; nil or "" keeps "AUX".
	auxName func() string
	// wakeVolumeMax is stock's volume thresholdHigh: a wake never comes back louder
	// than this. 0 leaves the volume unbounded.
	wakeVolumeMax int
	// wakeVolumeMin is stock's volume thresholdLow: a wake never comes back quieter
	// than this. 0 leaves the volume unbounded.
	wakeVolumeMin int
	// releasedAt is when a controller last stopped its source (ReleaseSource). A wake
	// right after it is the same controller moving to the next track, not a wake.
	releasedAt time.Time
	// wakeStation names the station POWER wakes into when standby left none to
	// replay (an external source ended, or the speaker just booted).
	wakeStation func() (location, name, art string, ok bool)
	// onVolume is fired outside the lock with the new level whenever the volume
	// actually changes, so the mixer's master gain follows a key press at once
	// instead of on trackOwnership's next 50 ms tick. Every millisecond here is
	// one the user waits between pressing volume-up and hearing it. Spotify's slider
	// follows through a second hook.
	onVolume []func(int)
	// volumeSeq numbers volume changes under mu. volumeHookMu serialises their delivery
	// and volumeDelivered is the newest change delivered, so a hook that lost the race
	// to a newer change is dropped instead of applying a stale level last.
	volumeSeq       uint64
	volumeHookMu    sync.Mutex
	volumeDelivered uint64
	// onStandby is fired outside the lock whenever the speaker goes from a source into
	// standby, whatever caused it: the POWER key, an API call, auto-off, a source
	// session ending. soundtapd's web server cancels its sleep timer and releases
	// followers there, so no path to "off" can skip that.
	onStandby []func()

	// Pending AirPlay metadata: shairport can push a track bundle just before our
	// airplay-source.sh flips the source to AIRPLAY, so SetTrackMeta would drop the
	// first track. Latch it here and apply it when the source becomes AIRPLAY (F25).
	pendTrack, pendArtist, pendArt, pendAlbum string

	zoneMaster  string // master deviceID; "" when ungrouped
	zoneSenders string // master IP (senderIPAddress)
	zoneMembers []member

	lastNotification *Notification // most recent audio notification, for test assertions
	notifyUntil      time.Time
	onNotify         func(*Notification) // audio arbiter hook; nil off-hardware

	// emptyPresetUntil is set for 2 s when a preset key fires on an empty slot, so
	// the LED loop can blink amber briefly — the same visual feedback stock gives.
	emptyPresetUntil time.Time
	// transportAckUntil is set for one blink when a transport key is handled.
	transportAckUntil time.Time
}

// Notification is an audio-notification request the speaker received.
type Notification struct {
	URL    string
	Volume int
	Artist string // <service>
	Album  string // <message>
	Track  string // <reason>
}

// defaultVolume is the volume a new speaker starts at.
const defaultVolume = 30

// New returns a speaker with default playback state and no identity: DeviceID, Software
// and IP are empty until the caller sets them. soundtapd sets the identity from the
// device before it serves (newSpeaker) and IP once the network has an address (maintainIP).
func New() *Speaker {
	return &Speaker{
		Name:        "SoundTouch",
		Type:        "SoundTouch 10",
		volume:      defaultVolume,
		bassTarget:  0,
		bassActual:  0,
		bassMin:     -9,
		bassMax:     0,
		bassDefault: 0,
		wifiSSID:    "HomeWiFi",
		presets:     map[int]Preset{},
		source:      sourceStandby,
		playStatus:  stateStop,
		playGen:     1, // generation 0 is reserved for "not the current attempt"
		DeviceID:    "", Software: "", IP: "", mu: sync.Mutex{},
		lockedKey: playKey{
			bass: 0, ip: "", location: "", muted: false, playGen: 0, playStatus: "", selection: 0, source: "",
			transportAck: 0, volume: 0,
		},
		sigMu: sync.Mutex{}, changed: nil, muted: false, wifiPSK: "", wifiSave: nil,
		wifiLinkCache: wifiLink{ssid: "", wifiSignal: wifiSignal{dbm: 0, mbps: 0, mhz: 0, signal: ""}},
		wifiLinkOK:    false, wifiLinkAt: time.Time{}, presetChange: nil, presetSwap: nil,
		presetMu: sync.Mutex{}, transport: nil, transportQ: nil, transportOnce: sync.Once{}, forwardKey: nil,
		lastSource: "", spotifyItem: Preset{Art: "", Location: "", Name: "", Source: "", Type: ""}, track: "",
		artist: "", album: "", station: "", location: "", art: "", unheard: false, posSource: "",
		positionMs: 0, durationMs: 0, positionAt: time.Time{}, failedGen: 0, selectionGen: 0, startAt: 0,
		onPlayChange: nil, onSpotifyPreset: nil, onBluetoothSelect: nil, btPaired: nil, auxName: nil,
		wakeVolumeMax: 0, wakeVolumeMin: 0, releasedAt: time.Time{}, wakeStation: nil, onVolume: nil,
		volumeSeq: 0, volumeHookMu: sync.Mutex{}, volumeDelivered: 0, onStandby: nil, pendTrack: "",
		pendArtist: "", pendArt: "", pendAlbum: "", zoneMaster: "", zoneSenders: "", zoneMembers: nil,
		lastNotification: nil, notifyUntil: time.Time{}, onNotify: nil, emptyPresetUntil: time.Time{},
		transportAckUntil: time.Time{},
	}
}

// SetStartVolume is the level before any saved volume is restored. 0 keeps the built-in one.
func (s *Speaker) SetStartVolume(level int) {
	if level == 0 {
		return
	}

	s.lock()
	s.volume = level
	s.unlock()
}

// SetWakeVolumeMax bounds the volume a wake comes back at, as stock's thresholdHigh
// does. 0 leaves it unbounded.
func (s *Speaker) SetWakeVolumeMax(level int) {
	s.lock()
	s.wakeVolumeMax = level
	s.unlock()
}

// SetWakeVolumeMin is the floor to SetWakeVolumeMax: stock's thresholdLow.
// 0 leaves it unbounded.
func (s *Speaker) SetWakeVolumeMin(level int) {
	s.lock()
	s.wakeVolumeMin = level
	s.unlock()
}

// StatusSnapshot is the compact live state sidecars like leds need.
type StatusSnapshot struct {
	Source       string
	PlayStatus   string
	Standby      bool
	Volume       int // the level; the volume heard is Output
	Muted        bool
	Bass         int
	Notification bool
	// EmptyPreset is true for ~2 s after a preset key fires on an empty slot, so
	// the LED loop can show the amber-blink feedback stock gives.
	EmptyPreset bool
	// TransportAck is true for one blink after a transport key. Stock acknowledges
	// play, pause, stop, next, previous and mute with a single 250 ms blink and
	// nothing else -- see docs/hardware/wla/led-behaviour.md -- so this is a window
	// rather than an event, in the shape EmptyPreset already uses.
	TransportAck bool
	// IP is the primary LAN address, empty until the network comes up. The LED
	// loop uses it to tell "connected" from "still associating".
	IP string
}

// Output is the volume actually heard: 0 while muted.
//
//nolint:gocritic // hugeParam: callers read it on the copy StatusSnapshot returns.
func (st StatusSnapshot) Output() int {
	if st.Muted {
		return 0
	}

	return st.Volume
}

func (s *Speaker) StatusSnapshot() StatusSnapshot {
	s.lock()
	defer s.unlock()

	return StatusSnapshot{
		Source:       s.source,
		PlayStatus:   s.playStatus,
		Standby:      s.source == sourceStandby,
		Volume:       s.volume,
		Muted:        s.muted,
		Bass:         s.bassTarget,
		Notification: time.Now().Before(s.notifyUntil),
		EmptyPreset:  time.Now().Before(s.emptyPresetUntil),
		TransportAck: time.Now().Before(s.transportAckUntil),
		IP:           s.IP,
	}
}

// IsStandby reports whether the speaker is currently in standby.
func (s *Speaker) IsStandby() bool {
	s.lock()
	defer s.unlock()

	return s.source == sourceStandby
}

// OnTransport registers the handler for NEXT_TRACK/PREV_TRACK. Without one those
// keys do nothing, which is the honest answer for a source SoundTap cannot skip.
func (s *Speaker) OnTransport(fn func(action string) error) {
	s.lock()
	s.transport = fn
	s.unlock()
}

// OnWifiProfile registers a callback for newly submitted Wi-Fi credentials: the network, its
// security as CheckWifi takes it ("wep" for a WEP network) and its password.
func (s *Speaker) OnWifiProfile(fn func(ssid, security, psk string) error) {
	s.lock()
	s.wifiSave = fn
	s.unlock()
}

// Wake wakes the speaker from standby by toggling power if needed.
// Power is toggled directly.
func (s *Speaker) Wake(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	s.lock()

	if s.source == sourceStandby {
		s.powerToggleLocked(false)
	}

	s.unlock()
}

// LastNotification returns the most recent audio notification received, or nil
// if none. Used by tests to assert what SoundTap sent.
func (s *Speaker) LastNotification() *Notification {
	s.lock()
	defer s.unlock()

	return s.lastNotification
}

func (s *Speaker) hasMember(id string) bool {
	for _, m := range s.zoneMembers {
		if m.deviceID == id {
			return true
		}
	}

	return false
}

// powerToggleLocked flips standby <-> the last active source. Caller holds s.mu.
// Shared by the POWER key and Wake so both behave identically.
//
// A Spotify preset survives standby: with resume set, waking into it returns a func
// (run it after releasing s.mu) that starts the preset's URI again. Without resume —
// a wake that a source selection follows — it wakes into TuneIn as before.
func (s *Speaker) powerToggleLocked(resume bool) func() {
	s.playGen++ // any wake/standby supersedes an in-flight buffering attempt
	if s.source == sourceStandby {
		return s.wakeLocked(resume)
	}

	s.enterStandbyLocked()

	return nil
}

// wakeLocked is the standby -> active half of powerToggleLocked.
//
//nolint:revive // flag-parameter: resume as powerToggleLocked takes it.
func (s *Speaker) wakeLocked(resume bool) func() {
	s.boundWakeVolumeLocked()

	if s.lastSource == "" {
		s.lastSource = sourceTuneIn
	}

	if start := s.wakeSpotifyLocked(resume); start != nil {
		return start
	}

	if start := s.wakeBluetoothLocked(resume); start != nil {
		return start
	}

	s.source = s.lastSource
	if fn := s.wakeStation; resume && fn != nil && s.source == sourceTuneIn && s.location == "" {
		// Stock wakes into what played last; leaving TuneIn idle lit the amber
		// "no source" lamp and played nothing.
		if loc, name, art, ok := fn(); ok {
			s.location, s.station, s.art = loc, name, art
		}
	}

	if resume && s.source == sourceTuneIn && s.location == "" {
		if start := s.wakeIntoPresetLocked(); start != nil {
			return start
		}
	}

	if s.source == sourceTuneIn && s.location == "" {
		s.playStatus = stateStop
	} else {
		s.playStatus = startStatus(s.source)
	}

	return nil
}

// wakeSpotifyLocked resumes a Spotify preset on wake when it can. Otherwise it drops
// Spotify as the wake target and returns nil.
//
//nolint:revive // flag-parameter: resume as powerToggleLocked takes it.
func (s *Speaker) wakeSpotifyLocked(resume bool) func() {
	if s.lastSource != sourceSpotify {
		return nil
	}

	if fn := s.onSpotifyPreset; resume && fn != nil && s.location != "" {
		s.source, s.playStatus = sourceSpotify, stateBuffering
		uri := s.location

		return func() { safego.Go("speakerstate.spotifyPreset", func() { fn(uri) }) }
	}

	s.lastSource = sourceTuneIn
	s.location, s.station, s.art = "", "", ""

	return nil
}

// wakeBluetoothLocked wakes into Bluetooth when it was the last source and a phone is
// paired: the select hook calls the phone, and the source waits paused until it
// streams. Otherwise, or on a wake that a selection follows, it drops Bluetooth as the
// wake target and returns nil.
//
//nolint:revive // flag-parameter: resume as powerToggleLocked takes it.
func (s *Speaker) wakeBluetoothLocked(resume bool) func() {
	if s.lastSource != sourceBT {
		return nil
	}

	if fn := s.onBluetoothSelect; resume && fn != nil && s.btPaired != nil && s.btPaired() {
		s.source, s.playStatus = sourceBT, statePause

		return func() { safego.Go("speakerstate.bluetoothSelect", func() { fn() }) }
	}

	s.lastSource = sourceTuneIn
	s.location, s.station, s.art = "", "", ""

	return nil
}

// enterStandbyLocked is the active -> standby half of powerToggleLocked.
func (s *Speaker) enterStandbyLocked() {
	// Wake target: keep the source we can actually replay on our own — a tuned
	// station or AUX — or Bluetooth, which calls the phone back on wake.
	// Another external source (AirPlay/Spotify/UPnP) has no session left after
	// standby, so waking into it would land on a source that produces nothing;
	// fall back to TuneIn instead.
	if s.replayableLocked() || s.source == sourceBT || (s.source == sourceSpotify && s.location != "") {
		s.lastSource = s.source
	} else {
		s.lastSource = sourceTuneIn
		// Drop what the external source left behind: waking into TuneIn with a
		// Spotify URI and playlist name put the TuneIn logo over that name.
		s.location, s.station, s.art = "", "", ""
	}

	s.source = sourceStandby
	// What played is over; a wake starts afresh, and a station's own metadata comes
	// back with its stream.
	s.track, s.artist, s.album = "", "", ""
	// Standby is not "playing": leaving playStatus at PLAY_STATE made the Home
	// Assistant bridge publish "playing" for a speaker that is off.
	s.playStatus = stateStop
}

// controllerRestartWindow is how soon after a controller's Stop a wake still counts as
// that controller restarting playback.
const controllerRestartWindow = 5 * time.Second

// boundWakeVolumeLocked lowers a volume above wakeVolumeMax to it and raises one below
// wakeVolumeMin to that. Stock does this through BoundVol when it leaves standby, so a
// speaker left at full volume does not come back at full volume, nor one left at 3 near
// silent. Caller holds s.mu.
func (s *Speaker) boundWakeVolumeLocked() {
	// Music Assistant sends Stop and then Play for every next, previous and seek. That
	// passes through standby, and bounding it raised a volume below the floor to 10 on
	// every track change, over the level the controller had just set.
	if time.Since(s.releasedAt) < controllerRestartWindow {
		return
	}

	if s.wakeVolumeMax > 0 {
		s.volume = min(s.volume, s.wakeVolumeMax)
	}

	if s.wakeVolumeMin > 0 {
		s.volume = max(s.volume, s.wakeVolumeMin)
	}
}

// wakeIntoPresetLocked loads the lowest-numbered preset into a wake that has no station
// to replay, so a speaker that has presets but has not played anything yet wakes into
// preset 1 instead of waking silent. With no preset it changes nothing and the
// speaker wakes idle, pointing the user to the app. A Spotify preset returns the func
// that starts it, as powerToggleLocked does. Caller holds s.mu.
func (s *Speaker) wakeIntoPresetLocked() func() {
	for slot := 1; slot <= speaker.PresetSlots; slot++ {
		preset, ok := s.presets[slot]
		if !ok {
			continue
		}

		if fn := s.onSpotifyPreset; preset.Source == sourceSpotify && fn != nil {
			s.source, s.lastSource, s.playStatus = sourceSpotify, sourceSpotify, stateBuffering
			s.location, s.station, s.art = preset.Location, preset.Name, preset.Art
			uri := preset.Location

			return func() { safego.Go("speakerstate.spotifyPreset", func() { fn(uri) }) }
		}

		if preset.Source == sourceTuneIn {
			s.location, s.station, s.art = preset.Location, preset.Name, preset.Art
		}

		return nil
	}

	return nil
}
