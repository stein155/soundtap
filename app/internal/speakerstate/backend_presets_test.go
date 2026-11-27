// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate_test

import (
	"testing"

	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// StorePresetIfEmpty fills an empty slot, persisting it as StorePreset does, and leaves a
// slot that holds a preset as it is.
func TestStorePresetIfEmptyKeepsAHeldSlot(t *testing.T) {
	t.Parallel()

	state := speakerstate.New()

	var persisted []int

	state.OnPresetChange(func(slot int, _ *speaker.Preset) error {
		persisted = append(persisted, slot)

		return nil
	})

	err := state.StorePreset(t.Context(), 1, "TUNEIN", "stationurl", "/v1/playback/station/s1", "Owner's", "")
	if err != nil {
		t.Fatal(err)
	}

	stored, err := state.StorePresetIfEmpty(t.Context(), 1, "TUNEIN", "stationurl", "https://example.com/a", "A", "")
	if err != nil || stored {
		t.Fatalf("held slot: stored %v, %v; want false, nil", stored, err)
	}

	stored, err = state.StorePresetIfEmpty(t.Context(), 2, "TUNEIN", "stationurl", "https://example.com/b", "B", "")
	if err != nil || !stored {
		t.Fatalf("empty slot: stored %v, %v; want true, nil", stored, err)
	}

	presets, err := state.Presets(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(presets) != 2 || presets[0].Name != "Owner's" || presets[1].Location != "https://example.com/b" ||
		presets[1].StationID != "https://example.com/b" {
		t.Fatalf("presets = %+v", presets)
	}

	if len(persisted) != 2 || persisted[0] != 1 || persisted[1] != 2 {
		t.Fatalf("persisted slots %v, want [1 2]", persisted)
	}

	_, err = state.StorePresetIfEmpty(t.Context(), 7, "TUNEIN", "stationurl", "https://example.com/c", "C", "")
	if err == nil {
		t.Fatal("slot 7 accepted")
	}

	_, err = state.StorePresetIfEmpty(t.Context(), 3, "TUNEIN", "stationurl", "https://example.com/c", " ", "")
	if err == nil {
		t.Fatal("an empty name accepted")
	}
}
