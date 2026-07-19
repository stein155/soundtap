// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/store"
)

func TestHeldTuneInPresetPersistsAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sp := speakerstate.New()
	sp.OnPresetChange(persistPresetChange(st))
	if err := sp.Select(context.Background(), "TUNEIN", "stationurl", "/v1/playback/station/s6712", "Radio 2", "", "http://logo"); err != nil {
		t.Fatal(err)
	}
	if stored, err := sp.StoreCurrentAsPreset("PRESET_3"); err != nil || !stored {
		t.Fatalf("store = %v, %v", stored, err)
	}

	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := reopened.Get(3)
	if !ok || p.StationID != "s6712" || p.Name != "Radio 2" || p.Logo != "http://logo" {
		t.Fatalf("reopened preset = %+v, %v", p, ok)
	}
}

func TestHeldPresetWriteFailureDoesNotMutateSpeaker(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "missing", "presets.json"))
	if err != nil {
		t.Fatal(err)
	}
	sp := speakerstate.New()
	sp.OnPresetChange(persistPresetChange(st))
	if err := sp.Select(context.Background(), "TUNEIN", "stationurl", "/v1/playback/station/s1", "One", "", "logo"); err != nil {
		t.Fatal(err)
	}
	if stored, err := sp.StoreCurrentAsPreset("PRESET_1"); err == nil || stored {
		t.Fatalf("store = %v, %v; want failed", stored, err)
	}
	if presets, err := sp.Presets(context.Background()); err != nil || len(presets) != 0 {
		t.Fatalf("presets after failed write = %+v, %v", presets, err)
	}
}

func TestHeldUPnPPresetPersistsAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sp := speakerstate.New()
	sp.OnPresetChange(persistPresetChange(st))
	if err := sp.Select(context.Background(), "UPNP", "audioItem", "http://media.local/song.flac", "Song", "", "cover"); err != nil {
		t.Fatal(err)
	}
	if stored, err := sp.StoreCurrentAsPreset("PRESET_2"); err != nil || !stored {
		t.Fatalf("store = %v, %v", stored, err)
	}

	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := reopened.Get(2)
	if !ok || p.Source != "UPNP" || p.Location != "http://media.local/song.flac" || p.Name != "Song" {
		t.Fatalf("reopened preset = %+v, %v", p, ok)
	}
}

// presetNames lists the preset names by slot, from the speaker and from a fresh read of the file.
func presetNames(t *testing.T, sp *speakerstate.Speaker, path string) (live, stored map[int]string) {
	t.Helper()
	live, stored = map[int]string{}, map[int]string{}
	presets, err := sp.Presets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range presets {
		live[p.Slot] = p.Name
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range reopened.All() {
		stored[p.Slot] = p.Name
	}
	return live, stored
}

func speakerWithPresets(t *testing.T, path string, names ...string) *speakerstate.Speaker {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sp := speakerstate.New()
	sp.OnPresetChange(persistPresetChange(st))
	sp.OnPresetSwap(st.Swap)
	for i, name := range names {
		if err := sp.StorePreset(context.Background(), i+1, "TUNEIN", "stationurl", "/v1/playback/station/s"+name, name, ""); err != nil {
			t.Fatal(err)
		}
	}
	return sp
}

// Overlapping swaps may land in either order, but no preset is lost or doubled.
func TestOverlappingPresetSwapsLoseNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	sp := speakerWithPresets(t, path, "A", "B", "C")
	var wg sync.WaitGroup
	for range 50 {
		for _, pair := range [][2]int{{1, 2}, {2, 3}, {3, 4}} {
			wg.Go(func() {
				if err := sp.SwapPresets(context.Background(), pair[0], pair[1]); err != nil {
					t.Error(err)
				}
			})
		}
	}
	wg.Wait()
	live, stored := presetNames(t, sp, path)
	if !maps.Equal(live, stored) {
		t.Fatalf("speaker has %v, file has %v", live, stored)
	}
	got := slices.Sorted(maps.Values(stored))
	if !slices.Equal(got, []string{"A", "B", "C"}) {
		t.Fatalf("presets after the swaps = %v, want A, B and C once each", stored)
	}
}

// A swap whose write fails leaves both slots as they were, on the speaker and on disk.
func TestFailedPresetSwapChangesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	sp := speakerWithPresets(t, path, "A", "B")
	sp.OnPresetSwap(func(int, int) error { return errors.New("flash is full") })
	if err := sp.SwapPresets(context.Background(), 1, 2); err == nil {
		t.Fatal("swap reported success after a failed write")
	}
	live, stored := presetNames(t, sp, path)
	want := map[int]string{1: "A", 2: "B"}
	if !maps.Equal(live, want) || !maps.Equal(stored, want) {
		t.Fatalf("speaker has %v, file has %v, want %v", live, stored, want)
	}
}

// Swapping with an empty slot moves the preset; the file agrees after one write.
func TestPresetSwapWithEmptySlotMoves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	sp := speakerWithPresets(t, path, "A")
	if err := sp.SwapPresets(context.Background(), 1, 5); err != nil {
		t.Fatal(err)
	}
	live, stored := presetNames(t, sp, path)
	want := map[int]string{5: "A"}
	if !maps.Equal(live, want) || !maps.Equal(stored, want) {
		t.Fatalf("speaker has %v, file has %v, want %v", live, stored, want)
	}
}
