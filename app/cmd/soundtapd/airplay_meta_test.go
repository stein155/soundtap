// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
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

func TestParseAirplayMeta(t *testing.T) {
	ctx := context.Background()
	sp := speakerstate.New()
	if err := sp.SetSource(ctx, "AIRPLAY"); err != nil {
		t.Fatal(err)
	}

	stream := ssncItem("mdst") +
		coreItem("minm", "Song Title") +
		coreItem("asar", "The Artist") +
		coreItem("asal", "The Album") +
		ssncItem("mden")

	parseAirplayMeta(ctx, strings.NewReader(stream), sp, slog.New(slog.NewTextHandler(io.Discard, nil)))

	np, _ := sp.NowPlaying(ctx)
	if np.Track != "Song Title" || np.Artist != "The Artist" {
		t.Fatalf("airplay meta = track=%q artist=%q, want Song Title / The Artist", np.Track, np.Artist)
	}
}

// A bundle with no title/artist must not clobber existing now-playing with blanks.
func TestParseAirplayMetaEmptyBundleNoClobber(t *testing.T) {
	ctx := context.Background()
	sp := speakerstate.New()
	_ = sp.SetSource(ctx, "AIRPLAY")
	_ = sp.SetTrackMeta(ctx, "AIRPLAY", "Keep", "Me", "")

	parseAirplayMeta(ctx, strings.NewReader(ssncItem("mdst")+ssncItem("mden")), sp, slogDiscard())

	np, _ := sp.NowPlaying(ctx)
	if np.Track != "Keep" || np.Artist != "Me" {
		t.Fatalf("empty bundle clobbered now-playing: track=%q artist=%q", np.Track, np.Artist)
	}
}

func slogDiscard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// The pvol scale is shairport's, documented in metadata/core.c: 0.00 dB is full
// scale, -30.00 is minimum, -144.00 is mute, and it is linear on the iOS slider.
func TestAirplayVolumePercent(t *testing.T) {
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
func TestParseAirplayMetaAppliesSenderVolume(t *testing.T) {
	ctx := context.Background()
	sp := speakerstate.New()
	if err := sp.SetSource(ctx, "AIRPLAY"); err != nil {
		t.Fatal(err)
	}
	if err := sp.SetVolume(ctx, 10); err != nil {
		t.Fatal(err)
	}
	stream := fmt.Sprintf(`<item><type>%s</type><code>%s</code><data encoding="base64">%s</data></item>`,
		tag("ssnc"), tag("pvol"),
		base64.StdEncoding.EncodeToString([]byte("-15.00,0.00,0.00,0.00")))

	parseAirplayMeta(ctx, strings.NewReader(stream), sp, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if got := sp.StatusSnapshot().Volume; got != 50 {
		t.Fatalf("volume after pvol -15.00 dB = %d, want 50", got)
	}
}

// A pvol read after another source took over must not move that source's volume.
func TestParseAirplayMetaIgnoresSenderVolumeAfterSourceSwitch(t *testing.T) {
	ctx := context.Background()
	sp := speakerstate.New()
	_ = sp.SetSource(ctx, "AIRPLAY")
	_ = sp.SetVolume(ctx, 14)
	_ = sp.SetSource(ctx, "SPOTIFY")
	stream := fmt.Sprintf(`<item><type>%s</type><code>%s</code><data encoding="base64">%s</data></item>`,
		tag("ssnc"), tag("pvol"), base64.StdEncoding.EncodeToString([]byte("0.00,0,0,0")))

	parseAirplayMeta(ctx, strings.NewReader(stream), sp, slogDiscard())

	if got := sp.StatusSnapshot().Volume; got != 14 {
		t.Fatalf("volume after stale pvol = %d, want 14", got)
	}
}

// A pause from the sender has to reach /api/now, and must not touch a source that took over.
func TestParseAirplayMetaFollowsSenderPause(t *testing.T) {
	ctx := context.Background()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	sp := speakerstate.New()
	if err := sp.SetSource(ctx, "AIRPLAY"); err != nil {
		t.Fatal(err)
	}
	status := func(code string) string {
		parseAirplayMeta(ctx, strings.NewReader(ssncItem(code)), sp, discard)
		return sp.StatusSnapshot().PlayStatus
	}
	for _, c := range []struct{ code, want string }{
		{"pfls", statusPaused}, {"prsm", "PLAY_STATE"},
		{"paus", statusPaused}, {"pres", "PLAY_STATE"},
		{"pfls", statusPaused}, {"pffr", "PLAY_STATE"},
	} {
		if got := status(c.code); got != c.want {
			t.Errorf("after %s: %s, want %s", c.code, got, c.want)
		}
	}
	if err := sp.SetSource(ctx, "TUNEIN"); err != nil {
		t.Fatal(err)
	}
	before := sp.StatusSnapshot().PlayStatus
	if got := status("pfls"); got != before {
		t.Errorf("a late flush changed the source that took over: %s, was %s", got, before)
	}
}

func TestAirplayProgress(t *testing.T) {
	for _, c := range []struct {
		in       string
		pos, dur int
		ok       bool
	}{
		{"1000/45100/2206000", 1000, 50000, true},
		{"4294967196/44000/2204900", 1000, 50000, true}, // wrapped past 32 bits
		{"1/2", 0, 0, false},
		{"a/b/c", 0, 0, false},
	} {
		pos, dur, ok := airplayProgress(c.in)
		if pos != c.pos || dur != c.dur || ok != c.ok {
			t.Errorf("%q = %d, %d, %v", c.in, pos, dur, ok)
		}
	}
}
