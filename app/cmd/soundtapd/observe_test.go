// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/counters"
)

// pprof is served on an alpha image only, and only on loopback (ADR 0127).
func TestPprofOnlyOnAlphaAndLoopback(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile")
	oldFile, oldAddr := profileFile, pprofAddr
	t.Cleanup(func() { profileFile, pprofAddr = oldFile, oldAddr })
	profileFile, pprofAddr = profile, "127.0.0.1:0"

	for _, tc := range []struct {
		name, firmware, profile string
		served                  bool
	}{
		{"alpha image", "1", "alpha\n", true},
		{"production image", "1", "production\n", false},
		{"no profile", "1", "", false},
		{"alpha profile off a speaker", "", "alpha\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SOUNDTAP_FIRMWARE", tc.firmware)
			_ = os.Remove(profile)
			if tc.profile != "" {
				if err := os.WriteFile(profile, []byte(tc.profile), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			addr := startPprof(ctx, log)
			if (addr != "") != tc.served {
				t.Fatalf("served at %q, want served %v", addr, tc.served)
			}
			if addr == "" {
				return
			}
			host, _, err := net.SplitHostPort(addr)
			if err != nil || !net.ParseIP(host).IsLoopback() {
				t.Fatalf("pprof listens on %s, not loopback", addr)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/debug/pprof/goroutine?debug=1", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK || len(body) == 0 {
				t.Errorf("goroutine profile: %s, %d bytes", resp.Status, len(body))
			}
		})
	}
}

func TestSoundtapdStartsCountedOnFirmwareOnly(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	old := soundtapdStarts
	t.Cleanup(func() { soundtapdStarts = old; counters.Reset() })
	soundtapdStarts = filepath.Join(t.TempDir(), "soundtapd-starts")

	t.Setenv("SOUNDTAP_FIRMWARE", "")
	countSoundtapdStart(log)
	if _, err := os.Stat(soundtapdStarts); err == nil {
		t.Fatal("a computer's start was counted in /run")
	}
	t.Setenv("SOUNDTAP_FIRMWARE", "1")
	for range 3 {
		countSoundtapdStart(log)
	}
	if n := counters.SoundtapdRestarts.Load(); n != 2 {
		t.Errorf("%d restarts after three starts, want 2", n)
	}
}
