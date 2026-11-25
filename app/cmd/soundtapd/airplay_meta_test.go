// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func tag(s string) string { return hex.EncodeToString([]byte(s)) }

func coreItem(code, text string) string {
	return fmt.Sprintf(`<item><type>%s</type><code>%s</code><data encoding="base64">%s</data></item>`,
		tag("core"), tag(code), base64.StdEncoding.EncodeToString([]byte(text)))
}

func ssncItem(code string) string {
	return fmt.Sprintf(`<item><type>%s</type><code>%s</code></item>`, tag("ssnc"), tag(code))
}

//nolint:paralleltest // changes package-wide state other tests read: airplayRemote
func TestParseAirplayMeta(t *testing.T) {
	ctx := context.Background()

	speaker := speakerstate.New()

	err := speaker.SetSource(ctx, "AIRPLAY")
	if err != nil {
		t.Fatal(err)
	}

	stream := ssncItem("mdst") +
		coreItem("minm", "Song Title") +
		coreItem("asar", "The Artist") +
		coreItem("asal", "The Album") +
		ssncItem("mden")

	parseAirplayMeta(ctx, strings.NewReader(stream), speaker, slog.New(slog.DiscardHandler))

	np, _ := speaker.NowPlaying(ctx)
	if np.Track != "Song Title" || np.Artist != "The Artist" {
		t.Fatalf("airplay meta = track=%q artist=%q, want Song Title / The Artist", np.Track, np.Artist)
	}
}

// A bundle with no title/artist must not clobber existing now-playing with blanks.
//
//nolint:paralleltest // changes package-wide state other tests read: airplayRemote
func TestParseAirplayMetaEmptyBundleNoClobber(t *testing.T) {
	ctx := context.Background()
	speaker := speakerstate.New()
	_ = speaker.SetSource(ctx, "AIRPLAY")
	_ = speaker.SetTrackMeta(ctx, "AIRPLAY", "Keep", "Me", "")

	parseAirplayMeta(ctx, strings.NewReader(ssncItem("mdst")+ssncItem("mden")), speaker, slogDiscard())

	np, _ := speaker.NowPlaying(ctx)
	if np.Track != "Keep" || np.Artist != "Me" {
		t.Fatalf("empty bundle clobbered now-playing: track=%q artist=%q", np.Track, np.Artist)
	}
}

func slogDiscard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// The pvol scale is shairport's, documented in metadata/core.c: 0.00 dB is full
// scale, -30.00 is minimum, -144.00 is mute, and it is linear on the iOS slider.
func TestAirplayVolumePercent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		payload string
		want    int
		ok      bool
	}{
		{"0.00,0.00,0.00,0.00", 100, true},
		{"-30.00,0.00,0.00,0.00", 0, true},
		{"-15.00,0.00,0.00,0.00", 50, true},
		{"-144.00,0.00,0.00,0.00", 0, true}, // mute
		{"-7.50,0.00,0.00,0.00", 75, true},
		{"3.00,0.00,0.00,0.00", 100, true}, // above full scale clamps
		{"", 0, false},
		{"not-a-number,0,0,0", 0, false},
	}
	for _, c := range cases {
		got, ok := airplayVolumePercent(c.payload)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("airplayVolumePercent(%q) = (%d,%v); want (%d,%v)", c.payload, got, ok, c.want, c.ok)
		}
	}
}

// End to end: a sender moving its slider must move our master volume, which is
// what makes the Home app, Control Center and the phone's volume keys work while
// shairport itself stays at ignore_volume_control.
//
//nolint:paralleltest // changes package-wide state other tests read: airplayRemote
func TestParseAirplayMetaAppliesSenderVolume(t *testing.T) {
	ctx := context.Background()

	speaker := speakerstate.New()

	err := speaker.SetSource(ctx, "AIRPLAY")
	if err != nil {
		t.Fatal(err)
	}

	err = speaker.SetVolume(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}

	stream := fmt.Sprintf(`<item><type>%s</type><code>%s</code><data encoding="base64">%s</data></item>`,
		tag("ssnc"), tag("pvol"),
		base64.StdEncoding.EncodeToString([]byte("-15.00,0.00,0.00,0.00")))

	parseAirplayMeta(ctx, strings.NewReader(stream), speaker, slog.New(slog.DiscardHandler))

	if got := speaker.StatusSnapshot().Volume; got != 50 {
		t.Fatalf("volume after pvol -15.00 dB = %d, want 50", got)
	}
}

// A pvol read after another source took over must not move that source's volume.
//
//nolint:paralleltest // changes package-wide state other tests read: airplayRemote
func TestParseAirplayMetaIgnoresSenderVolumeAfterSourceSwitch(t *testing.T) {
	ctx := context.Background()
	speaker := speakerstate.New()
	_ = speaker.SetSource(ctx, "AIRPLAY")
	_ = speaker.SetVolume(ctx, 14)
	_ = speaker.SetSource(ctx, "SPOTIFY")
	stream := fmt.Sprintf(`<item><type>%s</type><code>%s</code><data encoding="base64">%s</data></item>`,
		tag("ssnc"), tag("pvol"), base64.StdEncoding.EncodeToString([]byte("0.00,0,0,0")))

	parseAirplayMeta(ctx, strings.NewReader(stream), speaker, slogDiscard())

	if got := speaker.StatusSnapshot().Volume; got != 14 {
		t.Fatalf("volume after stale pvol = %d, want 14", got)
	}
}

// A pause from the sender has to reach /api/now, and must not touch a source that took over.
//
//nolint:paralleltest // changes package-wide state other tests read: airplayRemote
func TestParseAirplayMetaFollowsSenderPause(t *testing.T) {
	ctx := context.Background()
	discard := slog.New(slog.DiscardHandler)

	speaker := speakerstate.New()

	err := speaker.SetSource(ctx, "AIRPLAY")
	if err != nil {
		t.Fatal(err)
	}

	status := func(code string) string {
		parseAirplayMeta(ctx, strings.NewReader(ssncItem(code)), speaker, discard)

		return speaker.StatusSnapshot().PlayStatus
	}
	for _, testCase := range []struct{ code, want string }{
		{"pfls", statusPaused},
		{"prsm", "PLAY_STATE"},
		{"paus", statusPaused},
		{"pres", "PLAY_STATE"},
		{"pfls", statusPaused},
		{"pffr", "PLAY_STATE"},
	} {
		if got := status(testCase.code); got != testCase.want {
			t.Errorf("after %s: %s, want %s", testCase.code, got, testCase.want)
		}
	}

	err = speaker.SetSource(ctx, "TUNEIN")
	if err != nil {
		t.Fatal(err)
	}

	before := speaker.StatusSnapshot().PlayStatus
	if got := status("pfls"); got != before {
		t.Errorf("a late flush changed the source that took over: %s, was %s", got, before)
	}
}

func TestAirplayProgress(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		in       string
		pos, dur int
		ok       bool
	}{
		{"1000/45100/2206000", 1000, 50000, true},
		{"4294967196/44000/2204900", 1000, 50000, true}, // wrapped past 32 bits
		{"1/2", 0, 0, false},
		{"a/b/c", 0, 0, false},
	} {
		pos, dur, ok := airplayProgress(testCase.in)
		if pos != testCase.pos || dur != testCase.dur || ok != testCase.ok {
			t.Errorf("%q = %d, %d, %v", testCase.in, pos, dur, ok)
		}
	}
}
