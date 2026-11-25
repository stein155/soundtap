// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"fmt"
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
//
//nolint:gochecknoglobals // process-wide state: the receiver loop runs them, the source arbiter preempts them
var receivers = supervise.NewProcesses()

// librespotBin is Spotify Connect's receiver, its supervised name and its user's key.
const librespotBin = "librespot"

// daemon is how the media daemon name runs as bin: as its own user, with its capabilities
// and environment.
func daemon(name, bin string, args func() []string) supervise.Spec {
	return supervise.Spec{
		Name: name, Bin: bin, Args: args, Env: procEnv()[name], User: procUser[name], Caps: procCaps()[name],
	}
}

// procEnv is extra environment per supervised process.
func procEnv() map[string][]string {
	return map[string][]string{
		// librespot serves soundtapd's Spotify browsing on this loopback address.
		librespotBin: {"SOUNDTAP_SPOTIFY_CTL=" + spotify.CtlAddr, "SOUNDTAP_SPOTIFY_BOUNDARY=1"},
	}
}

// procUser is the system user each media daemon runs as (ADR 0066). The users come from
// os/board/rootfs/users.txt, all with primary group audio, the one group that may write
// the source bus. A daemon whose user is missing does not start: running it as root
// instead would quietly undo the separation.
//
//nolint:gochecknoglobals // a test seam: a test drops a user to see the daemon refused
var procUser = map[string]string{
	"shairport-sync": "airplay",
	librespotBin:     "spotify",
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
func procCaps() map[string][]uintptr {
	return map[string][]uintptr{"nqptp": {capNetBindService}} // PTP's ports are 319 and 320
}

// decoderUser runs every ffmpeg soundtapd starts: radio streams and announcements, both
// fetched from URLs the LAN can choose.
//
//nolint:gochecknoglobals // a test seam: tests run as root name a user that exists
var decoderUser = "radio"

// The modes of librespot's state: its directories and its stored login are the user's alone.
const (
	spotifyDirMode         = 0o700
	spotifyCredentialsMode = 0o600
)

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
	integrations func() settings.Integrations, volume func() int, log *slog.Logger,
) {
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
		enabled := integrations()
		airplay := modelAirplay && enabled.AirPlay()
		log.Info("receivers", "airplay", airplay, "spotify", enabled.Spotify())

		childCtx, cancel := context.WithCancel(ctx)

		var wg sync.WaitGroup
		if airplay {
			wg.Go(func() { runShairport(childCtx, conf, name, log) })
		}

		wg.Go(func() {
			if enabled.Spotify() {
				runLibrespot(childCtx, name, volume, log)
			}
		})

		select {
		case <-ctx.Done():
			cancel()
			wg.Wait()

			return
		case name = <-names:
			cancel()
			wg.Wait()

			err := publishCurrentName(name)
			if err != nil {
				log.Warn("publish name", "err", err)
			}

			log.Info("receivers renamed", "name", name)
		case <-changed:
			cancel()
			wg.Wait()
		}
	}
}

// runShairport supervises shairport-sync until ctx ends, once nqptp listens or after
// nqptpWait.
func runShairport(ctx context.Context, conf, name string, log *slog.Logger) {
	args := shairportArgs(conf, name)

	if !waitUntil(ctx, nqptpWait, func() bool { return udpListening(procNetUDP, nqptpControlPort) }) {
		log.Warn("nqptp not ready; AirPlay 2 may fall back to Classic", "after", nqptpWait)
	}

	shairport := daemon("shairport-sync", "shairport-sync", func() []string { return args })
	supervise.Run(ctx, log, receivers, sourceAirPlay, shairport)
}

// runLibrespot supervises librespot until ctx ends, once its state is its user's own and
// the speaker has a route or after librespotRouteWait.
func runLibrespot(ctx context.Context, name string, volume func() int, log *slog.Logger) {
	_, err := exec.LookPath(librespotBin)
	if err != nil {
		log.Warn("not installed, skipping", "bin", librespotBin)

		return
	}

	securePath(spotifyDir, spotifyDirMode, log)
	securePath(spotifySystemDir, spotifyDirMode, log)
	securePath(filepath.Join(spotifySystemDir, "credentials.json"), spotifyCredentialsMode, log)
	// Older builds ran librespot as root and left everything here root's.
	ownTree(spotifyDir, procUser[librespotBin], log)
	ownTree(spotifyStateDir, procUser[librespotBin], log)
	// librespot reaches Spotify's servers as it starts and exits at once without a
	// route ("could not initialize spirc"): on every boot it died once and came back
	// 6 s later. A speaker with no route at all (its setup network) starts it anyway.
	routed := waitUntil(ctx, librespotRouteWait, func() bool { return hasDefaultRoute(procNetRoute) })
	if !routed && ctx.Err() == nil {
		log.Info("no default route yet; starting librespot anyway", "after", librespotRouteWait)
	}
	// umask 077 so the credentials librespot writes are the owner's alone;
	// librespot creates them with the default mode.
	// The volume is read at every start, crash restarts included: librespot reports
	// --initial-volume to Spotify clients, and the boot-time level went stale.
	supervise.Run(ctx, log, receivers, sourceSpotify, daemon(librespotBin, "sh", func() []string {
		return append([]string{"-c", `umask 077; exec librespot "$@"`, "librespot"}, librespotArgs(name, volume())...)
	}))
}

// snapclient is the Snapcast client binary and its supervised name.
const snapclient = "snapclient"

// mediaDaemons holds the supervised processes that belong to no source (ADR 0121).
//
//nolint:gochecknoglobals // process-wide state: started at boot, restarted and woken by the multiroom code
var mediaDaemons = supervise.NewProcesses()

// runMediaDaemons supervises, for the life of ctx, the daemons that belong to no source:
// nqptp, AirPlay 2's clock, where AirPlay 2 can run, and Snapcast where the init script
// prepared a snapserver configuration: snapserver always, snapclient while snapclientWanted.
// They die with soundtapd, so shairport-sync, which registers with nqptp only at its own
// start, always starts after the nqptp it talks to.
// source is the speaker's current source, which decides where snapclient plays.
func runMediaDaemons(ctx context.Context, home string, source func() string, log *slog.Logger) {
	var wg sync.WaitGroup

	run := func(name, bin string, args func() []string) {
		wg.Go(func() {
			supervise.Run(ctx, log, mediaDaemons, name, daemon(name, bin, args))
		})
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

		run("snapserver", "nice", func() []string { return snapserverArgs(conf) })
		// The arguments are read at every start, so following another speaker is a restart.
		serverFile, clientID := filepath.Join(home, "multiroom-server"), os.Getenv("SOUNDTAP_SNAPCLIENT_ID")

		wg.Go(func() {
			wanted := func() bool { return snapclientWanted(serverFile, source(), anchor.Wanted()) }
			supervise.RunWhileWanted(ctx, wanted, snapclientPoke, func(ctx context.Context) {
				supervise.Run(ctx, log, mediaDaemons, snapclient, daemon(snapclient, "nice", func() []string {
					return snapclientArgs(serverFile, clientID, source())
				}))
			}, func() { log.Info("snapclient parked: no group to anchor or follow") })
		})
	} else {
		log.Info("snapcast skipped: no snapserver configuration")
	}

	wg.Wait()
}

// mediaNice is the niceness of the media daemons that feed the Mixer or the group but are
// not on the path to the DAC: snapserver and the decoder. On the ST20's one CPU, a
// multiroom leader at full priority let snapserver and ffmpeg take the CPU aplay needed,
// and aplay ran dry ("underrun!!!", heard as a pop, 2026-11-24). Both keep seconds of
// buffer behind them; aplay keeps a ring of a few hundred milliseconds.
const mediaNice = "5"

// snapserverArgs is nice's command line for snapserver.
func snapserverArgs(conf string) []string {
	return []string{"-n", mediaNice, "snapserver", "-c", conf}
}

// snapclientPoke tells the snapclient gate (RunWhileWanted) to look again whether it runs.
//
//nolint:gochecknoglobals // process-wide state: every multiroom change pokes the one snapclient gate
var snapclientPoke = make(chan struct{}, 1)

// pokeSnapclient starts or parks snapclient as snapclientWanted now says. It never blocks.
func pokeSnapclient() {
	select {
	case snapclientPoke <- struct{}{}:
	default: // a poke is pending and will read the state then
	}
}

// anchor is this speaker's demand for its silent group anchor.
//
//nolint:gochecknoglobals // process-wide state: the group code wants it, the snapclient gate reads it
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
// every track start on a Spotify leader (2026-09-29). The stream is FLAC (ADR 0013), so the
// anchor decodes it as every follower does; snapclient has no way to receive without
// decoding. That is libFLAC's cheap direction, estimated at 1-2 % of the CPU, still to be
// measured on a leader.
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
		follower := []string{
			"-n", "0", snapclient, "--player", "file:filename=" + busPath(),
			"--sampleformat", strconv.Itoa(audio.SampleRate) + ":16:*",
		}
		args := make([]string, 0, len(follower)+len(common)+1)
		args = append(args, follower...)
		args = append(args, common...)

		return append(args, "tcp://"+host+":1704")
	}

	silent := []string{"-n", "19", snapclient, "--player", "file:filename=/dev/null"}
	args := make([]string, 0, len(silent)+len(common)+1)
	args = append(args, silent...)
	args = append(args, common...)

	return append(args, "tcp://127.0.0.1:1704")
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
	err := os.Remove(filepath.Join(spotifySystemDir, "credentials.json"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("forget the Spotify login: %w", err)
	}

	receivers.Restart(sourceSpotify)

	return nil
}

// librespotVolumeMax is the top of librespot's --initial-volume, a percentage.
const librespotVolumeMax = 100

func librespotArgs(name string, volume int) []string {
	volume = min(max(volume, 0), librespotVolumeMax)

	return []string{
		"-q", "--name", name, "--device-type", "speaker", "--backend", "alsa",
		"--device", "default", "--disable-gapless", "--mixer", "softvol", "--volume-ctrl", "fixed",
		"--initial-volume", strconv.Itoa(volume),
		"--bitrate", "320", "--disable-audio-cache", "--cache", spotifyDir + "/cache",
		"--system-cache", spotifySystemDir, "--emit-sink-events",
		"--onevent", "/opt/soundtap/spotify-source.sh",
	}
}

const (
	spotifyDrainQuiet = 100 * time.Millisecond
	spotifyDrainMax   = 1500 * time.Millisecond
)

// busQuietPolls is how many times waitBusQuiet looks within quiet.
const busQuietPolls = 4

// waitBusQuiet returns once no source has written to the bus for quiet, or after maxWait.
func waitBusQuiet(health func() audio.Health, quiet, maxWait time.Duration) {
	poll := quiet / busQuietPolls

	last, still := health().SourceBytes, time.Duration(0)
	for end := time.Now().Add(maxWait); time.Now().Before(end) && still < quiet; {
		time.Sleep(poll)

		if b := health().SourceBytes; b != last {
			last, still = b, 0
		} else {
			still += poll
		}
	}
}
