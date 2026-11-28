// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Command soundtapd is the one daemon on a SoundTap speaker (ADR 0001): one process, one
// port (:80), no Bose application and no Bose cloud.
//
// What it does:
//   - In-process speaker state and control — speakerstate
//   - Web UI + JSON API for the browser (/api/…, /) — internal/web
//   - Radio: internal/player resolves a station and plays it through ffmpeg into the audio sink
//   - Physical buttons: buttons.go handles the keys internal/hw reads. Under the mainline
//     kernel the ST10/ST20 matrix is polled from userspace (internal/hw/keypad.go) and the
//     Wireless Link's gpio-keys-polled are read from /dev/input/event*;
//     /dev/shelby-keypad exists only under stock Linux
//   - IR remote: internal/hw decodes the raw timings of rc-core's /dev/lirc0
//     (internal/hw/ir_lirc.go); /dev/ir (stock ir.ko) exists only under stock Linux
//   - Status LEDs: internal/hw drives /sys/class/leds from speakerstate
//     (/dev/shelby-leds under stock Linux)
//   - AirPlay 2, Bluetooth, Snapcast and Spotify: supervised media daemons (ADR 0121)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/clocksync"
	"github.com/stein155/soundtap/app/internal/display"
	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/lanfilter"
	"github.com/stein155/soundtap/app/internal/lpm"
	"github.com/stein155/soundtap/app/internal/nowplaying"
	"github.com/stein155/soundtap/app/internal/osapi"
	"github.com/stein155/soundtap/app/internal/panel"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/spotify"
	"github.com/stein155/soundtap/app/internal/store"
	"github.com/stein155/soundtap/app/internal/tunein"
	"github.com/stein155/soundtap/app/internal/update"
	"github.com/stein155/soundtap/app/internal/web"
)

// version is the app release this binary is, as its tag (app-vX.Y.Z). The Core build and
// the app release both stamp it from app/VERSION; a plain `go build` says "dev".
var version = "dev"

// bcoImage is the BCO image Core ships (os/board/rootfs/post-build.sh, ADR 0031).
const bcoImage = "/usr/share/soundtap/bco-app.bcd"

// errOSAPITooOld is an app built for a newer SoundTap Core than the one it runs on.
var errOSAPITooOld = errors.New("SoundTap Core is too old for this app")

// checkOSAPI refuses an app built for a newer SoundTap Core than the one at path (ADR 0035).
// The upload is checked too, but only init's .base match keeps an override on the OS it
// was checked against; this does not depend on that. The exit is what init.d/soundtap
// acts on: three quick exits put the firmware's own soundtapd back, which always matches.
func checkOSAPI(path string) error {
	if need, have := osapi.Need(), osapi.Have(path); need > have {
		return fmt.Errorf("%w: this app needs SoundTap Core level %d, Core has level %d", errOSAPITooOld, need, have)
	}

	return nil
}

func listenPort(addr string) string {
	_, p, err := net.SplitHostPort(addr)
	if err != nil || p == "" {
		return "80"
	}

	return p
}

// bootDisarmPath is touched once the HTTP listener is up. The firmware's mainline
// boot watchdog (etc/init.d/soundtap) reboots to stock if this file never appears, so
// a failed bring-up recovers itself instead of needing a power cycle.
const bootDisarmPath = "/run/soundtap/booted"

// The source names soundtapd branches on, as they appear in the API.
const (
	sourceAirPlay   = "AIRPLAY"
	sourceAux       = "AUX"
	sourceBluetooth = "BLUETOOTH"
	sourceTV        = "TV"   // the ST300 processor's TV input
	sourceHDMI      = "HDMI" // the ST300 processor's HDMI input
	sourceMultiroom = "MULTIROOM"
	sourceSpotify   = "SPOTIFY"
	sourceStandby   = "STANDBY"
	sourceTuneIn    = "TUNEIN"
	sourceUPnP      = "UPNP"
)

// sourceProduct is an input the ST300's soundbar processor plays, as the speaker's source.
const sourceProduct = lpm.SourceProduct

// The play statuses soundtapd branches on, as they appear in the API.
const (
	playPlaying   = "PLAY_STATE"
	playBuffering = "BUFFERING_STATE"
	statusPaused  = "PAUSE_STATE"
)

// defaultListenAddr is where soundtapd listens unless -listen says otherwise (ADR 0001).
const defaultListenAddr = ":80"

// sharedHostName is the name every speaker competes for, so a person can type one
// address and reach whichever speaker is switched on. See internal/mdns (EnableSharedName).
const sharedHostName = "soundtap"

// memLimit is soundtapd's soft Go memory limit. Without one the runtime hands freed pages back
// to the kernel slowly, and a firmware upload at USB speed grew soundtapd from ~31 MB to 59 MB
// RSS until the OOM killer took it on the ST20. GOMEMLIMIT overrides it.
const memLimit = 40 << 20

func main() {
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(memLimit)
	}

	log := setupLogging()
	serial, serialErr := deviceSerial()

	defaultName, defaultDeviceID := identityDefaults(&board, serial)
	if serialErr != nil {
		log.Warn("serial unavailable; using default speaker name", "err", serialErr, "name", defaultName)
	}

	opt := defineOptions(defaultName, defaultDeviceID)

	flag.Parse()

	code := run(*opt, serial, log)
	if code != 0 {
		os.Exit(code)
	}
}

// exitDevOnSpeaker is the exit code of -dev on a flashed speaker.
const exitDevOnSpeaker = 2

// run is soundtapd after its command line: the one-shot answers init asks for, the checks
// before the daemon starts, and then the daemon. It returns the process's exit code.
func run(opt options, serial string, log *slog.Logger) int {
	setHome(opt.home)

	if opt.dev && !enterDevMode(&opt, log) {
		return exitDevOnSpeaker
	}

	useRootHelper(log)

	if opt.printOSAPI {
		fmt.Println(osapi.Need())

		return 0
	}
	// The init script starts snapclient before soundtapd, and snapclient's --hostID used
	// to be built from a MAC of its own -- a second identifier for one box, chosen from a
	// different interface than the device id, and unrelated to the serial the API reports.
	// Groups are lists of device ids, so the audio layer has to use the same one. Reading
	// the manufacturing partition a second time in shell would be a second parser for one
	// layout; asking the binary that already owns it is one line.
	// It answers before anything that keeps state: init asks as root, before soundtapd
	// starts, and the boot records and start count it moved and wrote then were root's,
	// out of soundtapd's reach, and gone before the daemon itself could log them.
	if opt.printIdentity {
		fmt.Println(speakerIdentity(serial, opt.deviceID))

		return 0
	}

	panicLog = filepath.Join(opt.home, web.PanicLogName)

	defer safego.Recover("main")

	how, moved, err := keepLastBoot(opt.home)
	if err != nil {
		log.Warn("previous boot records", "err", err)
	} else if moved {
		log.Info("previous boot ended", "how", how)
	}

	countSoundtapdStart(log)

	if !osAPIMatches(log) {
		return 1
	}

	refuseUnknownHardware(boardErr, &opt, log)
	prepareHome(opt.home, log)
	runDaemon(opt, serial, log)

	return 0
}

// runDaemon is the daemon itself: it wires every part and serves until shutdown.
func runDaemon(opt options, serial string, log *slog.Logger) {
	presetsPath := filepath.Join(opt.home, "presets.json")

	ctx, cancel := context.WithCancel(context.Background())

	life := &lifecycle{
		cancel: cancel, log: log,
		once: sync.Once{}, fadeOut: atomic.Pointer[func()]{}, savePrefs: atomic.Pointer[func()]{},
	}
	defer life.shutdown()

	stopSignals := life.shutdownOnSignal(ctx)
	defer stopSignals()

	set := settings.Open(presetsPath + ".settings")
	leftovers := stockTakeover(opt.home, log)
	identity := initialName(set, serial, opt.name, leftovers.name, log)
	// Published before anything reads it: bt.sh and the Bluetooth agent, started below,
	// otherwise find only the setup name and advertise that for the first seconds.
	err := publishCurrentName(identity)
	if err != nil {
		log.Warn("publish name", "err", err)
	}

	speakerState := newSpeaker(opt, identity, log)
	tuneinClient := tunein.New()
	presets, stockLookups := openPresets(ctx, speakerState, presetsPath, leftovers.presets, log)
	upd := update.New(version, log.With("comp", "update"))

	port, advertisedPort := advertisedPorts(opt)
	enr := nowplaying.New(speakerState, tuneinClient)
	recents := recentStations(speakerState, opt.home)
	spot := spotify.New()
	disp := display.New(ctx, displayPath(), func(context.Context) bool { return speakerState.IsStandby() },
		log.With("comp", "display"))
	leftovers.finishWithClock(set, disp, log)

	gate := newBootGate()
	wifiConf := filepath.Join(filepath.Dir(opt.home), "wifi.conf")
	changes, nameUpdates, receiverUpdates := changeNotifiers()
	mqttBridge := newMQTT(speakerState, set, upd, enr, log)
	firewall := web.NewFirewallCheck(web.ProfileFile, nil, log.With("comp", "firewall"))
	webSrv := web.New(web.Options{
		TuneIn: tuneinClient, Backend: speakerState, Settings: set, Update: upd, NowPlaying: enr,
		Home: opt.home, Log: log,
		Speaker: speakerOptions(serial, advertisedPort, port, recents),
		Health: web.HealthOptions{
			PresetsLoadErr: presets.LoadErr, DaemonProblems: problems.list, MuteLine: amplifier.MuteLine,
			Firewall: firewall,
		},
		Multiroom: multiroomOptions(),
		Firmware:  firmwareOptions(life, disp, opt.home, func() { quietForInstall(speakerState) }),
		Setup:     setupOptions(life, opt.home, wifiConf, log, stockLookups, set, presets),
		Bluetooth: bluetoothOptions(ctx, log),
		Panel:     panelOptions(disp, log),
		Changed:   changes,
		MQTT:      mqttBridge,
	})
	wireWeb(webSrv, speakerState)
	wireTransport(ctx, speakerState, webSrv, spot, disp, log)
	startBootScreen(ctx, disp, wifiConf, log)
	startFirmwareLane(ctx, webSrv, upd, gate, opt.home, log)
	// Before the Wi-Fi manager reads wifi.conf: on the first boot after an install from
	// Bose's software it joins stock's network instead of opening the setup AP.
	adoptStockWifi(opt.home, wifiConf, log)
	startWifi(ctx, wifiConf, log)

	// One mux, one port. JSON /api/* plus embedded UI only: no legacy SoundTouch XML.
	mux := http.NewServeMux()
	mux.Handle("/", webSrv.Handler())

	startWebServices(ctx, webSrv, speakerState, mqttBridge, identity, advertisedPort, opt, log)
	goSafe("firewallCheck", func() { firewall.Status(ctx) })
	// IPv6 addresses come without a DHCP hook to tell the filter (ADR 0134).
	if lanfilter.Wanted(web.ProfileFile) {
		goSafe("lanFilter6", func() { lanfilter.New(log.With("comp", "firewall")).Run(ctx, lanfilter.Interval) })
	}

	startPprof(ctx, log)

	pump := startAudioSide(ctx, opt, &audioWiring{
		sp: speakerState, web: webSrv, spot: spot, set: set, st: presets, lookups: stockLookups, tc: tuneinClient,
		disp: disp, mux: mux, home: opt.home, port: port, identity: identity, nameUpdates: nameUpdates,
		receiverUpdates: receiverUpdates,
		shutdown:        life.shutdown, fadeOut: &life.fadeOut, savePrefs: &life.savePrefs, log: log,
		chime: nil, eventChime: nil, // wireChimes sets them
	})
	serveUntilDone(ctx, &serveTarget{opt: opt, mux: mux, webSrv: webSrv, port: port, pump: pump, gate: gate}, log)
}

// httpShutdownGrace is how long the listeners get to finish their requests at shutdown.
const httpShutdownGrace = 3 * time.Second

// serveTarget is what serveUntilDone serves and watches.
type serveTarget struct {
	opt    options
	mux    *http.ServeMux
	webSrv *web.Server
	port   string // the API's port, as text for URLs
	pump   audioHealth
	gate   *bootGate
}

// serveUntilDone opens the listeners, starts the boot confirmation and watchdog, and
// blocks until shutdown.
func serveUntilDone(ctx context.Context, target *serveTarget, log *slog.Logger) {
	opt, port, gate := target.opt, target.port, target.gate
	servers := listen(ctx, opt, target.mux, log)
	log.Info("soundtapd up",
		"listen", listenAddrs(opt.listen), "primary_port", port, "device", opt.deviceID, "home", opt.home)
	// Also on a -no-audio boot: a speaker that answers is one that can be updated again,
	// and an unconfirmed boot is one U-Boot rolls back (ADR 0010).
	checks := gate.checks("http://127.0.0.1:" + port + "/api/version")

	if !devMode.Load() { // a computer has no U-Boot environment to confirm a boot in
		goSafe("confirmBoot", func() { confirmBoot(ctx, target.webSrv, gate, checks, log.With("comp", "boot")) })
	}

	if onFirmware() {
		healthy := speakerHealthy("http://127.0.0.1:"+port+"/api/version", target.pump)

		goSafe("watchdog", func() { hw.RunWatchdog(ctx, healthy, log.With("comp", "watchdog")) })
	}

	<-ctx.Done()

	// ctx is over by now; the listeners still get their grace.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), httpShutdownGrace)
	defer cancel()

	for _, server := range servers {
		_ = server.Shutdown(sctx)
	}
}

// finishWithClock takes stock's clock over on a speaker with a panel, the last part of the
// takeover, and then records that the takeover is behind this home.
func (s stockLeftovers) finishWithClock(set *settings.Store, disp *display.Manager, log *slog.Logger) {
	takeStockClock(set, s.clockFor(disp), log)
	s.finish(log)
}

// clockFor is stock's clock for a speaker with disp: nil without a panel, which shows no clock.
func (s stockLeftovers) clockFor(disp *display.Manager) func() (settings.Display, bool) {
	if !disp.Available() {
		return nil
	}

	return s.clock
}

// osAPIMatches reports whether a flashed speaker's OS is new enough for this soundtapd.
func osAPIMatches(log *slog.Logger) bool {
	if !onFirmware() {
		return true
	}

	err := checkOSAPI(osapi.Path)
	if err != nil {
		log.Error("refusing to start", "err", err)

		return false
	}

	return true
}

// defaultAdvertisedPort is the API's port when the listen address names none (ADR 0001).
const defaultAdvertisedPort = 80

// advertisedPorts returns the port the API listens on (as text, for URLs) and the one
// advertised to peers (mDNS, UPnP, notify base URL): :80 on every model (ADR 0001).
func advertisedPorts(opt options) (string, uint16) {
	port := listenPort(board.ListenAddr)
	if opt.listen != "" {
		port = listenPort(strings.Split(opt.listen, ",")[0])
	}
	// Peers proxy to this port. The ST20 advertised 80, which on its address is the BCO's
	// web server, so every request a peer forwarded to it came back 404.
	advertisedPort := uint16(defaultAdvertisedPort)

	parsed, err := strconv.ParseUint(port, 10, 16)
	if err == nil {
		advertisedPort = uint16(parsed)
	}

	return port, advertisedPort
}

// updateFailedFor is how long the panel says an install failed.
const updateFailedFor = 10 * time.Second

// startFirmwareLane starts what only a flashed speaker runs beside its routes: app updates
// through the server, the BCO's LAN watch and its flash.
func startFirmwareLane(
	ctx context.Context, webSrv *web.Server, upd *update.Manager, gate *bootGate, home string, log *slog.Logger,
) {
	if onFirmware() {
		wireFirmwareLane(ctx, webSrv, upd, home, log)
	}

	if tty := os.Getenv("SOUNDTAP_BCO_TTY"); onFirmware() && tty != "" {
		// The boot confirmation waits for this step and the BCO link (ADR 0010).
		gate.waitForBCO()
		wireBCOFlash(ctx, tty, filepath.Join(filepath.Dir(home), "wifi.conf"), gate, log)
	}
}

// installQuiet is how long an install waits after standby before it starts: the source's
// fade and the amp's mute on silence (about a second, ampMuteDelay) are over by then.
const installQuiet = 1500 * time.Millisecond

// quietForInstall puts a playing speaker in standby before an install, as stock stops
// playback for an update. Receiving, hashing and writing the package left the ST20's DAC
// short of samples while radio played on: an underrun mid-music, heard as a pop
// (2026-11-22). Standby fades the music out and mutes the amp on silence first.
func quietForInstall(playing interface {
	IsStandby() bool
	EnterStandby(ctx context.Context) error
},
) {
	if playing.IsStandby() {
		return
	}

	_ = playing.EnterStandby(context.Background())

	time.Sleep(installQuiet)
}

// firmwareOptions is the install lane: only a flashed speaker installs, reboots and
// updates its app; every speaker shows an install on its lamps and panel. quiet runs as an
// install starts, before its work.
func firmwareOptions(life *lifecycle, disp *display.Manager, home string, quiet func()) web.FirmwareOptions {
	opts := web.FirmwareOptions{
		// firmwareLaneOptions fills these in on a flashed speaker.
		Install: false, Reboot: nil, BootGuard: nil, RootRun: nil, SoundTapd: nil,
		Active: func(active bool) {
			if active && !firmwareActive.Load() {
				quiet()
			}

			firmwareActive.Store(active)
		},
		Progress: func(percent int) { firmwareProgress.Store(int64(percent)) },
		// As stock, the panel says so when an install fails; the reason is in the web UI.
		Failed: func() {
			disp.SetTransient(display.Content{Frame: panel.Say("Update failed"), Fade: 0}, updateFailedFor)
		},
	}
	if onFirmware() {
		firmwareLaneOptions(&opts, life.audioOff, home)
	}

	return opts
}

// panelOptions is what the speaker's own front shows and asks: dangerous routes wait for a
// key press on the speaker, or the root-only token.
func panelOptions(disp *display.Manager, log *slog.Logger) web.PanelOptions {
	opts := web.PanelOptions{
		DisplayAvailable: disp.Available,
		Notice: func(en string) {
			disp.SetTransient(display.Content{Frame: panel.Say(en), Fade: 0}, panel.NoticeFor)
		},
		Confirm: nil, // a flashed speaker asks on its front, below
	}

	if onFirmware() {
		confirmer.log = log.With("comp", "confirm")
		confirmer.show = func(text string, d time.Duration) {
			disp.SetTransient(display.Content{Frame: panel.Say(text), Fade: 0}, d)
		}
		confirmer.rotate()
		opts.Confirm = confirmer.confirm
	}

	return opts
}

// bluetoothOptions reach BlueZ. Only a speaker has BlueZ and bt.sh. Off-speaker the
// endpoints stay honest about there being no Bluetooth rather than shelling out to nothing.
func bluetoothOptions(ctx context.Context, log *slog.Logger) web.BluetoothOptions {
	opts := web.BluetoothOptions{Bus: nil, Pair: nil, Clear: nil, Forget: nil, Paired: nil}
	if !onFirmware() {
		return opts
	}

	opts.Bus = bluetoothBus
	if agent := bluetoothAgent(ctx, log); agent != nil {
		opts.Pair, opts.Clear = agent.Pair, func(ctx context.Context) error {
			_, err := agent.Clear(ctx)
			if err != nil {
				return fmt.Errorf("clear the Bluetooth pairings: %w", err)
			}

			return nil
		}
	}

	return opts
}

// onFirmware is set by init.d/soundtap: this soundtapd runs on a flashed speaker, with
// its NAND slots, mtd-utils, Bluetooth tools and boot watchdog.
func onFirmware() bool { return os.Getenv("SOUNDTAP_FIRMWARE") == "1" }

func setupLogging() *slog.Logger {
	log := slog.New(slog.NewTextHandler(os.Stderr, logOpts))
	// One format for every package, including those that log through slog.Default
	// (settings), and the level from SOUNDTAP_LOG_LEVEL (debug, info, warn, error).
	slog.SetDefault(log)

	err := logLevel.UnmarshalText([]byte(os.Getenv("SOUNDTAP_LOG_LEVEL")))
	if err != nil && os.Getenv("SOUNDTAP_LOG_LEVEL") != "" {
		log.Warn("SOUNDTAP_LOG_LEVEL ignored", "err", err)
	}

	return log
}

// options are soundtapd's command line.
type options struct {
	listen, home, deviceID, name string
	noAudio, dev                 bool
	printIdentity, printOSAPI    bool
}

// defineOptions defines soundtapd's flags; the options hold their values once main has
// parsed the command line.
func defineOptions(defaultName, defaultDeviceID string) *options {
	var opt options
	flag.StringVar(&opt.listen, "listen", "", "comma-separated listen addrs; empty = :80")
	flag.StringVar(&opt.home, "home", defaultHome,
		"writable state dir; its parent holds the key maps and stock's files")
	flag.StringVar(&opt.deviceID, "device-id", defaultDeviceID, "speaker device id")
	flag.StringVar(&opt.name, "name", defaultName, "speaker name")
	flag.BoolVar(&opt.noAudio, "no-audio", false, "skip audio/buttons/leds (for testing)")
	flag.BoolVar(&opt.dev, "dev", false,
		"development run on a computer: -no-audio, no hardware, nothing announced on the LAN (make dev)")
	flag.BoolVar(&opt.printIdentity, "print-identity", false,
		"print this speaker's stable identity and exit")
	flag.BoolVar(&opt.printOSAPI, "os-api", false,
		"print the SoundTap Core level this build needs and exit (ADR 0035)")

	return &opt
}

// homeDirMode is the mode of soundtapd's state directory and of its runtime directory.
const homeDirMode = 0o755

// wifiRestartDelay lets the setup client receive the HTTP success response before the
// Wi-Fi manager tears down the AP to join the selected network.
const wifiRestartDelay = 2 * time.Second

func prepareHome(home string, log *slog.Logger) {
	err := os.MkdirAll(home, homeDirMode)
	if err != nil {
		log.Warn("mkdir home", "err", err)
	}

	err = removeStalePlugins(home)
	if err != nil {
		log.Warn("remove stale plugins", "err", err)
	}

	err = removeRetiredFiles(home)
	if err != nil {
		log.Warn("remove retired files", "err", err)
	}

	cleanAtomicTemps(home, log)

	if devMode.Load() {
		return // no boot watchdog to disarm on a computer
	}

	err = os.MkdirAll(filepath.Dir(bootDisarmPath), homeDirMode)
	if err != nil {
		log.Warn("mkdir run dir", "err", err) // the disarm write after listening says so too
	}
}

// newSpeaker is the in-process speaker: speakerstate holds all playback state.
func newSpeaker(opt options, identity string, log *slog.Logger) *speakerstate.Speaker {
	state := speakerstate.New()
	state.DeviceID = opt.deviceID
	state.Name = identity
	state.Type = board.Type // product name per model profile (ST10/ST20)
	state.Software = strings.TrimPrefix(version, "app-v")
	state.SetStartVolume(board.DefaultVolume)
	state.SetWakeVolumeMax(board.WakeVolumeMax)
	state.SetWakeVolumeMin(board.WakeVolumeMin)

	if board.NoBass {
		state.NoBass()
	}

	state.OnWifiProfile(func(ssid, security, psk string) error {
		err := persistWifiProfile(filepath.Join(filepath.Dir(opt.home), "wifi.conf"), ssid, security, psk, log)
		if err != nil {
			return err
		}

		restartWifiAfter(wifiRestartDelay, log)

		return nil
	})

	return state
}

// openPresets is loadPresets, then the lookups of stock's Radio Browser presets beside the
// presets file, once those are on the speaker. A factory reset closes the lookups.
func openPresets(
	ctx context.Context, state *speakerstate.Speaker, path string, stockPresets func() stockPresetSet,
	log *slog.Logger,
) (*store.Store, io.Closer) {
	presets := loadPresets(ctx, state, path, stockPresets, log)
	lookups := startStockPresetLookups(ctx, filepath.Dir(path), state, log)

	return presets, lookups
}

// loadPresets opens the preset store and puts its presets on the speaker.
func loadPresets(
	ctx context.Context, state *speakerstate.Speaker, path string, stockPresets func() stockPresetSet,
	log *slog.Logger,
) *store.Store {
	// On the first start after an install from Bose's software (stockPresets is set then), no
	// file at all means no preset was ever stored here, so stock's presets are kept. Emptied
	// presets leave a file. Stock's Radio Browser presets are written down beside it, for
	// lookupStockPresets to store once the network answers.
	_, statErr := os.Stat(path)
	// An unreadable file leaves the store empty and read-only, so no preset write can
	// replace the owner's presets with this empty set; a corrupt one was moved aside.
	presets, err := store.Open(path)
	atomicjson.LogLoad(log, "presets", path, err)

	if stockPresets != nil && statErr == nil {
		log.Info("stock presets not taken over: this speaker has presets of its own", "path", path)
	}

	if stockPresets != nil && errors.Is(statErr, fs.ErrNotExist) {
		stock := stockPresets()
		for _, preset := range stock.taken {
			err := presets.Set(preset)
			if err != nil {
				log.Warn("preset from stock not kept", "slot", preset.Slot, "err", err)
			}
		}

		recordPendingStockPresets(filepath.Join(filepath.Dir(path), stockPendingName), stock.pending,
			trustedNow(time.Now(), clocksync.Synced), log)
	}

	for _, preset := range presets.All() {
		source, itemType, loc := preset.Source, preset.Type, preset.Location
		if source == "" {
			source, itemType, loc = sourceTuneIn, "stationurl", speaker.StationLocation(preset.StationID)
		}

		err := state.StorePreset(ctx, preset.Slot, source, itemType, loc, preset.Name, preset.Logo)
		if err != nil {
			log.Warn("stored preset not loaded", "slot", preset.Slot, "err", err)
		}
	}

	state.OnPresetChange(persistPresetChange(presets))
	state.OnPresetSwap(presets.Swap)

	return presets
}

func persistPresetChange(presets *store.Store) func(int, *speaker.Preset) error {
	return func(slot int, preset *speaker.Preset) error {
		if preset == nil {
			return presets.Remove(slot)
		}

		return presets.Set(store.Preset{
			Slot: slot, StationID: preset.StationID, Source: preset.Source, Type: preset.Type,
			Location: preset.Location, Name: preset.Name, Logo: preset.Logo,
		})
	}
}
