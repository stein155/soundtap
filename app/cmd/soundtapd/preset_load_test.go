// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"io"
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
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	path := filepath.Join(t.TempDir(), "presets.json")
	saved := []byte(`[{"slot":1,"stationId":"s1","name":"Radio 1"}]`)
	if err := os.WriteFile(path, saved, 0o000); err != nil {
		t.Fatal(err)
	}
	sp := speakerstate.New()
	st := loadPresets(context.Background(), sp, path, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if st.LoadErr() == nil {
		t.Fatal("LoadErr = nil for an unreadable presets file")
	}
	if err := sp.Select(context.Background(), "TUNEIN", "stationurl", "/v1/playback/station/s2", "Two", "", ""); err != nil {
		t.Fatal(err)
	}
	if stored, err := sp.StoreCurrentAsPreset("PRESET_2"); err == nil || stored {
		t.Fatalf("store = %v, %v; want refused", stored, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != string(saved) {
		t.Fatalf("unreadable presets overwritten with %s", data)
	}
}
