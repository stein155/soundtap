// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/install"
)

func TestFirmwareRecoveryRecordsTargetAndBootOutcome(t *testing.T) {
	home := t.TempDir()
	s := &Handler{homeDir: home}
	rep := Report{Size: 123, Kernel: "stock", Models: []string{"ST10", "ST20"}}
	if err := s.persistFirmwarePending("PONG", rep); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, install.PendingPath)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("pending state = %v, %v", info, err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary state remains: %v", err)
	}

	booted := install.LoadRecovery(home, func() (string, error) { return "ubi.mtd=9", nil })
	if booted.Status != "booted" || booted.Running != "PONG" || booted.Pending.Package.Kernel != "stock" {
		t.Fatalf("booted recovery = %+v", booted)
	}
	mismatch := install.LoadRecovery(home, func() (string, error) { return "ubi.mtd=8", nil })
	if mismatch.Status != "mismatch" || mismatch.Running != "PING" {
		t.Fatalf("mismatch recovery = %+v", mismatch)
	}
	var debug strings.Builder
	mismatch.WriteDebug(&debug)
	if got := debug.String(); !strings.Contains(got, "status     : mismatch\ntarget slot: PONG\nrunning slot: PING") {
		t.Fatalf("debug recovery = %q", got)
	}
}

func TestHasCmdlineFieldAcceptsTrailingNewline(t *testing.T) {
	if !install.HasCmdlineField("console=ttyGS0 soundtap.ram=1\n", "soundtap.ram=1") {
		t.Fatal("RAM recovery cmdline field was missed")
	}
}
