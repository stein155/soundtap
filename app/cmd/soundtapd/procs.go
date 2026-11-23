// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/hw/model"
	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/spotify"
	"github.com/stein155/soundtap/app/internal/supervise"
)

// receivers holds the supervised processes that play a source.
var receivers = supervise.NewProcesses()

// daemon is how the media daemon name runs as bin: as its own user, with its capabilities
// and environment.
func daemon(name, bin string, args func() []string) supervise.Spec {
	return supervise.Spec{Name: name, Bin: bin, Args: args, Env: procEnv[name], User: procUser[name], Caps: procCaps[name]}
}

// procEnv is extra environment per supervised process.
var procEnv = map[string][]string{
	// librespot serves soundtapd's Spotify browsing on this loopback address.
	"librespot": {"SOUNDTAP_SPOTIFY_CTL=" + spotify.CtlAddr, "SOUNDTAP_SPOTIFY_BOUNDARY=1"},
}

// procUser is the system user each media daemon runs as (ADR 0066). The users come from
// os/board/rootfs/users.txt, all with primary group audio, the one group that may write
// the source bus. A daemon whose user is missing does not start: running it as root
// instead would quietly undo the separation.
var procUser = map[string]string{
	"shairport-sync": "airplay",
	"librespot":      "spotify",
	snapclient:       "snapcast",
	// snapserver parses what any client on TCP 1704 sends.
	"snapserver": "snapcast",
	// nqptp parses PTP from the LAN on UDP 319 and 320. Its shared memory for
	// shairport-sync is created mode 0666, so the two users still share it.
	"nqptp": "ptp",
}

// capNetBindService (CAP_NET_BIND_SERVICE, linux/capability.h) lets a non-root process
// bind a port below 1024. syscall does not name it.
const capNetBindService = 10

// procCaps are the capabilities a daemon keeps after dropping root, as ambient ones.
var procCaps = map[string][]uintptr{"nqptp": {capNetBindService}} // PTP's ports are 319 and 320

// decoderUser runs every ffmpeg soundtapd starts: radio streams and announcements, both
// fetched from URLs the LAN can choose.
var decoderUser = "radio" // a variable so tests run as root can name a user that exists

// runSubprocesses supervises the AirPlay receiver (shairport-sync). Its audio reaches the
// speaker via ALSA "default", which under AUDIO_MIX is the source bus the Mixer reads (see
// asound.conf.bus); the arbiter mixes/ducks it. Bluetooth is owned by the init script's
// bt.sh (not here). System bring-up (kernel modules, wifi, dbus, avahi, bt.sh) lives in
// os/overlay/etc/init.d/soundtap; here we keep network receivers running.
// airplayEnabled reports whether to run shairport-sync. An ST20 SCM needs explicit scm-ap2
// bridge mode; every other ST20 mode keeps native BCO AirPlay 1 to avoid an unreachable AP2
// receiver competing for the mDNS name behind BCO NAT.
func airplayEnabled(mode, name string) bool {
	if name == model.ST20 {
		return mode == "scm-ap2"
	}
	return mode != "bco-ap1"
}

// integrations says which receivers the owner has switched on; a value on changed restarts
// them with the current set.
func runSubprocesses(ctx context.Context, name, deviceID string, names <-chan string, changed <-chan struct{},
	integrations func() settings.Integrations, volume func() int, log *slog.Logger) {
	modelAirplay := airplayEnabled(os.Getenv("SOUNDTAP_AIRPLAY_MODE"), board.Name)
	if !modelAirplay {
		log.Info("shairport skipped", "mode", os.Getenv("SOUNDTAP_AIRPLAY_MODE"), "model", board.Name)
	}
	conf := shairportConf(shairportBaseConf, shairportRunConf, deviceID, log)
	// Bluetooth is brought up entirely by the init
	// script's /opt/soundtap/bt.sh (TI attach + bluetoothd + bluealsa + pairing agent +
	// per-session source signalling) — running a second BlueZ stack from soundtapd fought
	// bt.sh for the HCI adapter and thrashed on the D-Bus name.
	for {
		on := integrations()
		airplay := modelAirplay && on.AirPlay()
		log.Info("receivers", "airplay", airplay, "spotify", on.Spotify())
		childCtx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		if airplay {
			wg.Add(1)
			go func() {
				defer wg.Done()
				args := shairportArgs(conf, name)
				if !waitUntil(childCtx, nqptpWait, func() bool { return udpListening(procNetUDP, nqptpControlPort) }) {
					log.Warn("nqptp not ready; AirPlay 2 may fall back to Classic", "after", nqptpWait)
				}
				supervise.Run(childCtx, log, receivers, sourceAirPlay, daemon("shairport-sync", "shairport-sync", func() []string { return args }))
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !on.Spotify() {
				return
			}
			if _, err := exec.LookPath("librespot"); err != nil {
				log.Warn("not installed, skipping", "bin", "librespot")
				return
			}
			securePath(spotifyDir, 0o700, log)
			securePath(spotifySystemDir, 0o700, log)
			securePath(filepath.Join(spotifySystemDir, "credentials.json"), 0o600, log)
			// Older builds ran librespot as root and left everything here root's.
			ownTree(spotifyDir, procUser["librespot"], log)
			ownTree(spotifyStateDir, procUser["librespot"], log)
			// librespot reaches Spotify's servers as it starts and exits at once without a
			// route ("could not initialize spirc"): on every boot it died once and came back
			// 6 s later. A speaker with no route at all (its setup network) starts it anyway.
			if !waitUntil(childCtx, librespotRouteWait, func() bool { return hasDefaultRoute(procNetRoute) }) && childCtx.Err() == nil {
				log.Info("no default route yet; starting librespot anyway", "after", librespotRouteWait)
			}
			// umask 077 so the credentials librespot writes are the owner's alone;
			// librespot creates them with the default mode.
			// The volume is read at every start, crash restarts included: librespot reports
			// --initial-volume to Spotify clients, and the boot-time level went stale.
			supervise.Run(childCtx, log, receivers, sourceSpotify, daemon("librespot", "sh", func() []string {
				return append([]string{"-c", `umask 077; exec librespot "$@"`, "librespot"}, librespotArgs(name, volume())...)
			}))
		}()
		select {
		case <-ctx.Done():
			cancel()
			wg.Wait()
			return
		case name = <-names:
			cancel()
			wg.Wait()
			if err := publishCurrentName(name); err != nil {
				log.Warn("publish name", "err", err)
			}
			log.Info("receivers renamed", "name", name)
		case <-changed:
			cancel()
			wg.Wait()
		}
	}
}

// snapclient is the Snapcast client binary and its supervised name.
const snapclient = "snapclient"

// mediaDaemons holds the supervised processes that belong to no source (ADR 0121).
var mediaDaemons = supervise.NewProcesses()

// runMediaDaemons supervises, for the life of ctx, the daemons that belong to no source:
// nqptp, AirPlay 2's clock, where AirPlay 2 can run, and Snapcast where the init script
// prepared a snapserver configuration: snapserver always, snapclient while snapclientWanted. They die with soundtapd, so shairport-sync, which
// registers with nqptp only at its own start, always starts after the nqptp it talks to.
// source is the speaker's current source, which decides where snapclient plays.
func runMediaDaemons(ctx context.Context, home string, source func() string, log *slog.Logger) {
	var wg sync.WaitGroup
	run := func(name, bin string, args func() []string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			supervise.Run(ctx, log, mediaDaemons, name, daemon(name, bin, args))
		}()
	}
	if os.Getenv("SOUNDTAP_AIRPLAY_MODE") == "scm-ap2" {
		run("nqptp", "nqptp", func() []string { return nil })
	} else {
		log.Info("nqptp skipped", "mode", os.Getenv("SOUNDTAP_AIRPLAY_MODE"))
	}
	if conf := os.Getenv("SOUNDTAP_SNAPSERVER_CONF"); conf != "" {
		seedSnapserverState(snapserverDataDir, snapserverKeepDir, log)
		ownTree(snapserverDataDir, procUser["snapserver"], log)
		go keepSnapserverState(ctx, snapserverDataDir, snapserverKeepDir, log)
		run("snapserver", "snapserver", func() []string { return []string{"-c", conf} })
		// The arguments are read at every start, so following another speaker is a restart.
		server, id := filepath.Join(home, "multiroom-server"), os.Getenv("SOUNDTAP_SNAPCLIENT_ID")
		wg.Add(1)
		go func() {
			defer wg.Done()
			wanted := func() bool { return snapclientWanted(server, source(), anchor.Wanted()) }
			supervise.RunWhileWanted(ctx, wanted, snapclientPoke, func(ctx context.Context) {
				supervise.Run(ctx, log, mediaDaemons, snapclient, daemon(snapclient, "nice", func() []string {
					return snapclientArgs(server, id, source())
				}))
			}, func() { log.Info("snapclient parked: no group to anchor or follow") })
		}()
	} else {
		log.Info("snapcast skipped: no snapserver configuration")
	}
	wg.Wait()
}

// snapclientPoke tells the snapclient gate (RunWhileWanted) to look again whether it runs.
var snapclientPoke = make(chan struct{}, 1)

// pokeSnapclient starts or parks snapclient as snapclientWanted now says. It never blocks.
func pokeSnapclient() {
	select {
	case snapclientPoke <- struct{}{}:
	default: // a poke is pending and will read the state then
	}
}

// anchor is this speaker's demand for its silent group anchor.
var anchor = multiroom.NewAnchorDemand(multiroom.AnchorHold, pokeSnapclient)

// restartSnapclient makes snapclient re-read which server it follows, starting it if it
// is parked and now wanted.
func restartSnapclient() {
	pokeSnapclient()
	mediaDaemons.Restart(snapclient)
}

// wakeSnapclient starts snapclient at once if it is parked or waiting out a crash backoff:
// a follower whose group was in standby joins the moment the leader plays.
func wakeSnapclient() {
	pokeSnapclient()
	mediaDaemons.Wake(snapclient)
}

// snapclientWanted reports whether snapclient runs at all: while this speaker follows
// another one on MULTIROOM, or while it wants its anchor (multiroom.AnchorDemand: it leads
// a group or is about to). In plain standby, and in its group's standby as a follower, it
// is parked: the anchor alone woke 35 times a second to write silence into /dev/null.
func snapclientWanted(serverFile, source string, anchorWanted bool) bool {
	return anchorWanted || followedHost(serverFile, source) != ""
}

// snapclientArgs is nice's command line for one snapclient, pointed where serverFile says
// while source is MULTIROOM. On any other source it is the anchor, which runs only while
// this speaker leads a group or is about to (snapclientWanted): a follower in the group's
// standby keeps its follow but not the leader's silent stream, which kept the whole chain
// busy, and parks; the leader's wake switches the source, which starts this again.
//
// Following nobody, it is the silent group anchor: tcp://127.0.0.1:1704 rendered into
// /dev/null, there so snapserver holds this speaker's group for SetMember. This speaker's Mixer already
// drives the card; a client that played here would fight it for the PCM. The anchor takes
// the stream's own format, since resampling into /dev/null cost the ST20 a third of its CPU
// (2026-09-24), and runs at nice 19: at full priority it took the CPU librespot needs at
// every track start on a Spotify leader (2026-09-29).
//
// Following another speaker, it writes that speaker's stream into the bus FIFO, which the
// Mixer reads at audio.SampleRate. The channel field must be "*": snapclient 0.35 exits on
// a number.
func snapclientArgs(serverFile, id, source string) []string {
	if id == "" {
		id = "soundtap-local"
	}
	common := []string{"--mixer", "none", "--logsink", "stderr", "--logfilter", "*:warning", "--hostID", id}
	if host := followedHost(serverFile, source); host != "" {
		args := []string{"-n", "0", snapclient, "--player", "file:filename=" + busPath(),
			"--sampleformat", strconv.Itoa(audio.SampleRate) + ":16:*"}
		return append(append(args, common...), "tcp://"+host+":1704")
	}
	args := []string{"-n", "19", snapclient, "--player", "file:filename=/dev/null"}
	return append(append(args, common...), "tcp://127.0.0.1:1704")
}

// followedHost is the other speaker snapclient follows while source is MULTIROOM, "" when
// it follows nobody, follows this speaker itself, or the source is another one. The address
// is serverFile's first line (the leader's id follows it). Whatever is in it, only these
// characters reach the command line.
func followedHost(serverFile, source string) string {
	if source != sourceMultiroom {
		return ""
	}
	b, err := os.ReadFile(serverFile)
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(b), "\n")
	host := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || strings.ContainsRune(".:_-", r) {
			return r
		}
		return -1
	}, line)
	if host == "127.0.0.1" || host == "localhost" {
		return ""
	}
	return host
}

// spotifyAccount lets the web UI forget the Connect login.
type spotifyAccount struct{}

// Forget deletes the stored credentials and restarts librespot, which comes back
// unpaired and waits for the Spotify app again.
func (spotifyAccount) Forget() error {
	if err := os.Remove(filepath.Join(spotifySystemDir, "credentials.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	receivers.Restart(sourceSpotify)
	return nil
}

func librespotArgs(name string, volume int) []string {
	if volume < 0 {
		volume = 0
	} else if volume > 100 {
		volume = 100
	}
	return []string{"-q", "--name", name, "--device-type", "speaker", "--backend", "alsa",
		"--device", "default", "--disable-gapless", "--mixer", "softvol", "--volume-ctrl", "fixed",
		"--initial-volume", strconv.Itoa(volume),
		"--bitrate", "320", "--disable-audio-cache", "--cache", spotifyDir + "/cache",
		"--system-cache", spotifySystemDir, "--emit-sink-events",
		"--onevent", "/opt/soundtap/spotify-source.sh"}
}

const (
	spotifyDrainQuiet = 100 * time.Millisecond
	spotifyDrainMax   = 1500 * time.Millisecond
)

// waitBusQuiet returns once no source has written to the bus for quiet, or after max.
func waitBusQuiet(health func() audio.Health, quiet, max time.Duration) {
	poll := quiet / 4
	last, still := health().SourceBytes, time.Duration(0)
	for end := time.Now().Add(max); time.Now().Before(end) && still < quiet; {
		time.Sleep(poll)
		if b := health().SourceBytes; b != last {
			last, still = b, 0
		} else {
			still += poll
		}
	}
}
