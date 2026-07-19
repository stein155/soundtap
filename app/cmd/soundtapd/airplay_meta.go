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
	Data struct {
		Encoding string `xml:"encoding,attr"`
		Value    string `xml:",chardata"`
	} `xml:"data"`
}

// runAirplayMeta reopens the metadata pipe for the life of ctx (shairport closes its
// write end between sessions, giving EOF) and parses each stream.
func runAirplayMeta(ctx context.Context, sp *speakerstate.Speaker, log *slog.Logger) {
	path := airplayMetaPipe
	for ctx.Err() == nil {
		f, err := os.OpenFile(path, os.O_RDONLY, 0) // blocks until shairport opens the write end
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second): // pipe not created yet — retry
			}
			continue
		}
		start := time.Now()
		parseAirplayMeta(ctx, f, sp, log)
		_ = f.Close()
		// Opening a FIFO blocks until a writer appears, so the normal loop is cheap.
		// If the path is not a FIFO (a leftover regular file), open returns instantly
		// and the parse hits EOF instantly — back off so that cannot become a spin.
		if time.Since(start) < 100*time.Millisecond && !sleepCtx(ctx, time.Second) {
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
		if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(val)); err == nil {
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
	db, err := strconv.ParseFloat(strings.TrimSpace(first), 64)
	if err != nil {
		return 0, false
	}
	if db < airplayVolumeMinDB {
		db = airplayVolumeMinDB
	}
	if db > airplayVolumeMaxDB {
		db = airplayVolumeMaxDB
	}
	return int(math.Round((db - airplayVolumeMinDB) / (airplayVolumeMaxDB - airplayVolumeMinDB) * 100)), true
}

// airplayRate is the clock of AirPlay's RTP timestamps.
const airplayRate = 44100

// airplayProgress reads a `prgr` payload, the "start/current/end" RTP timestamps of the track,
// as position and duration in milliseconds. The timestamps are 32 bits and may wrap.
func airplayProgress(payload string) (positionMs, durationMs int, ok bool) {
	parts := strings.Split(strings.TrimSpace(payload), "/")
	if len(parts) != 3 {
		return 0, 0, false
	}
	var at [3]uint32
	for i, p := range parts {
		v, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return 0, 0, false
		}
		at[i] = uint32(v)
	}
	toMs := func(frames uint32) int { return int(uint64(frames) * 1000 / airplayRate) } //nolint:gosec // at most 97 million
	return toMs(at[1] - at[0]), toMs(at[2] - at[0]), true
}

// parseAirplayMeta streams <item> elements and calls SetTrackMeta once per bundle.
func parseAirplayMeta(ctx context.Context, r io.Reader, sp *speakerstate.Speaker, log *slog.Logger) {
	dec := xml.NewDecoder(r)
	var title, artist, album string
	for ctx.Err() == nil {
		var it mdItem
		if err := dec.Decode(&it); err != nil {
			return // EOF (shairport closed the session) or malformed tail
		}
		typ, code := decodeTag(it.Type), decodeTag(it.Code)
		if typ == "ssnc" {
			switch code {
			case "mdst": // metadata bundle start — reset
				title, artist, album = "", "", ""
			case "mden": // metadata bundle end — publish
				if title != "" || artist != "" {
					_ = sp.SetTrackMeta(ctx, sourceAirPlay, title, artist, "")
					sp.SetAlbum(sourceAirPlay, album)
					log.Info("airplay meta", "title", title, "artist", artist)
				}
			case "pfls", "paus": // the sender paused; a skip or seek flushes too, and resumes at once
				sp.SetPaused(sourceAirPlay, true)
			case "prsm", "pres", "pffr": // audio is flowing again
				sp.SetPaused(sourceAirPlay, false)
			case "daid", "acre": // the sender's remote control, for the speaker's keys
				airplayRemote.note(code, itemPayload(it))
			case "prgr": // where the track is, on a start, a resume and a seek
				if pos, dur, ok := airplayProgress(itemPayload(it)); ok {
					sp.SetPosition(sourceAirPlay, pos, dur)
				}
			case "pvol": // the sender moved its volume slider
				// A pvol read after another source took over is stale; it must not
				// move that source's volume. Standby stays open: a session's first
				// pvol can land before the source flips to AIRPLAY.
				if src := sp.StatusSnapshot().Source; src != sourceAirPlay && src != "STANDBY" {
					continue
				}
				if pct, ok := airplayVolumePercent(itemPayload(it)); ok {
					if err := sp.SetVolume(ctx, pct); err != nil {
						return
					}
					log.Debug("airplay volume", "percent", pct)
				}
			}
			continue
		}
		if typ != "core" {
			continue
		}
		val := itemPayload(it)
		switch code {
		case "minm": // item name = track title
			title = val
		case "asar": // artist
			artist = val
		case "asal": // album
			album = val
		}
	}
}
