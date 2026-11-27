// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/radiobrowser"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/tunein"
	"github.com/stein155/soundtap/app/internal/web"
)

// The routes a stock Radio Browser preset is taken over by, as the log names them.
const (
	routeUUID           = "radio-browser-uuid"
	routeTuneInName     = "tunein-name"
	routeRadioBrowserBy = "radio-browser-name"
)

// nameHit is a station found by a preset's name.
type nameHit struct {
	route    string
	station  string // the TuneIn guide id or the Radio Browser uuid, for the log
	location string // what the preset plays
	logo     string
}

// takeOverByName stores a preset whose uuid Radio Browser no longer knows (stations are
// added again under a new uuid) by the preset's name: TuneIn's one station of that name,
// else Radio Browser's. Only the name is sent. It reports whether the preset still waits:
// when a search did not answer it is tried again in the next round, until its week is up.
func (l *stockLookups) takeOverByName(
	ctx context.Context, log *slog.Logger, preset *pendingStockPreset, now time.Time,
) bool {
	hit, found, failed := l.findByName(ctx, log, preset)
	if !found {
		if failed != nil {
			return retryOrGiveUp(log, preset, now, failed)
		}

		log.Info("stock preset not taken over: Radio Browser knows no such station, "+
			"and neither TuneIn nor Radio Browser has one station by its name",
			"uuid", preset.UUID, "tried", strings.Join([]string{routeUUID, routeTuneInName, routeRadioBrowserBy}, ","))

		return false
	}

	logo := preset.Logo
	if logo == "" && speaker.CheckPreset(preset.Name, hit.logo) == nil {
		logo = hit.logo
	}

	stored, err := l.store(ctx, preset.Slot, hit.location, preset.Name, logo)

	switch {
	case err != nil:
		return retryOrGiveUp(log, preset, now, err)
	case !stored:
		log.Info("stock preset not taken over: the slot holds a preset already")
	default:
		log.Info("stock preset taken over by its name", "route", hit.route, "station", hit.station,
			"uuid", preset.UUID)
	}

	return false
}

// findByName looks for the preset's station by its name, on TuneIn, then on Radio Browser.
// failed is what kept a search from answering, nil when both answered.
func (l *stockLookups) findByName(
	ctx context.Context, log *slog.Logger, preset *pendingStockPreset,
) (nameHit, bool, error) {
	var failed error

	stations, err := l.searchTuneIn(ctx, preset.Name)
	if err != nil {
		log.Info("TuneIn search by the preset's name failed", "err", err)

		failed = err
	} else if station, ok := tuneInMatch(stations, preset.Name); ok {
		return nameHit{
			route: routeTuneInName, station: station.ID, location: speaker.StationLocation(station.ID),
			logo: station.Logo,
		}, true, nil
	}

	if ctx.Err() != nil {
		return nameHit{route: "", station: "", location: "", logo: ""}, false, errors.Join(failed, ctx.Err())
	}

	listed, err := l.searchByName(ctx, preset.Name)
	if err != nil {
		log.Info("Radio Browser search by the preset's name failed", "err", err)

		return nameHit{route: "", station: "", location: "", logo: ""}, false, errors.Join(failed, err)
	}

	if station, ok := radioBrowserMatch(listed, preset.Name, l.country()); ok {
		return nameHit{
			route: routeRadioBrowserBy, station: station.UUID, location: station.StreamURL(),
			logo: strings.TrimSpace(station.Favicon),
		}, true, nil
	}

	return nameHit{route: "", station: "", location: "", logo: ""}, false, failed
}

// sameName reports whether two station names are the same, ignoring case and spacing.
func sameName(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}

// tuneInMatch is TuneIn's one station named name. TuneIn's search results name no country,
// so several stations of that name are no match.
func tuneInMatch(stations []tunein.Station, name string) (tunein.Station, bool) {
	var match tunein.Station

	found := false

	for _, station := range stations {
		if !sameName(station.Name, name) || !web.ValidGuideID(station.ID) {
			continue
		}

		if found && station.ID != match.ID {
			return tunein.Station{ID: "", Name: "", Logo: "", Bitrate: "", NowPlaying: ""}, false
		}

		match, found = station, true
	}

	return match, found
}

// radioBrowserMatch is Radio Browser's one station named name with a stream a preset may
// hold: of several, those of the speaker's country when there are any, then those Radio
// Browser last found playing when there are any. Stations left that play different streams
// are no match; the same station entered twice is.
func radioBrowserMatch(stations []radiobrowser.Station, name, country string) (radiobrowser.Station, bool) {
	var none radiobrowser.Station

	candidates := make([]radiobrowser.Station, 0, len(stations))

	for _, station := range stations {
		if sameName(station.Name, name) && web.ValidStreamURL(station.StreamURL()) {
			candidates = append(candidates, station)
		}
	}

	if country != "" {
		candidates = preferred(candidates, func(s *radiobrowser.Station) bool {
			return strings.EqualFold(s.CountryCode, country)
		})
	}

	candidates = preferred(candidates, func(s *radiobrowser.Station) bool { return s.LastCheckOK == 1 })

	if len(candidates) == 0 {
		return none, false
	}

	for index := range candidates[1:] {
		if candidates[index+1].StreamURL() != candidates[0].StreamURL() {
			return none, false
		}
	}

	return candidates[0], true
}

// preferred is the stations that are ok, or all of them when none is.
func preferred(stations []radiobrowser.Station, ok func(*radiobrowser.Station) bool) []radiobrowser.Station {
	out := make([]radiobrowser.Station, 0, len(stations))

	for index := range stations {
		if ok(&stations[index]) {
			out = append(out, stations[index])
		}
	}

	if len(out) == 0 {
		return stations
	}

	return out
}
