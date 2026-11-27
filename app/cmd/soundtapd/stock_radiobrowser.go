// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/radiobrowser"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/web"
)

// sourceRadioBrowser is the source stock gives a preset from Radio Browser.
const sourceRadioBrowser = "RADIO_BROWSER"

// stockPendingName is the file in soundtapd's home that holds stock's Radio Browser
// presets until their stream address is looked up (ADR 0129).
const stockPendingName = "stock-presets-pending.json"

// The pending file's format, and how the lookups are paced.
const (
	stockPendingVersion = 1
	stockPendingMode    = 0o600
	// stockPendingMaxAge is how long a preset is looked up for before it is given up.
	stockPendingMaxAge = 7 * 24 * time.Hour
	// stockLookupFirstRetry is the wait after the first round that left presets waiting;
	// it doubles up to stockLookupRetry. There is no signal for "the network is up", and
	// on the first start the network comes up some time after the takeover.
	stockLookupFirstRetry = 30 * time.Second
	stockLookupRetry      = 10 * time.Minute
	stockLookupGrowth     = 2
)

// saneClockFloor is the earliest time a speaker's clock is believed: before NTP it may
// read 1970, and a preset first seen then would be given up at once.
//
//nolint:gochecknoglobals // a constant time.Time
var saneClockFloor = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// pendingStockPreset is one of stock's Radio Browser presets waiting for its lookup.
type pendingStockPreset struct {
	Slot int    `json:"slot"`
	UUID string `json:"uuid"`
	Name string `json:"name"`
	Logo string `json:"logo,omitempty"`
	// FirstSeen is when the takeover wrote it down; zero while the clock was not yet set.
	FirstSeen time.Time `json:"firstSeen,omitzero"`
}

// pendingStockFile is the pending file.
type pendingStockFile struct {
	Version int                  `json:"version"`
	Presets []pendingStockPreset `json:"presets"`
}

// errPendingVersion is a pending file of a format this build does not know.
var errPendingVersion = errors.New("unknown version")

// recordPendingStockPresets writes stock's Radio Browser presets down for the lookups. A
// preset already waiting for the same station keeps the time it was first seen, so a
// takeover that runs again does not restart its week.
func recordPendingStockPresets(path string, pending []pendingStockPreset, now time.Time, log *slog.Logger) {
	if len(pending) == 0 {
		return
	}

	before, _ := readPendingStockPresets(path)

	out := make([]pendingStockPreset, 0, len(pending))
	for _, preset := range pending {
		preset.FirstSeen = time.Time{}
		if !now.Before(saneClockFloor) {
			preset.FirstSeen = now.UTC()
		}

		for _, old := range before.Presets {
			if old.Slot == preset.Slot && old.UUID == preset.UUID && !old.FirstSeen.IsZero() {
				preset.FirstSeen = old.FirstSeen
			}
		}

		out = append(out, preset)
	}

	err := atomicjson.Write(path, pendingStockFile{Version: stockPendingVersion, Presets: out}, stockPendingMode)
	if err != nil {
		log.Warn("stock presets for Radio Browser not written down", "path", path, "err", err)
	}
}

// readPendingStockPresets reads the pending file: found false for no file.
func readPendingStockPresets(path string) (pendingStockFile, error) {
	var file pendingStockFile

	data, found, err := atomicjson.Read(path)
	if err != nil || !found {
		return file, err //nolint:wrapcheck // Read names the path and the cause
	}

	err = json.Unmarshal(data, &file)
	if err != nil {
		return pendingStockFile{Version: 0, Presets: nil}, fmt.Errorf("parse %s: %w", path, err)
	}

	if file.Version != stockPendingVersion {
		return file, fmt.Errorf("%s: %w %d", path, errPendingVersion, file.Version)
	}

	return file, nil
}

// stockLookups stores stock's Radio Browser presets once their stream address is known.
type stockLookups struct {
	path string
	// resolve looks a station up (radiobrowser.Client.Station).
	resolve func(ctx context.Context, uuid string) (radiobrowser.Station, error)
	// slotHeld reports whether a slot holds a preset, so a held one is not looked up.
	slotHeld func(ctx context.Context, slot int) bool
	// store stores a preset in a slot only while it is empty (StorePresetIfEmpty).
	store func(ctx context.Context, slot int, stream, name, logo string) (bool, error)
	now   func() time.Time
	log   *slog.Logger
}

// startStockPresetLookups looks stock's Radio Browser presets up in the background while
// any wait, at every start and then with a growing pause, up to every ten minutes.
func startStockPresetLookups(ctx context.Context, home string, state *speakerstate.Speaker, log *slog.Logger) {
	path := filepath.Join(home, stockPendingName)

	_, err := os.Stat(path)
	if err != nil {
		return // nothing waits: the usual case
	}

	client := radiobrowser.New("SoundTap/" + strings.TrimPrefix(version, "app-v"))
	lookups := &stockLookups{
		path:    path,
		resolve: client.Station,
		slotHeld: func(ctx context.Context, slot int) bool {
			presets, err := state.Presets(ctx)
			if err != nil {
				return false // StorePresetIfEmpty checks again
			}

			for _, preset := range presets {
				if preset.Slot == slot {
					return true
				}
			}

			return false
		},
		store: func(ctx context.Context, slot int, stream, name, logo string) (bool, error) {
			return state.StorePresetIfEmpty(ctx, slot, sourceTuneIn, "stationurl", stream, name, logo)
		},
		now: time.Now,
		log: log.With("comp", "stock-presets"),
	}

	goSafe("stockPresetLookups", func() { lookups.run(ctx, sleepCtx) })
}

// run looks up until nothing waits, waiting between rounds with wait.
func (l *stockLookups) run(ctx context.Context, wait func(context.Context, time.Duration) bool) {
	pause := stockLookupFirstRetry

	for l.round(ctx) {
		if !wait(ctx, pause) {
			return
		}

		pause = min(stockLookupGrowth*pause, stockLookupRetry)
	}
}

// load reads the presets that wait: none when nothing waits. A file that does not read is
// logged and removed, a newer format left alone.
func (l *stockLookups) load() []pendingStockPreset {
	file, err := readPendingStockPresets(l.path)

	switch {
	case errors.Is(err, errPendingVersion) && file.Version > stockPendingVersion:
		l.log.Warn("stock presets for Radio Browser: a newer format, left alone", "path", l.path, "err", err)

		return nil
	case errors.Is(err, atomicjson.ErrUnreadable):
		l.log.Warn("stock presets for Radio Browser not read", "path", l.path, "err", err)

		return nil
	case err != nil:
		l.log.Warn("stock presets for Radio Browser not read; removed", "path", l.path, "err", err)
		_ = os.Remove(l.path)

		return nil
	case len(file.Presets) == 0:
		_ = os.Remove(l.path)

		return nil
	}

	return file.Presets
}

// round tries every waiting preset once and reports whether any still waits.
func (l *stockLookups) round(ctx context.Context) bool {
	presets := l.load()
	if len(presets) == 0 {
		return false
	}

	now := l.now()
	waiting := make([]pendingStockPreset, 0, len(presets))
	unreachable, changed := false, false

	for _, preset := range presets {
		if preset.FirstSeen.IsZero() && !now.Before(saneClockFloor) {
			preset.FirstSeen, changed = now.UTC(), true
		}

		if ctx.Err() != nil || unreachable {
			waiting = append(waiting, preset)

			continue
		}

		keep, noServer := l.lookup(ctx, &preset, now)
		if keep {
			waiting = append(waiting, preset)
		}

		unreachable = noServer // the next ones would wait for the same servers
	}

	// Written only when something changed: a week of retries must not rewrite NAND each time.
	if changed || len(waiting) != len(presets) {
		l.save(waiting)
	}

	if len(waiting) > 0 {
		l.log.Info("stock presets wait for Radio Browser", "waiting", len(waiting))
	}

	return len(waiting) > 0 && ctx.Err() == nil
}

// lookup looks one preset up and stores it: keep reports that it still waits, noServer
// that no Radio Browser server answered.
func (l *stockLookups) lookup(ctx context.Context, preset *pendingStockPreset, now time.Time) (bool, bool) {
	log := l.log.With("slot", preset.Slot, "name", preset.Name)

	why := pendingProblem(preset)
	if why != "" {
		log.Warn("stock preset not taken over", "why", why)

		return false, false
	}

	if l.slotHeld(ctx, preset.Slot) {
		log.Info("stock preset not taken over: the slot holds a preset already")

		return false, false
	}

	station, err := l.resolve(ctx, preset.UUID)

	switch {
	case errors.Is(err, radiobrowser.ErrUnknownStation):
		log.Info("stock preset not taken over: Radio Browser knows no such station", "uuid", preset.UUID)

		return false, false
	case err != nil:
		return retryOrGiveUp(log, preset, now, err), errors.Is(err, radiobrowser.ErrNoServer)
	}

	stream := station.StreamURL()
	if !web.ValidStreamURL(stream) {
		log.Info("stock preset not taken over: Radio Browser gives no usable stream address",
			"uuid", preset.UUID, "length", len(stream))

		return false, false
	}

	stored, err := l.store(ctx, preset.Slot, stream, preset.Name, preset.Logo)

	switch {
	case err != nil:
		return retryOrGiveUp(log, preset, now, err), false
	case !stored:
		log.Info("stock preset not taken over: the slot holds a preset already")
	default:
		log.Info("stock preset taken over from Radio Browser")
	}

	return false, false
}

// retryOrGiveUp keeps a preset whose lookup failed for the next round, until it has waited
// stockPendingMaxAge; reports whether it still waits.
func retryOrGiveUp(log *slog.Logger, preset *pendingStockPreset, now time.Time, err error) bool {
	if !preset.FirstSeen.IsZero() && now.Sub(preset.FirstSeen) >= stockPendingMaxAge {
		log.Warn("stock preset given up: Radio Browser did not answer for 7 days",
			"uuid", preset.UUID, "since", preset.FirstSeen, "err", err)

		return false
	}

	log.Info("stock preset lookup failed; tried again later", "err", err)

	return true
}

// pendingProblem is why an entry of the pending file cannot become a preset, "" when it can.
func pendingProblem(preset *pendingStockPreset) string {
	if preset.Slot < 1 || preset.Slot > speaker.PresetSlots {
		return fmt.Sprintf("no preset slot %d", preset.Slot)
	}

	if !radiobrowser.ValidUUID(preset.UUID) {
		return "not a Radio Browser station"
	}

	err := speaker.CheckPreset(preset.Name, preset.Logo)
	if err != nil {
		return err.Error()
	}

	return ""
}

// save writes what still waits back, and removes the file once nothing does.
func (l *stockLookups) save(waiting []pendingStockPreset) {
	if len(waiting) == 0 {
		err := os.Remove(l.path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			l.log.Warn("stock presets for Radio Browser: file not removed", "path", l.path, "err", err)

			return
		}

		l.log.Info("stock presets for Radio Browser: none waits any more")

		return
	}

	err := atomicjson.Write(l.path, pendingStockFile{Version: stockPendingVersion, Presets: waiting}, stockPendingMode)
	if err != nil {
		l.log.Warn("stock presets for Radio Browser not written back", "path", l.path, "err", err)
	}
}
