// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// A presets file soundtapd cannot read (EACCES here, EIO on a failing NAND) used to give
// an empty store that the next held preset key wrote over the owner's six presets. Now
// the store is read-only: the key press fails and the file is still there afterwards.
func TestUnreadablePresetsAreNotOverwritten(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}

	path := filepath.Join(t.TempDir(), "presets.json")

	saved := []byte(`[{"slot":1,"stationId":"s1","name":"Radio 1"}]`)

	err := os.WriteFile(path, saved, 0o000)
	if err != nil {
		t.Fatal(err)
	}

	speaker := speakerstate.New()

	st := loadPresets(context.Background(), speaker, path, stockPresets, slog.New(slog.DiscardHandler))
	if st.LoadErr() == nil {
		t.Fatal("LoadErr = nil for an unreadable presets file")
	}

	err = speaker.Select(context.Background(), "TUNEIN", "stationurl", "/v1/playback/station/s2", "Two", "", "")
	if err != nil {
		t.Fatal(err)
	}

	stored, err := speaker.StoreCurrentAsPreset("PRESET_2")
	if err == nil || stored {
		t.Fatalf("store = %v, %v; want refused", stored, err)
	}

	err = os.Chmod(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if data, _ := os.ReadFile(path); !bytes.Equal(data, saved) {
		t.Fatalf("unreadable presets overwritten with %s", data)
	}
}
