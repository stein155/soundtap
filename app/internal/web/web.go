// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package web serves the SoundTap control UI and JSON API. Playback is
// NATIVE: presets are real TUNEIN ContentItems the speaker plays itself (resolving
// via the live TuneIn service) — SoundTap never proxies audio.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/btctl"
	"github.com/stein155/soundtap/app/internal/favorites"
	"github.com/stein155/soundtap/app/internal/household"
	"github.com/stein155/soundtap/app/internal/hw/battery"
	"github.com/stein155/soundtap/app/internal/install"
	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/nowplaying"
	"github.com/stein155/soundtap/app/internal/recent"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/sleeptimer"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/spotify"
	"github.com/stein155/soundtap/app/internal/tunein"
	"github.com/stein155/soundtap/app/internal/update"
	"github.com/stein155/soundtap/app/internal/upnp"
	"github.com/stein155/soundtap/app/internal/web/apierror"
	"github.com/stein155/soundtap/app/internal/web/firmware"
)

// dist holds the built React UI (frontend/), embedded at build time. The
// contents are produced by `vite build` (see frontend/). index.html is served
// for / and the hashed assets under their own paths.
//
//go:embed all:dist
var distFS embed.FS

// tuneInBase / logoMaxBytes bound the same-origin TuneIn proxies below. TuneIn
// has no CORS and serves logos over plain http, so the browser can't reach them
// directly — SoundTap mirrors the request server-side instead.
const (
	tuneInBase   = "https://opml.radiotime.com"
	logoMaxBytes = 2 << 20 // 2 MiB
)

// Server wires the UI to the speaker and TuneIn search.
type Server struct {
	// local is the guarded route table, for a proxy request that names this speaker.
	local http.Handler
	// replays answers a write the UI sent twice for one action.
	replays replayCache
	// deviceConfirm waits for a key press before a dangerous route runs.
	deviceConfirm DeviceConfirm
	// startCalibration starts a soundbar's ADAPTiQ; nil on every other speaker.
	startCalibration func() error
	// peerGone, when set, replaces the package probe for this server only: a test's
	// stub must not be visible to a takeOverIfGone left running by another test.
	peerGone     func(Device) bool
	tunein       *tunein.Client
	backend      Backend
	scan         wifiScanCache
	settings     *settings.Store
	mdns         Hostnamer
	mdnsMu       sync.Mutex // guards mdns (set after serving starts, once the IP is known)
	mqtt         MQTTBridge
	update       *update.Manager        // self-update: release lookup, install, restart
	np           *nowplaying.Enricher   // live track/artist/cover for playing stations
	spotifyTrack atomic.Pointer[string] // the URI of what Spotify plays, from librespot's hook
	log          *slog.Logger
	version      string
	homeDir      string
	startedAt    time.Time
	ui           http.Handler                  // serves the embedded dist bundle
	proxy        *http.Client                  // for the same-origin TuneIn / logo proxies
	hub          *hub                          // pushes live state to browsers over SSE (/api/events)
	household    *household.Household          // every speaker's live state on one stream (/api/household/events)
	setupMode    func() error                  // optional on-speaker callback: drop into provisioning / setup AP
	wifiProfiles func() ([]WifiProfile, error) // optional: Wi-Fi networks the speaker remembers
	forgetWifi   func(id string) error         // optional: drop one of them
	// optional on-speaker callback: run the full reset and restart into setup
	hardwareReset    func() error
	port             uint16       // this speaker's advertised API port; 0 means 80
	nameChanged      func(string) // optional callback: restart name-advertising receivers
	receiversChanged func()       // optional callback: an integration was switched
	sourceChanging   func(string) // optional callback: mute/preempt before source ownership changes
	// one user source change at a time, so key sequences never interleave
	sourcePick      sync.Mutex
	flushAudioWait  func(context.Context) error // optional: acknowledge a stopped producer boundary
	flushAudio      func()                      // optional callback: drop audio queued for the DAC (track change)
	claimSource     func(string) bool           // optional: may a daemon's /api/source claim take the amp?
	stopSource      func(string) bool           // optional: may a daemon's stop release the amp?
	btBus           btctl.Bus                   // optional on-speaker callback: reach BlueZ on the system bus
	btPair, btClear func(context.Context) error // soundtapd's BlueZ agent
	btForget        func(ctx context.Context, address string) (bool, error)
	btPaired        func(ctx context.Context, address string) (bool, error)
	btMu            sync.Mutex // one Bluetooth command sequence at a time
	multiroom       multiroom.Backend
	snapclientID    string
	muteLine        func() string
	multiroomActive func(bool)
	channel         func(string)
	downstream      func() time.Duration
	// restartSnapclient kicks the supervised snapclient so it re-reads which server it
	// follows. nil off-device, where nothing supervises anything.
	restartSnapclient func()
	// wantAnchor starts this speaker's group anchor and keeps it for multiroom.AnchorHold;
	// nil off-device, where no anchor is parked.
	wantAnchor func()
	// eventChime plays a named builtin tone for an event (BT connected/cleared, grouped); nil off-device.
	eventChime func(name string)
	// panelNotice shows an event on the ST20 panel; nil elsewhere.
	panelNotice func(en string)
	// selectSource picks the source the arbiter grants the card to, for the one case the
	// speaker changes its own source: joining or leaving another speaker's stream.
	selectSource func(context.Context, string) error
	// optional: picks a user source another processor owns (ST300 TV)
	pickInput  func(string) error
	upnpSeek   func(context.Context, time.Duration) error // optional: moves a renderer track
	leaderLive func(bool)
	leaderNow  func(speaker.NowPlaying)
	// selfAddress is this speaker's own address on the network, for the one case it has to
	// tell another speaker where to find it. nil off-device.
	selfAddress func(context.Context) string
	// fw is the install lane: /api/firmware, /api/soundtapd, /api/reboot.
	fw *firmware.Handler
	// Spotify browsing and playback through the receiver's own session. nil off-device.
	spotify        *spotify.Client
	spotifyAccount SpotifyAccount
	spotifyImages  imageCache

	// The factory serial, when this board could read one. Everything else a speaker is
	// called moves: the name is editable, the IP comes from the router, and DeviceID is
	// the Wi-Fi MAC. soundtapd reads it out of NAND.Manufacturing-Data at startup and
	// hands it here; see cmd/soundtapd/identity.go, which owns that partition's layout.
	serial   string
	recovery install.Recovery
	// upnpReg browses UPnP MediaServer:1 ContentDirectory devices on the LAN.
	upnpReg UPnPRegistry
	// upnpQueue is the folder a browse result was played from.
	upnpQueue *upnp.Queue
	// upnpMu guards upnpGen, which counts plays: a folder lookup commits only while no
	// newer play has started.
	upnpMu  sync.Mutex
	upnpGen uint64
	// ampFaultFn reports whether the amp fault GPIO is currently asserted.
	// nil off-device (no hardware to read).
	ampFaultFn func() bool
	// batteryFn is the Portable's battery as last read; false where there is none.
	batteryFn func() (battery.Status, bool)
	// daemonProblems lists faults soundtapd found in itself (an unidentified model, a
	// failed Wi-Fi restart); /api/health shows them as warnings. nil reports none.
	daemonProblems func() []string
	// routes are the patterns Handler registered, for the API reference test.
	routes []string
	// firewall checks a production image's inbound filter; nil off-device.
	firewall *FirewallCheck
	// displayAvailable reports whether this speaker has an OLED panel. nil off-device.
	displayAvailable func() bool
	// soundbar reports the paired bass module and rears, nil unless this is a soundbar.
	// It returns nil until the soundbar's processor has answered.
	soundbar func() *speaker.Soundbar
	// tone and setTone read and set the soundbar's tone levels; nil unless a soundbar.
	tone func() *speaker.Tone
	// lipSync, setLipSync and lipSyncMax: the soundbar's lip-sync delay; nil unless a soundbar.
	lipSync    func() (int, bool)
	setLipSync func(int) error
	lipSyncMax int
	setTone    func(bass, treble, centre, rear int) error
	// choices are a soundbar's one-of-a-few settings by path name (audio-mode, cec-mode, hdmi-input);
	// empty elsewhere.
	choices     map[string]soundbarChoice
	userSources []string
	// devices is every other SoundTap speaker the discovery layer has seen; see devices.go.
	devices deviceRegistry
	// capabilities is what this speaker can do, handed over by soundtapd at startup.
	capabilities   []string
	notifyBase     string                // base URL the firmware fetches uploaded notification clips from
	audioMu        sync.Mutex            // guards audioClips and builtinClips
	audioClips     map[string]*audioClip // short-lived uploaded notification audio, keyed by URL name
	clipUpload     sync.Mutex            // one clip upload read into memory at a time
	builtinClips   map[string]*audioClip // permanent clips (no TTL); e.g. the preset-store tone
	recentStore    *recent.Store         // recently-played list; nil = feature disabled
	recentArtTried sync.Map              // recent station locations whose logo was looked up
	favoritesStore *favorites.Store      // saved favorites
	presetsLoadErr func() error          // what opening presets.json found; nil = not wired

	// sleep is the in-memory sleep timer (sleep.go); sleepUnit is one of its minutes,
	// zero meaning time.Minute, so tests can run one without waiting for it.
	sleep     sleeptimer.Timer
	sleepUnit time.Duration
	// offSlack is added to the deadlines of the ways into standby (stop, power, the
	// sleep timer); zero on the speaker. Tests set it so a loaded machine's slow fsync
	// of the recent list does not read as a speaker that would not turn off.
	offSlack time.Duration
	// clock is the wall clock the alarm reads (alarm.go); nil means time.Now.
	clock func() time.Time
	// alarmFired is the "date time" key of the last alarm that went off, so one alarm
	// minute plays its preset once however many ticks land inside it.
	alarmMu    sync.Mutex
	alarmFired string
	// alarmStop ends the running alarm's fallback (dismissAlarm).
	alarmStop context.CancelFunc
	// alarmRaised says the alarm turned the volume up from alarmRaisedFrom; dismissing
	// the alarm puts that level back.
	alarmRaised     bool
	alarmRaisedFrom int
	// sleepRetry is the pause between standby attempts of an expired sleep timer; zero
	// means sleeptimer.RetryEvery.
	sleepRetry time.Duration
	// alarmZones caches time.LoadLocation per zone name: the alarm asks every tick.
	alarmZones sync.Map // string -> *time.Location
	// standbyWatched is set once the backend reports every standby itself (WatchStandby);
	// enterStandby then leaves the follow-up to that hook instead of running it twice.
	standbyWatched atomic.Bool
	// group is this speaker's place in a multiroom group: whom it follows (multiroom_follow.go).
	group multiroom.Group
	// volumeMu makes the read-old-level/set-new-level pair in setVolume one step.
	volumeMu sync.Mutex
	// opts is what New was given and attached the groups handed over since, for a test
	// that changes them (export_test.go).
	opts     Options
	attached attached
}

// Run drives the background services the Server owns — the SSE hub's
// speaker poll loop and the alarm clock — until ctx is cancelled. Call once in a goroutine.
func (s *Server) Run(ctx context.Context) {
	safego.Go("web.alarm", func() { s.runAlarm(ctx, alarmEvery) })
	safego.Go("web.household", func() { s.household.Run(ctx) })
	s.hub.run(ctx)
}

// SetMDNS attaches the mDNS responder so settings can show the .local address and
// renames re-advertise it. Safe to call after serving starts (the responder is created
// once the LAN IP is known).
func (s *Server) SetMDNS(h Hostnamer) {
	s.mdnsMu.Lock()
	s.mdns = h
	s.mdnsMu.Unlock()
}

// Nudge pushes current state to SSE subscribers now. Used by the daemon when state
// changes outside an HTTP request (a buffering station reaching PLAY_STATE).
func (s *Server) Nudge() { s.hub.nudge() }

// StagingPath is where a downloaded app update waits for InstallApp.
func (s *Server) StagingPath() string { return s.fw.StagingPath() }

// InstallApp installs the app binary at path as the next soundtapd.
func (s *Server) InstallApp(path string) error {
	err := s.fw.InstallApp(path)
	if err != nil {
		return fmt.Errorf("install app: %w", err)
	}

	return nil
}

// ConfirmBoot tells the boot guard this boot worked.
func (s *Server) ConfirmBoot() error {
	err := s.fw.ConfirmBoot()
	if err != nil {
		return fmt.Errorf("confirm boot: %w", err)
	}

	return nil
}

// localNames returns the names that are this speaker: its own .local name and the shared
// one, and the names a home router gives it from the hostname it sends in DHCP.
// Empty until mDNS is up, so no name passes before then.
func (s *Server) localNames() []string {
	s.mdnsMu.Lock()
	if s.mdns == nil {
		s.mdnsMu.Unlock()

		return nil
	}

	own, shared := s.mdns.Hostname(), s.mdns.SharedName()
	s.mdnsMu.Unlock()

	return append([]string{own, shared}, routerNames(own, resolvConf)...)
}

// resolvConf is where udhcpc writes the lease's domain (option 15) as search/domain.
const resolvConf = "/etc/resolv.conf"

// routerNames are the speaker's bare name and that name in each domain the DHCP lease
// handed out. A rebinding page cannot pick those: they come from the home network.
// ponytail: reads resolv.conf on every request; cache it if that ever shows up in a profile.
func routerNames(own, resolv string) []string {
	base, ok := strings.CutSuffix(strings.TrimSuffix(own, "."), ".local")
	if !ok || base == "" || strings.Contains(base, ".") {
		return nil
	}

	names := []string{base}

	b, _ := os.ReadFile(resolv)
	for line := range strings.SplitSeq(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || (f[0] != "search" && f[0] != "domain") {
			continue
		}

		for _, d := range f[1:] {
			if d = strings.Trim(d, "."); d != "" && d != "local" {
				names = append(names, base+"."+d)
			}
		}
	}

	return names
}

// mdnsHost returns the advertised .local name, or "" if mDNS isn't up yet.
func (s *Server) mdnsHost() string {
	s.mdnsMu.Lock()
	defer s.mdnsMu.Unlock()

	if s.mdns == nil {
		return ""
	}

	return s.mdns.Hostname()
}

// mdnsSetName re-advertises under a new speaker name (no-op if mDNS isn't up).
func (s *Server) mdnsSetName(name string) {
	s.mdnsMu.Lock()
	defer s.mdnsMu.Unlock()

	if s.mdns != nil {
		s.mdns.SetName(name)
	}
}

// PanicLogName is the last crash's trace under the home directory.
const PanicLogName = "panic.log"

// LastBootDir holds what pstore kept of the previous boot, and how it ended.
const LastBootDir = "last-boot"

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" && r.URL.RawQuery == "" {
		marker := os.Getenv("SOUNDTAP_SETUP_AP_MARKER")
		if marker == "" {
			marker = "/run/soundtap/setup-ap"
		}

		if fileExists(marker) && !reachedOverLAN(r) {
			http.Redirect(w, r, "/?setup=wifi", http.StatusFound)

			return
		}
	}

	if strings.HasPrefix(r.URL.Path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}

	s.ui.ServeHTTP(w, r)
}

// reachedOverLAN reports whether the request arrived on an address other than the setup
// access point's: the owner already reaches the speaker over a working network (Ethernet on
// an ST20 or ST300 with no Wi-Fi saved), so the setup page is not forced on them. Without a
// known local address the request is treated as one on the access point.
func reachedOverLAN(r *http.Request) bool {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok || addr == nil {
		return false
	}

	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}

	ip := net.ParseIP(host)

	return ip != nil && !ip.IsLoopback() && host != speakerstate.SetupAPAddress
}

// updateApp maps POST /api/update onto the update manager: an optional
// {"tag": "..."} body targets a specific release; an empty body
// means "latest stable", the default Update button. Neither waits for a press: a picked
// tag may be older, but it installs only with a .sig that verifies (ADR 0048, ADR 0118).
func (s *Server) updateApp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tag     string `json:"tag"`
		Confirm bool   `json:"confirm"`
	}

	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil {
		s.badRequest(w, "bad body", err)

		return
	}

	if !body.Confirm {
		s.badRequest(w, "confirm must be true", nil)

		return
	}
	// No press, a picked older release included: the speaker installs only a release whose
	// .sig verifies (ADR 0048, ADR 0118).
	target, current, err := s.update.Start(r.Context(), body.Tag)

	var unknown update.UnknownTagError
	switch {
	case errors.Is(err, update.ErrNotUpdatable):
		apiError(w, http.StatusConflict, err.Error())
	case errors.Is(err, update.ErrBusy):
		// The error shape, plus the status the UI already reads.
		writeJSON(w, http.StatusConflict,
			map[string]string{"error": err.Error(), "code": string(apierror.Busy), "status": "updating"})
	case errors.As(err, &unknown):
		apiError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, update.ErrClockNotSet):
		apiErrorCode(w, http.StatusServiceUnavailable, apierror.ClockNotSet, update.ErrClockNotSet.Error())
	case err != nil:
		// The release list lives on GitHub: an upstream failure.
		s.log.Warn("release check failed", "err", err)
		apiError(w, http.StatusBadGateway, "release check failed")
	case current:
		writeJSON(w, http.StatusOK, map[string]string{"status": "current", keyVersion: s.version})
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "updating", "from": s.version, "to": target})
	}
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second+s.offSlack)
	defer cancel()

	err := s.enterStandby(ctx)
	if err != nil {
		s.fail(w, "stop failed", err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// The install lane's paths, for the body limits and the proxy rules.
const (
	firmwareUploadPath  = firmware.UploadPath
	soundtapdUpdatePath = firmware.AppUpdatePath
	soundtapdMaxSize    = firmware.AppMaxSize
)

// The install lane's exported calls, kept on Server for soundtapd.

// coreUpdatePath installs the newest SoundTap Core release the speaker finds.
const coreUpdatePath = "/api/core-update"

// updateCore maps POST /api/core-update onto the update manager and the install lane:
// the speaker downloads the newest Core release that is newer than the one running and
// streams it into the same install an upload gets (ADR 0048). It needs no press: the speaker
// fetches the release itself and installs it only when its .sig verifies (ADR 0048). The
// answer is the install's report; after a successful install the speaker restarts into the
// new slot on its own ("restarting": true), never after a failure.
func (s *Server) updateCore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm bool `json:"confirm"`
	}

	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil {
		s.badRequest(w, "bad body", err)

		return
	}

	if !body.Confirm {
		s.badRequest(w, "confirm must be true", nil)

		return
	}

	running, _ := osBuild()
	// The download is the speaker's own: a browser closed halfway must not cut off a
	// package that is already streaming into the spare slot.
	ctx := context.WithoutCancel(r.Context())

	err = s.fw.InstallStream(w, r.WithContext(ctx), func() (io.ReadCloser, int64, string, error) {
		pkg, openErr := s.update.OpenCore(ctx, running)
		if openErr != nil {
			return nil, 0, "", openErr //nolint:wrapcheck // its text is the 409's message
		}

		s.log.Info("core update", "from", running, "to", pkg.Tag)

		return pkg.Body, pkg.Size, pkg.Signature, nil
	})
	switch {
	case err == nil:
	case errors.Is(err, firmware.ErrInstallBusy):
		apiErrorCode(w, http.StatusConflict, apierror.Busy, err.Error())
	case errors.Is(err, update.ErrNotUpdatable):
		apiError(w, http.StatusConflict, err.Error())
	case errors.Is(err, update.ErrCoreCurrent):
		writeJSON(w, http.StatusOK, map[string]string{"status": "current", keyVersion: running})
	case errors.Is(err, update.ErrClockNotSet):
		apiErrorCode(w, http.StatusServiceUnavailable, apierror.ClockNotSet, update.ErrClockNotSet.Error())
	default:
		// The release list and the package live on GitHub: an upstream failure.
		s.log.Warn("core update download failed", "err", err)
		apiError(w, http.StatusBadGateway, "the Core release could not be downloaded")
	}
}
