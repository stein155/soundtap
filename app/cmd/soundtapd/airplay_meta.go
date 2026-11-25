// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"io"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// AirPlay now-playing metadata. shairport-sync writes a stream of <item> XML blocks to
// its metadata pipe; soundtapd reads them and pushes track/artist to speakerstate so
// /api/now shows the AirPlay track like it does for Spotify. Each metadata bundle is
// bracketed by ssnc mdst/mden; the track fields live in core minm/asar/asal items.
const airplayMetaPipe = "/tmp/shairport-sync-metadata"

type mdItem struct {
	Type string `xml:"type"`
	Code string `xml:"code"`
	Data mdData `xml:"data"`
}

// mdData is an item's payload and how shairport encoded it.
type mdData struct {
	Encoding string `xml:"encoding,attr"`
	Value    string `xml:",chardata"`
}

const (
	// airplayMetaRetry is how long runAirplayMeta waits for shairport to create its pipe.
	airplayMetaRetry = 3 * time.Second
	// airplayMetaSpin is the shortest session a FIFO gives; a quicker EOF means the path
	// is not a FIFO.
	airplayMetaSpin = 100 * time.Millisecond
)

// runAirplayMeta reopens the metadata pipe for the life of ctx (shairport closes its
// write end between sessions, giving EOF) and parses each stream.
func runAirplayMeta(ctx context.Context, speaker *speakerstate.Speaker, log *slog.Logger) {
	path := airplayMetaPipe
	for ctx.Err() == nil {
		file, err := os.OpenFile(path, os.O_RDONLY, 0) // blocks until shairport opens the write end
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(airplayMetaRetry): // pipe not created yet — retry
			}

			continue
		}

		start := time.Now()

		parseAirplayMeta(ctx, file, speaker, log)
		_ = file.Close()
		// Opening a FIFO blocks until a writer appears, so the normal loop is cheap.
		// If the path is not a FIFO (a leftover regular file), open returns instantly
		// and the parse hits EOF instantly — back off so that cannot become a spin.
		if time.Since(start) < airplayMetaSpin && !sleepCtx(ctx, time.Second) {
			return
		}
	}
}

// decodeTag turns shairport's 8-hex-digit type/code (ASCII bytes) into its string, e.g.
// "636f7265" -> "core", "6d696e6d" -> "minm".
func decodeTag(s string) string {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return ""
	}

	return string(b)
}

// itemPayload returns an item's data, decoding the base64 shairport uses for
// anything that is not plain ASCII.
func itemPayload(it mdItem) string {
	val := it.Data.Value
	if strings.EqualFold(it.Data.Encoding, "base64") {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(val))
		if err == nil {
			return string(b)
		}
	}

	return val
}

// AirPlay's volume scale, from shairport-sync's metadata/core.c: `pvol` carries
// "airplay_volume,volume,lowest,highest", where airplay_volume runs from 0.00 at
// full scale down to -30.00 at minimum and is linear on the iOS slider. -144.00
// means mute, which clamps to the bottom of the range and so needs no special case.
const (
	airplayVolumeMinDB = -30.0
	airplayVolumeMaxDB = 0.0
	// airplayVolumeTop is the master volume at AirPlay's full scale.
	airplayVolumeTop = 100
)

// airplayVolumePercent maps a `pvol` payload onto our 0..100 master volume.
//
// We keep shairport's ignore_volume_control = "yes" — the mixer must stay the one
// place volume is applied, or AirPlay would attenuate before the mixer does it
// again — so this is the feedback path that makes the sender's slider mean
// something. Without it the Home app, Control Center and the phone's volume keys
// all move a control that does nothing.
func airplayVolumePercent(payload string) (int, bool) {
	first, _, _ := strings.Cut(payload, ",")

	decibels, err := strconv.ParseFloat(strings.TrimSpace(first), 64)
	if err != nil {
		return 0, false
	}

	if decibels < airplayVolumeMinDB {
		decibels = airplayVolumeMinDB
	}

	if decibels > airplayVolumeMaxDB {
		decibels = airplayVolumeMaxDB
	}

	share := (decibels - airplayVolumeMinDB) / (airplayVolumeMaxDB - airplayVolumeMinDB)

	return int(math.Round(share * airplayVolumeTop)), true
}

// airplayRate is the clock of AirPlay's RTP timestamps.
const airplayRate = 44100

// airplayProgressFields are the start, current and end timestamps of a `prgr` payload.
const airplayProgressFields = 3

// airplayProgress reads a `prgr` payload, the "start/current/end" RTP timestamps of the track,
// as position and duration in milliseconds, and whether it parsed. The timestamps are 32
// bits and may wrap.
func airplayProgress(payload string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(payload), "/")
	if len(parts) != airplayProgressFields {
		return 0, 0, false
	}

	var progress [airplayProgressFields]uint32

	for index, p := range parts {
		v, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return 0, 0, false
		}

		progress[index] = uint32(v)
	}

	toMs := func(frames uint32) int {
		return int(uint64(frames) * 1000 / airplayRate) //nolint:gosec // at most 97 million
	}

	return toMs(progress[1] - progress[0]), toMs(progress[2] - progress[0]), true
}

// airplayBundle is the track a metadata bundle names, published at its end.
type airplayBundle struct {
	title, artist, album string
}

// parseAirplayMeta streams <item> elements and calls SetTrackMeta once per bundle.
func parseAirplayMeta(ctx context.Context, r io.Reader, speaker *speakerstate.Speaker, log *slog.Logger) {
	dec := xml.NewDecoder(r)

	var bundle airplayBundle

	for ctx.Err() == nil {
		var item mdItem

		err := dec.Decode(&item)
		if err != nil {
			return // EOF (shairport closed the session) or malformed tail
		}

		typ, code := decodeTag(item.Type), decodeTag(item.Code)
		switch typ {
		case "ssnc":
			if !airplaySessionItem(ctx, speaker, code, item, &bundle, log) {
				return
			}
		case "core":
			airplayTrackItem(code, itemPayload(item), &bundle)
		default: // other item types carry nothing the speaker shows
		}
	}
}

// airplaySessionItem handles one of shairport's own (ssnc) items: a bundle's start or
// end, pause and resume, the remote control, progress and volume. It returns false when
// the speaker refused a volume, which ends the stream.
func airplaySessionItem(
	ctx context.Context, speaker *speakerstate.Speaker, code string, item mdItem, bundle *airplayBundle,
	log *slog.Logger,
) bool {
	switch code {
	case "mdst": // metadata bundle start — reset
		*bundle = airplayBundle{title: "", artist: "", album: ""}
	case "mden": // metadata bundle end — publish
		if bundle.title != "" || bundle.artist != "" {
			_ = speaker.SetTrackMeta(ctx, sourceAirPlay, bundle.title, bundle.artist, "")
			speaker.SetAlbum(sourceAirPlay, bundle.album)
			log.Info("airplay meta", "title", bundle.title, "artist", bundle.artist)
		}
	case "pfls", "paus": // the sender paused; a skip or seek flushes too, and resumes at once
		speaker.SetPaused(sourceAirPlay, true)
	case "prsm", "pres", "pffr": // audio is flowing again
		speaker.SetPaused(sourceAirPlay, false)
	case "daid", "acre": // the sender's remote control, for the speaker's keys
		airplayRemote.note(code, itemPayload(item))
	case "prgr": // where the track is, on a start, a resume and a seek
		pos, dur, ok := airplayProgress(itemPayload(item))
		if ok {
			speaker.SetPosition(sourceAirPlay, pos, dur)
		}
	case "pvol": // the sender moved its volume slider
		return airplayVolumeItem(ctx, speaker, itemPayload(item), log)
	default: // other session items are not used
	}

	return true
}

// airplayVolumeItem applies the sender's volume slider; false when the speaker refused it.
func airplayVolumeItem(ctx context.Context, speaker *speakerstate.Speaker, payload string, log *slog.Logger) bool {
	// A pvol read after another source took over is stale; it must not
	// move that source's volume. Standby stays open: a session's first
	// pvol can land before the source flips to AIRPLAY.
	src := speaker.StatusSnapshot().Source
	if src != sourceAirPlay && src != "STANDBY" {
		return true
	}

	pct, ok := airplayVolumePercent(payload)
	if !ok {
		return true
	}

	err := speaker.SetVolume(ctx, pct)
	if err != nil {
		return false
	}

	log.Debug("airplay volume", "percent", pct)

	return true
}

// airplayTrackItem keeps a core item's track field for the bundle's end.
func airplayTrackItem(code, val string, bundle *airplayBundle) {
	switch code {
	case "minm": // item name = track title
		bundle.title = val
	case "asar": // artist
		bundle.artist = val
	case "asal": // album
		bundle.album = val
	default: // other core items are not shown
	}
}
