// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCPULineReadsIdleAgainstUptime(t *testing.T) {
	t.Parallel()

	proc := t.TempDir()
	_ = os.MkdirAll(filepath.Join(proc, "self"), 0o755)
	_ = os.WriteFile(filepath.Join(proc, "uptime"), []byte("1000.00 900.00\n"), 0o644)
	_ = os.WriteFile(
		filepath.Join(proc, "stat"),
		[]byte("cpu  5000 0 3000 85000 0 0 0 0 0 0\ncpu0 5000 0 3000 85000 0 0 0 0 0 0\nprocesses 4321\n"),
		0o644,
	)
	_ = os.WriteFile(
		filepath.Join(proc, "self", "stat"),
		[]byte("42 (soundtap d) S 1 42 42 0 -1 0 0 0 0 0 1500 500 0 0 20 0\n"),
		0o644,
	)

	want := "uptime 1000 s, idle 850 s (busy 15% since boot), soundtapd 20 s, forks 4321"
	if got := cpuLine(proc); got != want {
		t.Fatalf("cpuLine = %q, want %q", got, want)
	}

	if got := cpuLine(t.TempDir()); got != "unavailable" {
		t.Fatalf("empty proc: %q", got)
	}
}
