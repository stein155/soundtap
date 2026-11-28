// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"cmp"
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

	if station, ok := radioBrowserMatch(listed, preset.Name, preset.Logo, l.country()); ok {
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
// only a bit rate and a reliability, so several stations of that name are no match: ranking
// them could pick another country's station.
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
// hold. Of several, each step keeps those that pass it when there are any:
//
//  1. those whose logo is stock's own logo for the preset, logo (the entry stock stored);
//  2. those of the speaker's country;
//  3. those Radio Browser last found playing.
//
// Stations left that all play one stream are the same station entered twice: that one. Stations
// left that play different streams are told apart only when they are all of one country, so
// all the same broadcaster's entries: the one with the most votes, then clicks, then the
// highest bit rate. Stations of different or unknown countries, or that tie on all three, are
// no match.
func radioBrowserMatch(stations []radiobrowser.Station, name, logo, country string) (radiobrowser.Station, bool) {
	var none radiobrowser.Station

	candidates := make([]radiobrowser.Station, 0, len(stations))

	for index := range stations {
		if sameName(stations[index].Name, name) && web.ValidStreamURL(stations[index].StreamURL()) {
			candidates = append(candidates, stations[index])
		}
	}

	if logo = strings.TrimSpace(logo); logo != "" {
		candidates = preferred(candidates, func(s *radiobrowser.Station) bool {
			return strings.TrimSpace(s.Favicon) == logo
		})
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

	if oneStream(candidates) {
		return candidates[0], true
	}

	if !oneCountry(candidates) {
		return none, false
	}

	return mostListened(candidates)
}

// oneStream reports whether the stations all play the same stream.
func oneStream(stations []radiobrowser.Station) bool {
	for index := range stations[1:] {
		if stations[index+1].StreamURL() != stations[0].StreamURL() {
			return false
		}
	}

	return true
}

// oneCountry reports whether the stations are all of one known country.
func oneCountry(stations []radiobrowser.Station) bool {
	for index := range stations {
		if strings.TrimSpace(stations[index].CountryCode) == "" ||
			!strings.EqualFold(stations[index].CountryCode, stations[0].CountryCode) {
			return false
		}
	}

	return true
}

// mostListened is the station with the most votes, then clicks, then the highest bit rate;
// none when another with a different stream ties with it on all three.
func mostListened(stations []radiobrowser.Station) (radiobrowser.Station, bool) {
	var none radiobrowser.Station

	rank := func(a, b *radiobrowser.Station) int {
		return cmp.Or(cmp.Compare(b.Votes, a.Votes), cmp.Compare(b.ClickCount, a.ClickCount),
			cmp.Compare(b.Bitrate, a.Bitrate))
	}

	best := 0

	for index := range stations {
		if rank(&stations[index], &stations[best]) < 0 {
			best = index
		}
	}

	for index := range stations {
		if rank(&stations[index], &stations[best]) == 0 && stations[index].StreamURL() != stations[best].StreamURL() {
			return none, false
		}
	}

	return stations[best], true
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
