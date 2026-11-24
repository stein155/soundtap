// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/btctl"
	"github.com/stein155/soundtap/app/internal/favorites"
	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/hw/battery"
	"github.com/stein155/soundtap/app/internal/install"
	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/nowplaying"
	"github.com/stein155/soundtap/app/internal/recent"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/spotify"
	"github.com/stein155/soundtap/app/internal/tunein"
	"github.com/stein155/soundtap/app/internal/update"
	"github.com/stein155/soundtap/app/internal/upnp"
	"github.com/stein155/soundtap/app/internal/web/firmware"
)

// Options is what New builds a Server from. The first block is required. In the groups
// below it a nil hook or a zero value means this speaker has no such thing, and the
// routes that need it say so; everything soundtapd knows before the server exists goes
// here. The audio side starts after the server, because it needs the server itself; it
// hands its hooks over through AttachAudio, AttachSpotify, AttachSoundbar and AttachUPnP.
type Options struct {
	TuneIn     *tunein.Client
	Backend    Backend
	Settings   *settings.Store
	Update     *update.Manager      // self-update: release lookup, install, restart
	NowPlaying *nowplaying.Enricher // live track/artist/cover for playing stations
	Home       string               // the state directory
	Log        *slog.Logger

	Speaker   SpeakerOptions
	Health    HealthOptions
	Multiroom MultiroomOptions
	Firmware  FirmwareOptions
	Setup     SetupOptions
	Bluetooth BluetoothOptions
	Panel     PanelOptions
	Changed   ChangeHooks
	MQTT      MQTTBridge // settings changes reconnect it, the UI shows its status
}

// SpeakerOptions is what this speaker is and reports about itself.
type SpeakerOptions struct {
	// Serial is the factory serial, "" when this board could not read one. Everything else
	// a speaker is called moves: the name is editable, the IP comes from the router, and
	// DeviceID is the Wi-Fi MAC. soundtapd reads it out of NAND.Manufacturing-Data.
	Serial string
	// Port is the API port /api/devices reports for this speaker; 0 means 80.
	Port uint16
	// Capabilities is what this speaker can do. soundtapd derives the list from its
	// hardware profile -- which knows the answers -- rather than the UI inferring them from
	// a model name, because a model union is wrong the day a new board appears.
	Capabilities []string
	// UserSources are the inputs a person may select over the LAN API.
	UserSources []string
	// NotifyBaseURL is the base URL the notification player fetches clips uploaded to
	// /api/speaker/notify from: SoundTap's own loopback address. Without it uploaded clips
	// are refused, as the player would have nowhere to fetch them.
	NotifyBaseURL string
	// Recent is the recently-played list; nil disables the feature.
	Recent *recent.Store
}

// HealthOptions is what /api/health and the support bundle report.
type HealthOptions struct {
	PresetsLoadErr func() error    // what opening presets.json found
	DaemonProblems func() []string // faults soundtapd found in itself, shown as warnings
	MuteLine       func() string   // the amp mute line's value file
	Firewall       *FirewallCheck  // a production image's inbound filter
}

// MultiroomOptions is where this speaker's multiroom group lives: the local snapserver and
// its own snapclient's id. A nil Backend answers "multiroom unavailable".
type MultiroomOptions struct {
	Backend multiroom.Backend
	LocalID string
}

// FirmwareOptions is the install lane of a flashed speaker: /api/firmware, /api/soundtapd,
// /api/reboot and U-Boot's boot count.
type FirmwareOptions struct {
	// Install lets POST /api/firmware?install=1 write NAND. Only a flashed speaker sets it:
	// off-device there is no NAND to write, and a simulated install is worse than a refusal.
	Install bool
	// Reboot serves POST /api/reboot.
	Reboot func() error
	// BootGuard is how soundtapd tells U-Boot's bootcount that this boot worked.
	BootGuard func(envCount uint32, settle func(fallback bool) error) error
	// RootRun runs the lane's NAND and environment writes through soundtap-root when
	// soundtapd runs as its own user (ADR 0066); nil runs them in soundtapd.
	RootRun firmware.RootRun
	// SoundTapd serves POST /api/soundtapd; nil leaves it off.
	SoundTapd *SoundTapdUpdate
	// Active hears true when an install begins writing to NAND and false when it
	// finishes; Progress its percentage; Failed an install that started and then failed.
	Active   func(bool)
	Progress func(int)
	Failed   func()
}

// SoundTapdUpdate is where an uploaded app binary goes; see firmware.Handler.
type SoundTapdUpdate struct {
	Run, Base, Home string
	Restart         func()
}

// SetupOptions are the network setup and reset hooks.
type SetupOptions struct {
	SetupMode    func() error                  // drop into provisioning / setup AP
	WifiProfiles func() ([]WifiProfile, error) // the Wi-Fi networks the speaker remembers
	ForgetWifi   func(id string) error         // drop one of them
	// FactoryReset performs the reset the front-panel chord performs: state, pairings,
	// Wi-Fi, and a restart.
	FactoryReset func() error
}

// BluetoothOptions reach BlueZ: the system bus and soundtapd's pairing agent.
type BluetoothOptions struct {
	Bus         btctl.Bus
	Pair, Clear func(context.Context) error
}

// PanelOptions is what the speaker's own front shows and asks.
type PanelOptions struct {
	// DisplayAvailable says whether an OLED panel is present, so the standby-screen page
	// is offered only where it does something.
	DisplayAvailable func() bool
	// Notice shows an event on the speaker's own display, in English (the display
	// translates it).
	Notice func(en string)
	// Confirm waits for a key press before a dangerous route runs.
	Confirm DeviceConfirm
}

// ChangeHooks hear what the API changed that runs elsewhere.
type ChangeHooks struct {
	Name      func(string) // restart name-advertising receivers after a rename
	Receivers func()       // an integration was switched on or off
}

// New builds a Server from o. Update performs self-updates and NowPlaying enriches
// now-playing with live track metadata; the server only maps them onto HTTP.
func New(o Options) *Server {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// dist is embedded at build time; this only fails if the build is broken.
		panic("web: embedded dist missing: " + err.Error())
	}
	srv := &Server{
		tunein:         o.TuneIn,
		backend:        o.Backend,
		settings:       o.Settings,
		update:         o.Update,
		np:             o.NowPlaying,
		log:            o.Log,
		version:        o.Update.Version(),
		homeDir:        o.Home,
		favoritesStore: favorites.Open(filepath.Join(o.Home, "favorites.json")),
		startedAt:      time.Now(),
		ui:             &gzipAssets{fs: sub, next: http.FileServer(http.FS(sub))},
		proxy:          &http.Client{Timeout: 12 * time.Second, Transport: publicOnlyTransport()},
		fw:             firmware.New(o.Log, o.Home),
	}
	srv.hub = newHub(srv.pollState, o.Log.With("comp", "events"))
	srv.household = newHousehold(srv, o.Log.With("comp", "household"))
	srv.recovery = install.LoadRecovery(o.Home, flash.ReadCmdline)
	srv.configure(o)
	return srv
}

// configure takes the groups of o over.
func (s *Server) configure(o Options) {
	s.opts = o
	sp := o.Speaker
	s.serial, s.port, s.recentStore = sp.Serial, sp.Port, sp.Recent
	s.capabilities = append([]string(nil), sp.Capabilities...)
	s.userSources = append([]string(nil), sp.UserSources...)
	s.notifyBase = strings.TrimRight(sp.NotifyBaseURL, "/")
	h := o.Health
	s.presetsLoadErr, s.daemonProblems, s.muteLine, s.firewall = h.PresetsLoadErr, h.DaemonProblems, h.MuteLine, h.Firewall
	s.multiroom, s.snapclientID = o.Multiroom.Backend, o.Multiroom.LocalID
	if s.fw != nil { // a test's bare Server has no install lane
		s.configureFirmware(o.Firmware)
	}
	st := o.Setup
	s.setupMode, s.wifiProfiles, s.forgetWifi, s.hardwareReset = st.SetupMode, st.WifiProfiles, st.ForgetWifi, st.FactoryReset
	s.btBus, s.btPair, s.btClear = o.Bluetooth.Bus, o.Bluetooth.Pair, o.Bluetooth.Clear
	s.displayAvailable, s.panelNotice, s.deviceConfirm = o.Panel.DisplayAvailable, o.Panel.Notice, o.Panel.Confirm
	s.nameChanged, s.receiversChanged = o.Changed.Name, o.Changed.Receivers
	s.mqtt = o.MQTT
}

// configureFirmware hands the install lane its hooks.
func (s *Server) configureFirmware(fw FirmwareOptions) {
	if fw.Install {
		s.fw.EnableInstall(install.LivePlan)
	}
	s.fw.SetReboot(fw.Reboot)
	s.fw.SetBootGuard(fw.BootGuard)
	if fw.RootRun != nil {
		s.fw.SetRootRun(fw.RootRun)
	}
	if u := fw.SoundTapd; u != nil {
		s.fw.EnableSoundTapdUpdate(u.Run, u.Base, u.Home, s.version, u.Restart)
	}
	s.fw.SetActiveFn(fw.Active)
	s.fw.SetProgressFn(fw.Progress)
	s.fw.SetFailedFn(fw.Failed)
}

// AudioHooks are the audio side's: the arbiter, the multiroom group and the chimes. Only a
// speaker running its audio side has them.
type AudioHooks struct {
	// SourceChanging is the synchronous hardware handoff that must finish before an
	// external receiver's session hook returns and starts writing PCM.
	SourceChanging func(string)
	// Flush drops audio already queued for the DAC. Unlike SourceChanging it must not
	// touch source ownership — see setSource. FlushWait acknowledges a stopped
	// producer's audio boundary.
	Flush     func()
	FlushWait func(context.Context) error
	// Channel applies the stored channel; Downstream is the audio delay after the bus.
	Channel    func(string)
	Downstream func() time.Duration
	// MultiroomActive switches the Mixer's copy for Snapcast.
	MultiroomActive func(bool)
	// SelectSource picks the source the arbiter grants the card to, for the one case the
	// speaker changes its own source: joining or leaving another speaker's stream.
	SelectSource func(context.Context, string) error
	// LeaderLive and LeaderNow report a follower's leader: whether it answers, and what
	// it plays.
	LeaderLive func(bool)
	LeaderNow  func(speaker.NowPlaying)
	// SelfAddress is this speaker's own address on the network, for the one case it has
	// to tell another speaker where to find it.
	SelfAddress func(context.Context) string
	// RestartSnapclient kicks the supervised snapclient so it re-reads which server it
	// follows; WantAnchor starts this speaker's group anchor and keeps it for
	// multiroom.AnchorHold.
	RestartSnapclient func()
	WantAnchor        func()
	// EventChime plays a named builtin tone for an event (BT connected/cleared, grouped).
	EventChime func(name string)
	// AmpFault reports whether the amp fault GPIO is asserted, for the health warning.
	AmpFault func() bool
	// Battery is the battery as last read, for /api/health; false on a model without one.
	Battery func() (battery.Status, bool)
}

// attached is every group handed over after New, as last attached.
type attached struct {
	Audio    AudioHooks
	Spotify  SpotifyHooks
	Soundbar SoundbarHooks
	UPnP     UPnPHooks
}

// AttachAudio hands the audio side's hooks over. Call before serving starts.
func (s *Server) AttachAudio(a AudioHooks) {
	s.attached.Audio = a
	s.sourceChanging, s.flushAudio, s.flushAudioWait = a.SourceChanging, a.Flush, a.FlushWait
	s.channel, s.downstream, s.multiroomActive = a.Channel, a.Downstream, a.MultiroomActive
	s.selectSource, s.leaderLive, s.leaderNow, s.selfAddress = a.SelectSource, a.LeaderLive, a.LeaderNow, a.SelfAddress
	s.restartSnapclient, s.wantAnchor = a.RestartSnapclient, a.WantAnchor
	s.eventChime, s.ampFaultFn, s.batteryFn = a.EventChime, a.AmpFault, a.Battery
}

// SpotifyHooks are Spotify browsing and playback through the receiver's own session, and
// the filters on what librespot claims. Without a Client every /api/spotify route answers
// spotify_not_connected.
type SpotifyHooks struct {
	Client  *spotify.Client
	Account SpotifyAccount
	// Claim filters daemon source claims: false drops the claim. Stop filters daemon stop
	// events: false keeps the source, because the daemon is being asked to start again.
	Claim func(source string) bool
	Stop  func(source string) bool
}

// AttachSpotify hands Spotify over. Call before serving starts.
func (s *Server) AttachSpotify(h SpotifyHooks) {
	s.attached.Spotify = h
	s.spotify, s.spotifyAccount = h.Client, h.Account
	s.claimSource, s.stopSource = h.Claim, h.Stop
}

// SoundbarChoice is one of a soundbar's one-of-a-few settings: Get returns "" until the
// processor has said, Set refuses a value it does not know.
type SoundbarChoice struct {
	Get func() string
	Set func(string) error
}

// SoundbarHooks reach a soundbar's processor (the SoundTouch 300).
type SoundbarHooks struct {
	// Speakers is the paired bass module and rears, nil until the processor has answered.
	Speakers func() *speaker.Soundbar
	// Tone and SetTone read and set the tone levels; Tone returns nil until the
	// processor has answered, SetTone refuses a level outside its range.
	Tone    func() *speaker.Tone
	SetTone func(bass, treble, centre, rear int) error
	// Choices are served at /api/soundbar/<name> (audio-mode, cec-mode, hdmi-input).
	Choices map[string]SoundbarChoice
	// LipSync and SetLipSync: the delay in ms the sound waits for the TV picture, 0 to
	// LipSyncMax. LipSync says false until the soundbar has told.
	LipSync    func() (int, bool)
	SetLipSync func(int) error
	LipSyncMax int
	// StartADAPTiQ starts the calibration (POST /api/soundbar/adaptiq).
	StartADAPTiQ func() error
	// PickInput routes every user source pick to the processor instead of the AUX and
	// source keys.
	PickInput func(string) error
}

// AttachSoundbar hands a soundbar's processor over. Call before serving starts.
func (s *Server) AttachSoundbar(h SoundbarHooks) {
	s.attached.Soundbar = h
	s.soundbar, s.tone, s.setTone = h.Speakers, h.Tone, h.SetTone
	s.choices = map[string]soundbarChoice{}
	for name, c := range h.Choices {
		s.choices[name] = soundbarChoice{c.Get, c.Set}
	}
	s.lipSync, s.setLipSync, s.lipSyncMax = h.LipSync, h.SetLipSync, h.LipSyncMax
	s.startCalibration, s.pickInput = h.StartADAPTiQ, h.PickInput
}

// UPnPHooks are the UPnP renderer's and the LAN's media servers.
type UPnPHooks struct {
	// Seek moves the renderer's track, for the time bar of a track a controller sent.
	Seek func(context.Context, time.Duration) error
	// Registry browses MediaServer:1 ContentDirectory devices; nil disables the routes (404).
	Registry UPnPRegistry
	// Queue is the folder a played browse result goes into.
	Queue *upnp.Queue
}

// AttachUPnP hands UPnP over. Call before serving starts.
func (s *Server) AttachUPnP(h UPnPHooks) {
	s.attached.UPnP = h
	s.upnpSeek, s.upnpReg, s.upnpQueue = h.Seek, h.Registry, h.Queue
}
