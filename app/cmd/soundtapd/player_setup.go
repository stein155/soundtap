// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/player"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// Hardware paths come from the active model profile (internal/hw/model); an unidentified
// model has none.
var (
	shelbyDev     = board.AudioDev
	shelbySource  = board.ShelbySource
	aicConfigured = board.AICConfigured
	aicConfigFile = board.AICConfigFile
	aicConfig     = board.AICConfig
)

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// resolveAICConfig picks the AIC3256 codec config for the running hardware variant. The
// config name is <variant>_<module>_normal.cfg (rhino_sm2 on ST10, spotty_scm on ST20); the
// kernel exposes variant= on /proc/cmdline. Glob /lib/firmware for the variant's normal config;
// fall back to the model default if cmdline has no variant or the blob isn't shipped.
func resolveAICConfig(fallback string) string {
	b, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return fallback
	}
	variant := ""
	for _, f := range strings.Fields(string(b)) {
		if v, ok := strings.CutPrefix(f, "variant="); ok {
			variant = v
			break
		}
	}
	if variant == "" {
		return fallback
	}
	if m, _ := filepath.Glob("/lib/firmware/" + variant + "_*_normal.cfg"); len(m) > 0 {
		return filepath.Base(m[0])
	}
	return fallback
}

func initAudio(log *slog.Logger) {
	if os.Getenv("AUDIO_NOINIT") == "1" || os.Getenv("AUDIO_EXTERNAL_INIT") == "1" {
		return
	}
	if b, err := os.ReadFile(aicConfigured); err == nil && strings.TrimSpace(string(b)) == "1" {
		return
	}
	_ = hw.SysfsWrite(aicConfigFile, resolveAICConfig(aicConfig), log)
	for range 6 {
		time.Sleep(500 * time.Millisecond)
		if b, err := os.ReadFile(aicConfigured); err == nil && strings.TrimSpace(string(b)) == "1" {
			break
		}
	}
	if shelbySource != "" {
		_ = hw.SysfsWrite(shelbySource, "wifi", log)
	}
}

// announcementsInFlight counts notifications currently being played. It is what lets
// sourceRoute know that PCM has to reach the DAC even though the selected source is
// analogue; playAnnouncement owns both ends of the count.
var announcementsInFlight atomic.Int32

// routeWake tells runSourceRouting the announcement count changed: the speaker state does
// not, so Speaker.Changed alone never woke it for a doorbell over AUX.
var routeWake = make(chan struct{}, 1)

// announcementDemand moves the announcement count by delta and wakes the routing loop.
func announcementDemand(delta int32) {
	announcementsInFlight.Add(delta)
	select {
	case routeWake <- struct{}{}:
	default:
	}
}

func sourceRoute(source string) string {
	// AUX is a hardware route: snd-shelby2 wires the codec to the analogue input and
	// the DAC never sees anything SoundTap writes. That is right for listening to a
	// turntable and wrong for a doorbell -- a notification posted to
	// /api/speaker/notify while AUX was selected decoded, mixed and reached the sink
	// with the counters all moving, and was simply not connected to the amplifier.
	// A notification is the one thing that must always be audible, so it takes the
	// route for as long as it lasts; runSourceRouting, woken by announcementDemand, puts AUX
	// back when the clip ends (announcementDemand wakes it).
	if source == sourceAux && announcementsInFlight.Load() == 0 {
		return "aux"
	}
	return "wifi"
}

// runSourceRouting selects only modes exposed by stock snd-shelby2. AUX physical
// unmute remains governed by PCM-flow safety and therefore awaits hardware validation.
func runSourceRouting(ctx context.Context, sp *speakerstate.Speaker, log *slog.Logger) {
	if _, err := os.Stat(shelbySource); err != nil {
		log.Info("stock source routing unavailable", "path", shelbySource)
		return
	}
	last := ""
	for {
		changed := sp.Changed()
		mode := sourceRoute(sp.StatusSnapshot().Source)
		if mode != last {
			_ = hw.SysfsWrite(shelbySource, mode, log)
			last = mode
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		case <-routeWake:
		}
	}
}

// audioHealth reports cumulative PCM flow, so the player and the watchdog can tell "a
// decoder is running" apart from "audio is actually reaching the DAC". nil on the legacy
// direct-to-device path, where no Mixer exists.
type audioHealth func() audio.Health

// tuneinStreams is the process-wide cache the player reads and player.Prewarm fills.
var tuneinStreams = &player.StreamCache{}

// runPlayer plays radio and UPnP (internal/player): into the source bus under the Mixer,
// straight into the audio device without it.
func runPlayer(ctx context.Context, sp *speakerstate.Speaker, tc player.Resolver, health audioHealth, flush func(), log *slog.Logger) {
	sink := os.Getenv("AUDIO_DEV")
	if sink == "" {
		sink = shelbyDev
	}
	if mixEnabled() {
		sink = busPath()
	}
	initAudio(log)
	player.Run(ctx, sp, tc, player.Config{
		Sink:        sink,
		Mixed:       mixEnabled(),
		Mute:        func(muted bool) { ampLines.mute(muted, log) },
		Health:      health,
		Flush:       flush,
		Receivers:   receivers,
		Streams:     tuneinStreams,
		DecoderUser: decoderUser,
	}, log)
}
