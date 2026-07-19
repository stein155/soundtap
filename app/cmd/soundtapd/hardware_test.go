// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"errors"
	"strings"
	"testing"
)

// An unidentified model leaves the API up but turns every hardware path off, and says
// so in /api/health.
func TestRefuseUnknownHardware(t *testing.T) {
	t.Cleanup(func() { problems.clear("model") })
	opt := options{}
	refuseUnknownHardware(nil, &opt, quietLog())
	if opt.noAudio || len(problems.list()) != 0 {
		t.Fatalf("a known model was refused: noAudio=%v problems=%q", opt.noAudio, problems.list())
	}
	refuseUnknownHardware(errors.New("MODEL is not set"), &opt, quietLog())
	if !opt.noAudio {
		t.Fatal("unknown model still drives the audio side")
	}
	if got := problems.list(); len(got) != 1 || !strings.Contains(got[0], "MODEL is not set") {
		t.Fatalf("problems = %q", got)
	}
	oldErr := boardErr
	boardErr = errors.New("unknown")
	t.Cleanup(func() { boardErr = oldErr })
	if got := displayPath(); got != "" {
		t.Fatalf("displayPath = %q on an unknown model, want none", got)
	}
}

func TestEnvDefault(t *testing.T) {
	t.Setenv("SOUNDTAP_TEST_PATH", "/override")
	if got := envDefault("SOUNDTAP_TEST_PATH", "/default"); got != "/override" {
		t.Fatalf("envDefault = %q", got)
	}
}
