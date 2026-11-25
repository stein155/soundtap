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
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/btctl"
	"github.com/stein155/soundtap/app/internal/favorites"
	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/install"
	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/nowplaying"
	"github.com/stein155/soundtap/app/internal/recent"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/tunein"
	"github.com/stein155/soundtap/app/internal/update"
	"github.com/stein155/soundtap/app/internal/web/firmware"
)

// publicProxyTimeout bounds one request the public proxy makes for the UI.
const publicProxyTimeout = 12 * time.Second

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

// New builds a Server from o. Update performs self-updates and NowPlaying enriches
// now-playing with live track metadata; the server only maps them onto HTTP.
func New(opts Options) *Server { //nolint:gocritic // hugeParam: cmd/soundtapd passes Options by value
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// dist is embedded at build time; this only fails if the build is broken.
		panic("web: embedded dist missing: " + err.Error())
	}

	srv := new(Server)
	srv.tunein, srv.backend, srv.settings, srv.update = opts.TuneIn, opts.Backend, opts.Settings, opts.Update
	srv.np, srv.log, srv.version, srv.homeDir = opts.NowPlaying, opts.Log, opts.Update.Version(), opts.Home
	srv.favoritesStore = favorites.Open(filepath.Join(opts.Home, "favorites.json"))
	srv.startedAt = time.Now()
	srv.ui = &gzipAssets{fs: sub, next: http.FileServer(http.FS(sub)), mu: sync.Mutex{}, cache: nil}
	srv.proxy = &http.Client{Timeout: publicProxyTimeout, Transport: publicOnlyTransport()}
	srv.fw = firmware.New(opts.Log, opts.Home)
	srv.hub = newHub(srv.pollState, opts.Log.With("comp", "events"))
	srv.household = newHousehold(srv, opts.Log.With("comp", "household"))
	srv.recovery = install.LoadRecovery(opts.Home, flash.ReadCmdline)
	srv.configure(&opts)

	return srv
}

// configure takes the groups of o over.
func (s *Server) configure(opts *Options) {
	s.opts = *opts
	sp := opts.Speaker
	s.serial, s.port, s.recentStore = sp.Serial, sp.Port, sp.Recent
	s.capabilities = append([]string(nil), sp.Capabilities...)
	s.userSources = append([]string(nil), sp.UserSources...)
	s.notifyBase = strings.TrimRight(sp.NotifyBaseURL, "/")
	h := opts.Health
	s.presetsLoadErr, s.daemonProblems = h.PresetsLoadErr, h.DaemonProblems
	s.muteLine, s.firewall = h.MuteLine, h.Firewall

	s.multiroom, s.snapclientID = opts.Multiroom.Backend, opts.Multiroom.LocalID
	if s.fw != nil { // a test's bare Server has no install lane
		s.configureFirmware(opts.Firmware)
	}

	st := opts.Setup
	s.setupMode, s.wifiProfiles = st.SetupMode, st.WifiProfiles
	s.forgetWifi, s.hardwareReset = st.ForgetWifi, st.FactoryReset
	s.btBus, s.btPair, s.btClear = opts.Bluetooth.Bus, opts.Bluetooth.Pair, opts.Bluetooth.Clear

	s.btForget = opts.Bluetooth.Forget
	if s.btForget == nil && s.btBus != nil {
		bus := s.btBus
		s.btForget = func(ctx context.Context, address string) (bool, error) { return btctl.Forget(ctx, bus, address) }
	}

	s.btPaired = opts.Bluetooth.Paired
	if s.btPaired == nil && s.btBus != nil {
		bus := s.btBus
		s.btPaired = func(ctx context.Context, address string) (bool, error) {
			return btctl.IsPaired(ctx, bus, address)
		}
	}

	panel := opts.Panel
	s.displayAvailable, s.panelNotice, s.deviceConfirm = panel.DisplayAvailable, panel.Notice, panel.Confirm
	s.nameChanged, s.receiversChanged = opts.Changed.Name, opts.Changed.Receivers
	s.mqtt = opts.MQTT
}

// configureFirmware hands the install lane its hooks.
func (s *Server) configureFirmware(firmwareOpts FirmwareOptions) {
	if firmwareOpts.Install {
		s.fw.EnableInstall(install.LivePlan)
	}

	s.fw.SetReboot(firmwareOpts.Reboot)
	s.fw.SetBootGuard(firmwareOpts.BootGuard)

	if firmwareOpts.RootRun != nil {
		s.fw.SetRootRun(firmwareOpts.RootRun)
	}

	if u := firmwareOpts.SoundTapd; u != nil {
		s.fw.EnableSoundTapdUpdate(u.Run, u.Base, u.Home, s.version, u.Restart)
	}

	s.fw.SetActiveFn(firmwareOpts.Active)
	s.fw.SetProgressFn(firmwareOpts.Progress)
	s.fw.SetFailedFn(firmwareOpts.Failed)
}
