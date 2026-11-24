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
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/display"
	"github.com/stein155/soundtap/app/internal/hw"
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
var bcoImage = "/usr/share/soundtap/bco-app.bcd"

// checkOSAPI refuses an app built for a newer SoundTap Core than the one at path (ADR 0035).
// The upload is checked too, but only init's .base match keeps an override on the OS it
// was checked against; this does not depend on that. The exit is what init.d/soundtap
// acts on: three quick exits put the firmware's own soundtapd back, which always matches.
func checkOSAPI(path string) error {
	if need, have := osapi.Need(), osapi.Have(path); need > have {
		return fmt.Errorf("this app needs SoundTap Core level %d, Core has level %d", need, have)
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
	defaultName, defaultDeviceID := identityDefaults(board, serial)
	if serialErr != nil {
		log.Warn("serial unavailable; using default speaker name", "err", serialErr, "name", defaultName)
	}
	opt := parseOptions(defaultName, defaultDeviceID)
	setHome(opt.home)
	if opt.dev {
		enterDevMode(&opt, log)
	}
	useRootHelper(log)
	if opt.printOSAPI {
		fmt.Println(osapi.Need())
		return
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
		return
	}
	panicLog = filepath.Join(opt.home, web.PanicLogName)
	defer safego.Recover("main")
	if how, moved, err := keepLastBoot(opt.home); err != nil {
		log.Warn("previous boot records", "err", err)
	} else if moved {
		log.Info("previous boot ended", "how", how)
	}
	countSoundtapdStart(log)

	requireOSAPI(log)
	refuseUnknownHardware(boardErr, &opt, log)
	prepareHome(opt.home, log)
	presetsPath := filepath.Join(opt.home, "presets.json")

	ctx, cancel := context.WithCancel(context.Background())
	life := &lifecycle{cancel: cancel, log: log}
	defer life.shutdown()
	stopSignals := life.shutdownOnSignal(ctx)
	defer stopSignals()

	set := settings.Open(presetsPath + ".settings")
	fromStock := takeStockOnce(opt.home, log)
	identity := initialName(set, serial, opt.name, fromStock, log)
	// Published before anything reads it: bt.sh and the Bluetooth agent, started below,
	// otherwise find only the setup name and advertise that for the first seconds.
	if err := publishCurrentName(identity); err != nil {
		log.Warn("publish name", "err", err)
	}
	sp := newSpeaker(opt, identity, log)
	tc := tunein.New()
	st := loadPresets(ctx, sp, presetsPath, fromStock, log)
	upd := update.New(version, log.With("comp", "update"))

	port, advertisedPort := advertisedPorts(opt)
	enr := nowplaying.New(sp, tc)
	recents := recentStations(sp, opt.home)
	spot := spotify.New()
	disp := display.New(ctx, displayPath(), func(context.Context) bool { return sp.IsStandby() }, log.With("comp", "display"))
	takeStockClock(set, fromStock, disp.Available(), log)
	gate := newBootGate()
	wifiConf := filepath.Join(filepath.Dir(opt.home), "wifi.conf")
	changes, nameUpdates, receiverUpdates := changeNotifiers()
	mb := newMQTT(sp, set, upd, enr, log)
	fw := web.NewFirewallCheck(web.ProfileFile, nil, log.With("comp", "firewall"))
	webSrv := web.New(web.Options{
		TuneIn: tc, Backend: sp, Settings: set, Update: upd, NowPlaying: enr, Home: opt.home, Log: log,
		Speaker: speakerOptions(serial, advertisedPort, port, recents),
		Health: web.HealthOptions{
			PresetsLoadErr: st.LoadErr, DaemonProblems: problems.list, MuteLine: amplifier.MuteLine, Firewall: fw,
		},
		Multiroom: multiroomOptions(),
		Firmware:  firmwareOptions(life, disp, opt.home, func() { quietForInstall(sp) }),
		Setup:     setupOptions(life, set, st, opt.home, wifiConf, log),
		Bluetooth: bluetoothOptions(ctx, log),
		Panel:     panelOptions(disp, log),
		Changed:   changes,
		MQTT:      mb,
	})
	wireWeb(webSrv, sp)
	wireTransport(ctx, sp, webSrv, spot, disp, log)
	startBootScreen(ctx, disp, set, log)
	startFirmwareLane(ctx, webSrv, upd, gate, opt.home, log)
	// Before the Wi-Fi manager reads wifi.conf: on the first boot after an install from
	// Bose's software it joins stock's network instead of opening the setup AP.
	adoptStockWifi(opt.home, wifiConf, log)
	startWifi(ctx, wifiConf, log)

	// One mux, one port. JSON /api/* plus embedded UI only: no legacy SoundTouch XML.
	mux := http.NewServeMux()
	mux.Handle("/", webSrv.Handler())

	startWebServices(ctx, webSrv, sp, mb, identity, advertisedPort, opt, log)
	goSafe("firewallCheck", func() { fw.Status(ctx) })
	startPprof(ctx, log)

	pump := startAudioSide(ctx, opt, audioWiring{
		sp: sp, web: webSrv, spot: spot, set: set, st: st, tc: tc, disp: disp, mux: mux,
		home: opt.home, port: port, identity: identity, nameUpdates: nameUpdates,
		receiverUpdates: receiverUpdates,
		shutdown:        life.shutdown, fadeOut: &life.fadeOut, savePrefs: &life.savePrefs, log: log,
	})
	serveUntilDone(ctx, opt, mux, webSrv, port, pump, gate, log)
}

// serveUntilDone opens the listeners, starts the boot confirmation and watchdog, and
// blocks until shutdown.
func serveUntilDone(ctx context.Context, opt options, mux *http.ServeMux, webSrv *web.Server, port string, pump audioHealth, gate *bootGate, log *slog.Logger) {
	servers := listen(opt, mux, log)
	log.Info("soundtapd up", "listen", listenAddrs(opt.listen), "primary_port", port, "device", opt.deviceID, "home", opt.home)
	// Also on a -no-audio boot: a speaker that answers is one that can be updated again,
	// and an unconfirmed boot is one U-Boot rolls back (ADR 0010).
	checks := gate.checks("http://127.0.0.1:" + port + "/api/version")
	if !devMode.Load() { // a computer has no U-Boot environment to confirm a boot in
		goSafe("confirmBoot", func() { confirmBoot(ctx, webSrv, gate, checks, log.With("comp", "boot")) })
	}
	if onFirmware() {
		healthy := speakerHealthy("http://127.0.0.1:"+port+"/api/version", pump)
		goSafe("watchdog", func() { hw.RunWatchdog(ctx, healthy, log.With("comp", "watchdog")) })
	}
	<-ctx.Done()
	sctx, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	for _, s := range servers {
		_ = s.Shutdown(sctx)
	}
}

// requireOSAPI exits when a flashed speaker's OS is too old for this soundtapd.
func requireOSAPI(log *slog.Logger) {
	if !onFirmware() {
		return
	}
	if err := checkOSAPI(osapi.Path); err != nil {
		log.Error("refusing to start", "err", err)
		os.Exit(1)
	}
}

// advertisedPorts returns the port the API listens on (as text, for URLs) and the one
// advertised to peers (mDNS, UPnP, notify base URL): :80 on every model (ADR 0001).
func advertisedPorts(opt options) (string, uint16) {
	port := listenPort(board.ListenAddr)
	if opt.listen != "" {
		port = listenPort(strings.Split(opt.listen, ",")[0])
	}
	// Peers proxy to this port. The ST20 advertised 80, which on its address is the BCO's
	// web server, so every request a peer forwarded to it came back 404.
	advertisedPort := uint16(80)
	if n, err := strconv.ParseUint(port, 10, 16); err == nil {
		advertisedPort = uint16(n)
	}
	return port, advertisedPort
}

// updateFailedFor is how long the panel says an install failed.
const updateFailedFor = 10 * time.Second

// startFirmwareLane starts what only a flashed speaker runs beside its routes: app updates
// through the server, the BCO's LAN watch and its flash.
func startFirmwareLane(ctx context.Context, webSrv *web.Server, upd *update.Manager, gate *bootGate, home string, log *slog.Logger) {
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
func quietForInstall(sp interface {
	IsStandby() bool
	EnterStandby(context.Context) error
}) {
	if sp.IsStandby() {
		return
	}
	_ = sp.EnterStandby(context.Background())
	time.Sleep(installQuiet)
}

// firmwareOptions is the install lane: only a flashed speaker installs, reboots and
// updates its app; every speaker shows an install on its lamps and panel. quiet runs as an
// install starts, before its work.
func firmwareOptions(life *lifecycle, disp *display.Manager, home string, quiet func()) web.FirmwareOptions {
	o := web.FirmwareOptions{
		Active: func(active bool) {
			if active && !firmwareActive.Load() {
				quiet()
			}
			firmwareActive.Store(active)
		},
		Progress: func(percent int) { firmwareProgress.Store(int64(percent)) },
		// As stock, the panel says so when an install fails; the reason is in the web UI.
		Failed: func() {
			disp.SetTransient(display.Content{Frame: panel.Message(panel.Text("Update failed"))}, updateFailedFor)
		},
	}
	if onFirmware() {
		firmwareLaneOptions(&o, life.audioOff, home)
	}
	return o
}

// panelOptions is what the speaker's own front shows and asks: dangerous routes wait for a
// key press on the speaker, or the root-only token.
func panelOptions(disp *display.Manager, log *slog.Logger) web.PanelOptions {
	o := web.PanelOptions{
		DisplayAvailable: disp.Available,
		Notice: func(en string) {
			disp.SetTransient(display.Content{Frame: panel.Message(panel.Text(en))}, panel.NoticeFor)
		},
	}
	if onFirmware() {
		confirmer.log = log.With("comp", "confirm")
		confirmer.show = func(text string, d time.Duration) {
			disp.SetTransient(display.Content{Frame: panel.Message(panel.Text(text))}, d)
		}
		confirmer.rotate()
		o.Confirm = confirmer.confirm
	}
	return o
}

// bluetoothOptions reach BlueZ. Only a speaker has BlueZ and bt.sh. Off-speaker the
// endpoints stay honest about there being no Bluetooth rather than shelling out to nothing.
func bluetoothOptions(ctx context.Context, log *slog.Logger) web.BluetoothOptions {
	if !onFirmware() {
		return web.BluetoothOptions{}
	}
	o := web.BluetoothOptions{Bus: bluetoothBus}
	if a := bluetoothAgent(ctx, log); a != nil {
		o.Pair, o.Clear = a.Pair, func(ctx context.Context) error { _, err := a.Clear(ctx); return err }
	}
	return o
}

// onFirmware is set by init.d/soundtap: this soundtapd runs on a flashed speaker, with
// its NAND slots, mtd-utils, Bluetooth tools and boot watchdog.
func onFirmware() bool { return os.Getenv("SOUNDTAP_FIRMWARE") == "1" }

func setupLogging() *slog.Logger {
	log := slog.New(slog.NewTextHandler(os.Stderr, logOpts))
	// One format for every package, including those that log through slog.Default
	// (settings), and the level from SOUNDTAP_LOG_LEVEL (debug, info, warn, error).
	slog.SetDefault(log)
	if err := logLevel.UnmarshalText([]byte(os.Getenv("SOUNDTAP_LOG_LEVEL"))); err != nil && os.Getenv("SOUNDTAP_LOG_LEVEL") != "" {
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

func parseOptions(defaultName, defaultDeviceID string) options {
	var o options
	flag.StringVar(&o.listen, "listen", "", "comma-separated listen addrs; empty = :80")
	flag.StringVar(&o.home, "home", defaultHome, "writable state dir; its parent holds the key maps and stock's files")
	flag.StringVar(&o.deviceID, "device-id", defaultDeviceID, "speaker device id")
	flag.StringVar(&o.name, "name", defaultName, "speaker name")
	flag.BoolVar(&o.noAudio, "no-audio", false, "skip audio/buttons/leds (for testing)")
	flag.BoolVar(&o.dev, "dev", false,
		"development run on a computer: -no-audio, no hardware, nothing announced on the LAN (make dev)")
	flag.BoolVar(&o.printIdentity, "print-identity", false,
		"print this speaker's stable identity and exit")
	flag.BoolVar(&o.printOSAPI, "os-api", false,
		"print the SoundTap Core level this build needs and exit (ADR 0035)")
	flag.Parse()
	return o
}

func prepareHome(home string, log *slog.Logger) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		log.Warn("mkdir home", "err", err)
	}
	if err := removeStalePlugins(home); err != nil {
		log.Warn("remove stale plugins", "err", err)
	}
	if devMode.Load() {
		return // no boot watchdog to disarm on a computer
	}
	if err := os.MkdirAll(filepath.Dir(bootDisarmPath), 0o755); err != nil {
		log.Warn("mkdir run dir", "err", err) // the disarm write after listening says so too
	}
}

// newSpeaker is the in-process speaker: speakerstate holds all playback state.
func newSpeaker(opt options, identity string, log *slog.Logger) *speakerstate.Speaker {
	sp := speakerstate.New()
	sp.DeviceID = opt.deviceID
	sp.Name = identity
	sp.Type = board.Type // product name per model profile (ST10/ST20)
	sp.Software = strings.TrimPrefix(version, "app-v")
	sp.SetStartVolume(board.DefaultVolume)
	sp.SetWakeVolumeMax(board.WakeVolumeMax)
	sp.SetWakeVolumeMin(board.WakeVolumeMin)
	if board.NoBass {
		sp.NoBass()
	}
	sp.OnWifiProfile(func(ssid, psk string) error {
		if err := persistWifiProfile(filepath.Join(filepath.Dir(opt.home), "wifi.conf"), ssid, psk, log); err != nil {
			return err
		}
		// Let the setup client receive the HTTP success response before the Wi-Fi
		// manager tears down the AP to join the selected network.
		restartWifiAfter(2*time.Second, log)
		return nil
	})
	return sp
}

// loadPresets opens the preset store and puts its presets on the speaker.
func loadPresets(ctx context.Context, sp *speakerstate.Speaker, path string, fromStock bool, log *slog.Logger) *store.Store {
	// On the first start after an install from Bose's software (fromStock), no file at all
	// means no preset was ever stored here, so stock's presets are kept. Emptied presets leave a file.
	_, statErr := os.Stat(path)
	// An unreadable file leaves the store empty and read-only, so no preset write can
	// replace the owner's presets with this empty set; a corrupt one was moved aside.
	st, err := store.Open(path)
	atomicjson.LogLoad(log, "presets", path, err)
	if fromStock && errors.Is(statErr, fs.ErrNotExist) {
		for _, p := range stockPresets() {
			if err := st.Set(p); err != nil {
				log.Warn("preset from stock not kept", "slot", p.Slot, "err", err)
			}
		}
	}
	for _, p := range st.All() {
		source, itemType, loc := p.Source, p.Type, p.Location
		if source == "" {
			source, itemType, loc = sourceTuneIn, "stationurl", speaker.StationLocation(p.StationID)
		}
		if err := sp.StorePreset(ctx, p.Slot, source, itemType, loc, p.Name, p.Logo); err != nil {
			log.Warn("stored preset not loaded", "slot", p.Slot, "err", err)
		}
	}
	sp.OnPresetChange(persistPresetChange(st))
	sp.OnPresetSwap(st.Swap)
	return st
}

func persistPresetChange(st *store.Store) func(int, *speaker.Preset) error {
	return func(slot int, p *speaker.Preset) error {
		if p == nil {
			return st.Remove(slot)
		}
		return st.Set(store.Preset{Slot: slot, StationID: p.StationID, Source: p.Source, Type: p.Type, Location: p.Location, Name: p.Name, Logo: p.Logo})
	}
}
