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
	t.Parallel()

	home := t.TempDir()
	handler := New(nil, home)

	var rep Report

	rep.Size, rep.Kernel, rep.Models = 123, "stock", []string{"ST10", "ST20"}

	err := handler.persistFirmwarePending("PONG", &rep)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(home, install.PendingPath)

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("pending state = %v, %v", info, err)
	}

	_, err = os.Stat(path + ".tmp")
	if !os.IsNotExist(err) {
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
	t.Parallel()

	if !install.HasCmdlineField("console=ttyGS0 soundtap.ram=1\n", "soundtap.ram=1") {
		t.Fatal("RAM recovery cmdline field was missed")
	}
}
