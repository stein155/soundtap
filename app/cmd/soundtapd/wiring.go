// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/bco"
	"github.com/stein155/soundtap/app/internal/display"
	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/hw/model"
	"github.com/stein155/soundtap/app/internal/mdns"
	"github.com/stein155/soundtap/app/internal/mqttbridge"
	"github.com/stein155/soundtap/app/internal/nowplaying"
	"github.com/stein155/soundtap/app/internal/oplock"
	"github.com/stein155/soundtap/app/internal/panel"
	"github.com/stein155/soundtap/app/internal/player"
	"github.com/stein155/soundtap/app/internal/recent"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/snapcast"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/spotify"
	"github.com/stein155/soundtap/app/internal/store"
	"github.com/stein155/soundtap/app/internal/tunein"
	"github.com/stein155/soundtap/app/internal/update"
	"github.com/stein155/soundtap/app/internal/upnp"
	"github.com/stein155/soundtap/app/internal/web"
)

// recentStations opens the recently-played list and lets a wake replay its last station.
func recentStations(sp *speakerstate.Speaker, home string) *recent.Store {
	recents := recent.Open(filepath.Join(home, "recent.json"))
	sp.OnWakeStation(func() (string, string, string, bool) {
		e, ok := recents.LastStation()
		return e.Location, e.Name, e.Art, ok
	})
	return recents
}

// speakerOptions is what the web server reports about this speaker.
func speakerOptions(serial string, advertisedPort uint16, port string, recents *recent.Store) web.SpeakerOptions {
	o := web.SpeakerOptions{
		// The identity a device list, a group or a proxy route can rely on. Read once at
		// start, out of NAND.Manufacturing-Data; empty on a board whose partition could not
		// be read, and /api/info then falls back to the MAC-derived DeviceID.
		Serial: serial,
		Port:   advertisedPort,
		// What this speaker can do, for the device list a controller builds from /api/devices.
		Capabilities:  board.Capabilities(),
		NotifyBaseURL: "http://127.0.0.1:" + port,
		Recent:        recents,
	}
	if len(board.UserSources) > 0 {
		o.UserSources = board.UserSources
	}
	return o
}

// multiroomOptions is the local snapserver, only when one can exist. The init sets
// WLA_AUDIO_READY=0 on a WLA whose PCM never appeared and then skips snapserver entirely,
// so installing a backend there means every /api/multiroom request dials a port nothing
// listens on and logs a warning for it -- measured on the board as a WARN every few
// seconds, which is exactly the kind of noise that hides a real fault. Answer "multiroom
// unavailable" instead, which is the truth.
func multiroomOptions() web.MultiroomOptions {
	if os.Getenv("WLA_AUDIO_READY") == "0" {
		return web.MultiroomOptions{}
	}
	return web.MultiroomOptions{Backend: snapcast.New(), LocalID: os.Getenv("SOUNDTAP_SNAPCLIENT_ID")}
}

// wireWeb connects the web server to the speaker's changes.
func wireWeb(webSrv *web.Server, sp *speakerstate.Speaker) {
	sp.OnPlayChange(webSrv.Nudge)
	// Every way into standby -- the POWER key, auto-off, MQTT, POST /api/source STANDBY,
	// a source session ending -- cancels the sleep timer and, when groups are not kept,
	// releases the followers. Not only the web API's own off switches.
	webSrv.WatchStandby(sp)
}

// rebootSpeaker is POST /api/reboot's hook: sound off, then reboot. It leaves an uploaded
// app's .trial alone: only init.d/soundtap confirms one, after 60 s up with its listener
// bound. A reboot inside that minute is no proof, so the next boot drops the upload as
// it would after a hang.
func rebootSpeaker(audioOff func(), reboot func() error) func() error {
	return func() error {
		audioOff()
		return reboot()
	}
}

// firmwareLaneOptions turns on what only a flashed speaker can do: installs, reboot, app
// updates and U-Boot's boot count.
func firmwareLaneOptions(o *web.FirmwareOptions, audioOff func(), home string) {
	// /run/soundtap is tmpfs: the lock is gone after a reboot, and kept by an
	// install's children across a soundtapd restart (ADR 0052).
	oplock.Path = "/run/soundtap/operation.lock"
	// Only a flashed speaker has the NAND slots — and the mtd-utils — that an
	// install needs. init.d/soundtap is what sets this.
	o.Install = true
	o.Reboot = rebootSpeaker(audioOff, rebootSystem)
	// Only where init.d/soundtap runs soundtapd from tmpfs: it swaps the file there and
	// restarts this process, and owns the boot-time side of the override.
	if exe, err := os.Executable(); err == nil && runsFromTmpfs(exe) {
		o.SoundTapd = &web.SoundTapdUpdate{Run: exe, Base: "/opt/soundtap/soundtapd", Home: home, Restart: func() {
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		}}
	}
	// U-Boot's own boot count (ADR 0010). A board without one reads no magic in
	// SCRATCH2 and nothing is written.
	o.BootGuard = flash.ConfirmBootCount
	if rootHelper != nil {
		// soundtapd's own user has no /dev/mem and may not write NAND (ADR 0066).
		o.BootGuard = flash.ConfirmBootCountVia(rootHelper)
		o.RootRun = rootHelper.Run
	}
}

// runsFromTmpfs says whether exe is the copy init.d/soundtap runs from tmpfs and swaps for
// an uploaded app: in /run/audio-bin/app, a directory soundtapd's user owns and no PATH
// names, when soundtap-root starts it; in /run/audio-bin under an OS that runs it as root.
func runsFromTmpfs(exe string) bool {
	return exe == "/run/audio-bin/app/soundtapd" || exe == "/run/audio-bin/soundtapd"
}

// wireFirmwareLane starts the app lane's updates and the BCO's LAN watch.
func wireFirmwareLane(ctx context.Context, webSrv *web.Server, upd *update.Manager, home string, log *slog.Logger) {
	if exe, err := os.Executable(); err == nil && runsFromTmpfs(exe) {
		// The app lane updates itself from app-v* releases through the same step (ADR 0035).
		upd.SetAppInstaller(webSrv)
	}
	if b, _ := os.ReadFile("/proc/cmdline"); strings.Contains(string(b), "module_type=scm") {
		lanLog := log
		if f, err := os.Create("/run/soundtap/bco-lan.log"); err == nil {
			// Capped: the watch runs for the whole uptime and /run is 4 MiB.
			lanLog = slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, &limitedFile{f: f, left: 256 << 10}), logOpts))
		}
		goSafe("bcoLAN", func() { bco.NewLANWatch(home, lanLog.With("comp", "bco-lan")).Run(ctx) })
	}
}

// newMQTT is the MQTT bridge. It idles until the settings page enables it.
func newMQTT(sp *speakerstate.Speaker, set *settings.Store, upd *update.Manager, enr *nowplaying.Enricher, log *slog.Logger) *mqttbridge.Bridge {
	mb := mqttbridge.New(sp, func() mqttbridge.Config { return mqttbridge.Config(set.Get().MQTT) }, upd, log.With("comp", "mqtt"))
	mb.SetNowPlaying(enr.NowPlaying)
	return mb
}

// startAudioSide starts the audio path, or just the no-audio watchdog on -no-audio.
// player / buttons / led talk directly to speakerstate — no HTTP loopback.
func startAudioSide(ctx context.Context, opt options, a audioWiring) audioHealth {
	if devMode.Load() {
		goSafe("devPlayer", func() { runDevPlayer(ctx, a.sp) })
		return nil
	}
	if opt.noAudio {
		goSafe("noAudioWatchdog", func() { runNoAudioWatchdog(ctx, a.sp, noAudioGrace, a.log.With("comp", "player")) })
		return nil // the watchdog's view of the audio pump: none
	}
	return startAudio(ctx, a)
}

// setupOptions are Wi-Fi setup, saved networks and the factory reset.
func setupOptions(life *lifecycle, set *settings.Store, st *store.Store, home, wifiConf string, log *slog.Logger) web.SetupOptions {
	return web.SetupOptions{
		SetupMode:    func() error { return openSetupAP(log) },
		WifiProfiles: func() ([]web.WifiProfile, error) { return savedWifiProfiles(wifiConf) },
		ForgetWifi:   func(id string) error { return forgetWifiProfile(wifiConf, id) },
		// The dashboard's factory reset runs the same reset the front-panel chord does.
		// It used to clear only Wi-Fi, so a reset from the browser left presets, accounts,
		// Bluetooth pairings and the speaker's name in place — a "reset" that reset almost
		// nothing. One implementation, one behaviour, whichever way it is asked for.
		FactoryReset: func() error {
			factoryReset(home, wifiConf, log.With("comp", "reset"), func() {
				// Let the browser receive the response before the daemon restarts.
				go func() {
					time.Sleep(2 * time.Second)
					life.shutdown()
				}()
			}, set, st)
			return nil
		},
	}
}

// changeNotifiers are the web server's rename and integration-switch hooks, and the
// channels they signal on.
func changeNotifiers() (web.ChangeHooks, chan string, chan struct{}) {
	nameUpdates := make(chan string, 1)
	// An integration switched on or off restarts the receivers with the new set.
	receiverUpdates := make(chan struct{}, 1)
	return web.ChangeHooks{
		Name: func(name string) { sendLatest(nameUpdates, name) },
		Receivers: func() {
			select {
			case receiverUpdates <- struct{}{}:
			default: // a restart is already pending; it reads the settings then
			}
		},
	}, nameUpdates, receiverUpdates
}

// startWebServices starts the SSE hub, MQTT and the IP/mDNS upkeep.
func startWebServices(ctx context.Context, webSrv *web.Server, sp *speakerstate.Speaker, mb *mqttbridge.Bridge, identity string, advertisedPort uint16, opt options, log *slog.Logger) {
	// Live-state push to browsers over SSE, and keep the speaker's own IP current (for
	// /api/info + multiroom discovery) as Wi-Fi comes up after boot.
	goSafe("web", func() { webSrv.Run(ctx) })
	goSafe("mqttBridge", func() { mb.Run(ctx) })
	goSafe("maintainIP", func() {
		maintainIP(ctx, sp, webSrv, identity, mdns.Service{
			ID:       webSrv.Identity(opt.deviceID),
			Model:    board.Type,
			Firmware: version,
			API:      web.APILevel,
			Port:     advertisedPort,
		}, log.With("comp", "net"))
	})
}

// sendLatest puts v on a one-slot channel, replacing a value nobody took yet.
func sendLatest(ch chan string, v string) {
	select {
	case ch <- v:
	default:
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- v:
		default:
		}
	}
}

// audioWiring is what startAudio connects the audio side to.
type audioWiring struct {
	sp          *speakerstate.Speaker
	web         *web.Server
	spot        *spotify.Client
	set         *settings.Store
	st          *store.Store
	tc          *tunein.Client
	disp        *display.Manager
	mux         *http.ServeMux
	home, port  string
	identity    string
	nameUpdates <-chan string
	// receiverUpdates signals that an integration (Spotify, AirPlay) was switched.
	receiverUpdates <-chan struct{}
	shutdown        func()
	// chime plays the preset-stored tone, eventChime a named event's (wireChimes).
	chime      func()
	eventChime func(name string)
	fadeOut    *atomic.Pointer[func()]
	savePrefs  *atomic.Pointer[func()]
	log        *slog.Logger
}

// startAudio brings up everything that needs the audio path: the arbiter, receivers,
// player, buttons, panel and UPnP. It returns the pump's health for the watchdog.
func startAudio(ctx context.Context, a audioWiring) audioHealth {
	a.chime, a.eventChime = wireChimes(ctx, a.web, a.sp, a.port)
	tap := wireAudioPath(ctx, a)
	wireSpotify(ctx, a, tap)
	startAudioLoops(ctx, a, tap)
	startPanel(ctx, a)
	return tap.Health
}

// wireAudioPath starts the arbiter (when enabled) and hooks up channel, multiroom and
// source changes. The returned tap is a no-op without an arbiter.
func wireAudioPath(ctx context.Context, a audioWiring) audioTap {
	sp, webSrv, set, log := a.sp, a.web, a.set, a.log
	tap := noopTap()
	// Audio arbiter: single DAC writer + announcement ducking (AUDIO_MIX=1 only).
	if mixEnabled() {
		tap = setupArbiter(ctx, sp, func() {
			// Read now, at the source change: by the time the leave runs a new follow may
			// have been written, and that one is not this leave's to end.
			host, id := webSrv.Follow()
			go webSrv.LeaveFollow(host, id)
		}, log.With("comp", "audio"))
		fade := tap.FadeOut
		a.fadeOut.Store(&fade)
	}
	applyChannel := func(name string) {
		c, err := audio.ParseChannel(name)
		if name != "" && err != nil {
			log.Warn("unknown stored channel; playing stereo", "channel", name)
		}
		tap.SetChannel(c)
		frontPanel.SetChannel(name)
	}
	applyChannel(set.Get().Channel)
	sp.AuxName(func() string { return set.Get().AuxName })
	webSrv.AttachAudio(web.AudioHooks{
		// Two distinct hooks. A source *change* must stop the daemon that owned the
		// amp; a *flush* only drops queued audio. Routing flush through the source
		// change was killing shairport-sync on every Spotify track boundary.
		SourceChanging: func(source string) {
			receivers.Preempt(sp.StatusSnapshot().Source, source)
			tap.Flush() // after the preempt: a yielding source may still have drained into the bus
		},
		Flush:           tap.Flush,
		FlushWait:       tap.FlushWait,
		Channel:         applyChannel,
		Downstream:      tap.Downstream,
		MultiroomActive: tap.SetMultiroom,
		// Following another speaker is a setting plus a restart: runMediaDaemons builds
		// snapclient's arguments from that setting at every start.
		SelectSource: sp.SetSource,
		LeaderLive:   sp.SetLeaderLive,
		LeaderNow:    sp.SetLeaderNow,
		SelfAddress: func(ctx context.Context) string {
			if net, err := sp.NetworkInfo(ctx); err == nil && net != nil {
				return net.IP
			}
			return ""
		},
		RestartSnapclient: restartSnapclient,
		WantAnchor:        anchor.Want,
		EventChime:        a.eventChime,
		AmpFault:          ampFaulted,
		Battery:           batteryStatus,
	})
	goSafe("watchMultiroom", func() { webSrv.WatchMultiroom(ctx, 3*time.Second) })
	goSafe("watchLeader", func() { webSrv.WatchLeader(ctx, 30*time.Second) })
	goSafe("watchLeaderPower", func() { webSrv.WatchLeaderPower(ctx, 5*time.Second) })
	goSafe("watchPeers", func() { webSrv.WatchPeers(ctx, 15*time.Second) })
	tap.SetMultiroom(os.Getenv("SOUNDTAP_MULTIROOM") == "1")
	return tap
}

// startAudioLoops restores the saved audio prefs and starts the loops that drive the
// amp: prefs saver, auto-off, source routing, aux capture and the player.
func startAudioLoops(ctx context.Context, a audioWiring, tap audioTap) {
	sp, set, tc, log := a.sp, a.set, a.tc, a.log
	// Push the buffering -> playing transition to browsers the moment it happens,
	// instead of waiting for the SSE hub's next poll.
	// Volume and tone before anything can play, so the first note is at the
	// level the user left it at rather than the built-in default.
	restoreAudioPrefs(ctx, sp, set, log.With("comp", "prefs"))
	goSafe("prefsSaver", func() { runPrefsSaver(ctx, sp, set, log.With("comp", "prefs")) })
	finalSave := func() { saveAudioPrefs(sp, set, log.With("comp", "prefs")) }
	a.savePrefs.Store(&finalSave)
	goSafe("autoOff", func() { runAutoOff(ctx, sp, set, tap.Health, log.With("comp", "autooff")) })
	goSafe("sourceRouting", func() { runSourceRouting(ctx, sp, log.With("comp", "source-route")) })
	if mixEnabled() && !exists(board.ShelbySource) && exists(board.AuxCapture) {
		goSafe("auxCapture", func() { runAuxCapture(ctx, sp, busPath(), log.With("comp", "aux")) })
	}
	tuneinStreams.Load(filepath.Join(a.home, "tunein-streams.json"))
	goSafe("prewarm", func() { player.Prewarm(ctx, tc, sp.Presets, tuneinStreams, sp.IsStandby, log.With("comp", "prewarm")) })
	goSafe("player", func() { runPlayer(ctx, sp, tc, tap.Health, tap.Flush, log.With("comp", "player")) })
}

// startPanel wires the front panel and everything user-facing: chords, chimes,
// buttons, IR, LEDs, display, subprocesses, UPnP and the amp fault watch.
func startPanel(ctx context.Context, a audioWiring) {
	sp, webSrv, set, st, disp, mux := a.sp, a.web, a.set, a.st, a.disp, a.mux
	port, identity, nameUpdates, shutdown, log := a.port, a.identity, a.nameUpdates, a.shutdown, a.log
	// Front-panel escape hatches remain usable when the network is unreachable.
	wifiConf := filepath.Join(filepath.Dir(a.home), "wifi.conf")
	feedback := func(text string) {
		d := 2 * time.Second
		if strings.HasPrefix(text, "Hold ") {
			d = chordHold
		}
		disp.SetTransient(display.Content{Frame: panel.Message(panel.Text(text))}, d)
	}
	// Both chords change the network, so an install or BCO flash refuses them as it does a key.
	refused := func() {
		log.Info("chord ignored: another speaker operation is in progress")
		feedback("Updating")
	}
	chords := recoveryChords(
		exclusive(&resetLock, false, refused, func() {
			disp.SetTransient(display.Content{Frame: panel.Message(panel.Text("Resetting"))}, 10*time.Second)
			// The owner key goes as on the web page's reset (ADR 0048); the chord holds
			// the operations lock an environment write needs.
			if err := webSrv.ClearOwnerKey(); err != nil {
				log.Error("factory reset: remove the owner key", "err", err)
			}
			factoryReset(a.home, wifiConf, log.With("comp", "reset"), shutdown, set, st)
		}),
		exclusive(new(oplock.Lock), true, refused, func() {
			disp.SetTransient(display.Content{Frame: panel.Message(panel.Text("Setup"))}, 5*time.Second)
			if err := toggleSetupAP(wifiConf, func() error { return restartWifi(log) }, func() error { return openSetupAP(log) }); err != nil {
				log.Error("chord setup mode", "err", err)
			}
		}),
	)
	chime := a.chime
	btPair := wireBluetoothPair(ctx, sp, log)
	auxHoldBT := slices.Contains(board.UserSources, sourceBluetooth)
	panel.SetLanguage(func() string { return set.Get().Language })
	goSafe("buttons", func() {
		runButtons(ctx, sp, chords, feedback, chime, btPair, auxHoldBT, log.With("comp", "buttons"))
	})
	// The remote goes through the same key states as the panel, so it answers the same way:
	// a stored preset shows and chimes, a held AUX picks Bluetooth, a held Bluetooth pairs.
	goSafe("ir", func() {
		runIR(ctx, sp, func(h *buttonHandler) {
			h.feedback, h.chime, h.btPair, h.auxHoldBT = feedback, chime, btPair, auxHoldBT
		}, log.With("comp", "ir"))
	})
	goSafe("leds", func() { runLamps(ctx, sp, log.With("comp", "led")) })
	if board.Name == model.ST300 {
		wireSoundbar(ctx, webSrv, sp, log.With("comp", "lpm"))
	}
	if board.Battery { // before Run, which reads it every frame
		frontPanel.Battery = func() (int, bool, bool) {
			b, ok := batteryStatus()
			return b.Level, b.Charging, ok && b.Present
		}
	}
	goSafe("display", func() { frontPanel.Run(ctx, sp, disp, panel.Clock(set), log.With("comp", "oled")) })
	goSafe("clockScreen", func() {
		panel.RunClockScreen(ctx, disp, func() settings.Display { return set.Get().Display }, log.With("comp", "clock"))
	})
	goSafe("mediaDaemons", func() {
		runMediaDaemons(ctx, a.home, func() string { return sp.StatusSnapshot().Source }, log.With("comp", "proc"))
	})
	goSafe("subprocesses", func() {
		runSubprocesses(ctx, identity, sp.DeviceID, nameUpdates, a.receiverUpdates,
			func() settings.Integrations { return set.Get().Integrations },
			func() int { return sp.StatusSnapshot().Volume }, log.With("comp", "proc"))
	})
	goSafe("airplayMeta", func() { runAirplayMeta(ctx, sp, log.With("comp", "airplay-meta")) })
	reg := upnp.NewRegistry()
	webSrv.AttachUPnP(web.UPnPHooks{
		Seek:     startUPnP(ctx, sp, mux, webSrv.Guard, identity, port, log.With("comp", "upnp")).SeekTo,
		Registry: reg,
		Queue:    upnpFolder,
	})
	goSafe("upnpFolder", func() { runUPnPFolder(ctx, sp, log.With("comp", "upnp")) })
	goSafe("discover", func() { reg.Discover(ctx, func(msg string, kv ...any) { log.Info(msg, kv...) }) })
	goSafe("ampFault", func() { runAmpFault(ctx, log.With("comp", "ampfault")) })
	if board.HostLink {
		goSafe("adapterLink", func() { runAdapterLink(ctx, sp, btPair, log.With("comp", "abl")) })
	}
	if board.STM32Amp {
		goSafe("sa5Amp", func() { runSA5Amp(ctx, log.With("comp", "sa5amp")) })
	}
	if board.Battery {
		goSafe("battery", func() { runBattery(ctx, log.With("comp", "battery")) })
		goSafe("batteryUI", func() { runBatteryUI(ctx, sp, log.With("comp", "battery")) })
	}
}
