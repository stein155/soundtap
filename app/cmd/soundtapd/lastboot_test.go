// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootEnd(t *testing.T) {
	for want, records := range map[string]map[string][]byte{
		"panic":      {"dmesg-ramoops-0": []byte("Panic#1 Part1\n<0>Kernel panic"), "console-ramoops-0": []byte("x")},
		"oops":       {"dmesg-ramoops-0": []byte("Oops#1 Part1\n")},
		"reboot":     {"console-ramoops-0": []byte("[ 108.87] reboot: Restarting system\n")},
		"power off":  {"console-ramoops-0": []byte("reboot: Power down\n")},
		"power loss": {},
		// panic_on_oops: the oops and the panic it caused, in either map order.
		"oops ": {"dmesg-ramoops-0": []byte("Panic#1 Part1\n"), "dmesg-ramoops-1": []byte("Oops#1 Part1\n")},
		"reset without a reboot (watchdog or hang)": {"console-ramoops-0": []byte("[ 5.0] wlan0: associated\n")},
	} {
		want = strings.TrimSpace(want) // map keys are unique; a trailing space adds a second case
		for range 20 {                 // map order varies between iterations
			if got := bootEnd(records); got != want {
				t.Errorf("bootEnd(%v) = %q, want %q", records, got, want)
			}
		}
	}
}

// The records move once per boot: a soundtapd restart finds pstore empty and keeps them.
func TestKeepLastBootMovesOncePerBoot(t *testing.T) {
	home, run := t.TempDir(), t.TempDir()
	pstoreDir, lastBootDone = t.TempDir(), filepath.Join(run, "moved")
	defer func() { pstoreDir, lastBootDone = "/sys/fs/pstore", "/run/soundtap/last-boot-moved" }()
	for name, body := range map[string]string{"dmesg-ramoops-0": "Panic#1 Part1\n", "console-ramoops-0": "[1] up\n"} {
		if err := os.WriteFile(filepath.Join(pstoreDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	how, moved, err := keepLastBoot(home)
	if err != nil || !moved || how != "panic" {
		t.Fatalf("first = %q %v %v, want panic true nil", how, moved, err)
	}
	if _, err := os.Stat(filepath.Join(pstoreDir, "dmesg-ramoops-0")); !os.IsNotExist(err) {
		t.Errorf("record still in pstore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pstoreDir, "console-ramoops-0")); err != nil {
		t.Errorf("console record removed: removing it wipes the running boot's console: %v", err)
	}
	if _, moved, _ := keepLastBoot(home); moved {
		t.Error("a restart within the boot moved the records again")
	}
	b, err := os.ReadFile(filepath.Join(home, "last-boot", "dmesg-ramoops-0"))
	if err != nil || string(b) != "Panic#1 Part1\n" {
		t.Errorf("kept record = %q %v", b, err)
	}
}
