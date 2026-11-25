// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stein155/soundtap/app/internal/web"
)

// pstoreDir is where init mounts what the kernel kept of the previous boot in ramoops
// (am335x-soundtap-ramoops.dtsi): its console log, and a record per panic or oops.
//
//nolint:gochecknoglobals // a test seam: a test points it at a temp dir
var pstoreDir = "/sys/fs/pstore"

// lastBootDone marks that this boot already moved the records: a restart of soundtapd
// finds pstore empty and must not replace them with "power loss".
//
//nolint:gochecknoglobals // a test seam: a test points it at a temp dir
var lastBootDone = "/run/soundtap/last-boot-moved"

// Modes of what keepLastBoot writes: the records are the owner's alone, /run/soundtap is
// the daemon's runtime directory as init makes it.
const (
	lastBootDirMode  = 0o700
	lastBootFileMode = 0o600
	runDirMode       = 0o755
)

// keepLastBoot moves the previous boot's records to <home>/last-boot, where they outlive
// the next reset too and GET /api/debug shows them, and says how that boot ended.
// Panic and oops records are removed from pstore to free their RAM. It does nothing on a
// restart of soundtapd within one boot (moved is false).
func keepLastBoot(home string) (string, bool, error) {
	if devMode.Load() {
		return "", false, nil // a computer's pstore holds its own crash records, not a speaker's
	}

	_, err := os.Stat(lastBootDone)
	if err == nil {
		return "", false, nil
	}

	entries, err := os.ReadDir(pstoreDir)
	if err != nil {
		return "", false, fmt.Errorf("read pstore: %w", err)
	}

	dir := filepath.Join(home, web.LastBootDir)

	err = os.RemoveAll(dir)
	if err != nil {
		return "", false, fmt.Errorf("clear the last boot's records: %w", err)
	}

	err = os.MkdirAll(dir, lastBootDirMode)
	if err != nil {
		return "", false, fmt.Errorf("last boot's records: %w", err)
	}

	out, err := os.OpenRoot(dir)
	if err != nil {
		return "", false, fmt.Errorf("last boot's records: %w", err)
	}
	defer out.Close()

	records := map[string][]byte{}

	for _, entry := range entries {
		src := filepath.Join(pstoreDir, entry.Name())

		record, readErr := os.ReadFile(src)
		if readErr != nil {
			return "", false, fmt.Errorf("read a pstore record: %w", readErr)
		}

		err = out.WriteFile(entry.Name(), record, lastBootFileMode)
		if err != nil {
			return "", false, fmt.Errorf("keep a pstore record: %w", err)
		}

		records[entry.Name()] = record
		// A panic record stays until removed and takes a slot from the next one. The
		// console record is not: removing it would wipe this boot's console buffer, and
		// the next boot replaces it anyway.
		if !strings.HasPrefix(entry.Name(), "console-") {
			_ = os.Remove(src) // copied above; a record left in pstore is read again next boot
		}
	}

	how := bootEnd(records)
	err = errors.Join(
		os.WriteFile(filepath.Join(dir, "how"), []byte(how+"\n"), lastBootFileMode),
		os.MkdirAll(filepath.Dir(lastBootDone), runDirMode),
		os.WriteFile(lastBootDone, nil, lastBootFileMode))

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
