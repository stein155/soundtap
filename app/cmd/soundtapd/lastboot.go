// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/stein155/soundtap/app/internal/web"
)

// pstoreDir is where init mounts what the kernel kept of the previous boot in ramoops
// (am335x-soundtap-ramoops.dtsi): its console log, and a record per panic or oops.
var pstoreDir = "/sys/fs/pstore"

// lastBootDone marks that this boot already moved the records: a restart of soundtapd
// finds pstore empty and must not replace them with "power loss".
var lastBootDone = "/run/soundtap/last-boot-moved"

// keepLastBoot moves the previous boot's records to <home>/last-boot, where they outlive
// the next reset too and GET /api/debug shows them, and says how that boot ended.
// Panic and oops records are removed from pstore to free their RAM. It does nothing on a
// restart of soundtapd within one boot (moved is false).
func keepLastBoot(home string) (how string, moved bool, err error) {
	if devMode.Load() {
		return "", false, nil // a computer's pstore holds its own crash records, not a speaker's
	}
	if _, err := os.Stat(lastBootDone); err == nil {
		return "", false, nil
	}
	entries, err := os.ReadDir(pstoreDir)
	if err != nil {
		return "", false, err
	}
	dir := filepath.Join(home, web.LastBootDir)
	if err := os.RemoveAll(dir); err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, err
	}
	out, err := os.OpenRoot(dir)
	if err != nil {
		return "", false, err
	}
	defer out.Close()
	records := map[string][]byte{}
	for _, e := range entries {
		src := filepath.Join(pstoreDir, e.Name())
		b, err := os.ReadFile(src)
		if err != nil {
			return "", false, err
		}
		if err := out.WriteFile(e.Name(), b, 0o600); err != nil {
			return "", false, err
		}
		records[e.Name()] = b
		// A panic record stays until removed and takes a slot from the next one. The
		// console record is not: removing it would wipe this boot's console buffer, and
		// the next boot replaces it anyway.
		if !strings.HasPrefix(e.Name(), "console-") {
			_ = os.Remove(src) // copied above; a record left in pstore is read again next boot
		}
	}
	how = bootEnd(records)
	err = errors.Join(
		os.WriteFile(filepath.Join(dir, "how"), []byte(how+"\n"), 0o600),
		os.MkdirAll(filepath.Dir(lastBootDone), 0o755),
		os.WriteFile(lastBootDone, nil, 0o600))
	return how, true, err
}

// bootEnd reads how a boot ended from what pstore kept of it, as far as the hardware
// can tell: a panic or oops leaves a dmesg record, a clean reboot ends its console log
// with the kernel's last line, and a reset that line never reached (watchdog, hang)
// leaves a log without it. A power loss leaves nothing, because DDR loses its content.
func bootEnd(records map[string][]byte) string {
	// An oops panics (CONFIG_PANIC_ON_OOPS), which leaves an Oops and a Panic record:
	// the oops is the cause, whatever order the map yields them in.
	dmesg := false
	for name, b := range records {
		if !strings.HasPrefix(name, "dmesg-") {
			continue
		}
		if bytes.HasPrefix(b, []byte("Oops")) {
			return "oops"
		}
		dmesg = true
	}
	if dmesg {
		return "panic"
	}
	console, ok := records["console-ramoops-0"]
	switch {
	case !ok:
		return "power loss"
	case bytes.Contains(console, []byte("reboot: Restarting system")):
		return "reboot"
	case bytes.Contains(console, []byte("reboot: Power down")):
		return "power off"
	default:
		return "reset without a reboot (watchdog or hang)"
	}
}
