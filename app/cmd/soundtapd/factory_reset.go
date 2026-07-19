// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/btctl"
)

// factoryReset returns the speaker to an out-of-box state: presets, settings and
// Wi-Fi credentials go, and it comes back up hosting the setup AP.
//
// Everything it removes lives inside the writable state dir or is the one Wi-Fi
// file beside it — deliberately nothing that touches the NAND layout. Per the boot
// contract the persistent volume is never reformatted, and doing so here would turn
// a recovery gesture into a way to brick the speaker.
//
// Failures are logged and the rest still runs so one stale file cannot block setup mode.
// stores are closed before anything is deleted, so no save still in flight (the
// shutdown's last volume, a preset write) can recreate a file the reset removed.
func factoryReset(home, wifiConf string, log *slog.Logger, terminate func(), stores ...io.Closer) {
	log.Warn("factory reset requested")
	// Before anything goes: a reset is not the first start after an install from stock.
	if err := markStockTaken(home); err != nil {
		log.Error("factory reset: record stock takeover", "err", err)
	}
	for _, s := range stores {
		if err := s.Close(); err != nil {
			log.Warn("factory reset: close store", "err", err) // its file is removed below anyway
		}
	}
	persistentRoot := filepath.Dir(home)
	paths := []string{
		filepath.Join(home, "presets.json"),
		filepath.Join(home, "api-token"),
		filepath.Join(home, "presets.json.tmp"),
		filepath.Join(home, "presets.json.settings"),
		filepath.Join(home, "presets.json.settings.tmp"),
		filepath.Join(home, "recent.json"), // listening history is the owner's
		filepath.Join(home, "favorites.json"),
		filepath.Join(home, ".sessions"),
		filepath.Join(home, ".sessions.tmp"),
		filepath.Join(home, "plugins"),
		filepath.Join(home, "snapserver"),
		filepath.Join(snapserverDataDir, snapserverState), // live copy; soundtapd restarts, no reboot
		filepath.Join(home, "multiroom-enabled"),
		filepath.Join(home, "multiroom-server"), // or it boots back into its old group
		wifiConf + ".good",
		wifiConf + ".known", // every saved network goes, not just the current one
	}
	onSpeaker := filepath.Clean(home) == filepath.Join(persistentRoot, "soundtap")
	if onSpeaker {
		clearBluetoothPairings(log)
	}
	// Spotify's and BlueZ's state belongs to other users: soundtap-root removes and
	// recreates it when soundtapd runs as its own (ADR 0066).
	foreign := onSpeaker && rootHelper == nil
	if foreign {
		paths = append(paths, filepath.Join(persistentRoot, "spotify"))
		for _, dir := range bluetoothStateDirs {
			paths = append(paths, filepath.Join(persistentRoot, dir))
		}
	}
	if onSpeaker && rootHelper != nil {
		if err := rootHelper.ResetForeignState(); err != nil {
			log.Error("factory reset: Spotify and Bluetooth state", "err", err)
		}
	}
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			log.Error("factory reset: remove", "path", path, "err", err)
		}
	}
	removeAtomicTemps(append(paths, wifiConf), log)
	// The corrupt copies Open moved aside hold the same data as the stores themselves.
	for _, name := range []string{"presets.json.settings", "presets.json", "recent.json", "favorites.json"} {
		if err := atomicjson.RemoveQuarantine(filepath.Join(home, name)); err != nil {
			log.Error("factory reset: remove corrupt copies", "store", name, "err", err)
		}
	}
	if foreign {
		for _, dir := range bluetoothStateDirs {
			if err := os.MkdirAll(filepath.Join(persistentRoot, dir), 0o700); err != nil {
				log.Error("factory reset: recreate Bluetooth state directory", "dir", dir, "err", err)
			}
		}
	}
	if err := enterSetupMode(wifiConf); err != nil {
		log.Error("factory reset: enter setup mode", "err", err)
	}
	log.Warn("factory reset complete: restarting into setup mode")
	terminate()
}

// removeAtomicTemps deletes what an interrupted atomicjson.WriteFile leaves beside each
// path (".<name>.tmp-<random>"): a complete copy of the credentials the reset is meant to clear.
func removeAtomicTemps(paths []string, log *slog.Logger) {
	for _, path := range paths {
		orphans, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*"))
		for _, orphan := range orphans {
			if err := os.Remove(orphan); err != nil {
				log.Error("factory reset: remove temp file", "path", orphan, "err", err)
			}
		}
	}
}

// bluetoothStateDirs live under /mnt/nv behind /var/lib links: BlueZ's pairings and
// bluealsa's per-device volumes. A reset empties both and leaves the links' targets.
var bluetoothStateDirs = []string{"bluetooth", "bluealsa"}

func clearBluetoothPairings(log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, d := range pairedBluetoothDevices(ctx) {
		if err := btctl.Remove(ctx, bluetoothBus, d); err != nil {
			log.Warn("factory reset: remove Bluetooth pairing", "address", d.Address, "err", err)
		}
	}
}

// pairedBluetoothDevices lists BlueZ's paired devices; nil when BlueZ cannot be asked.
func pairedBluetoothDevices(ctx context.Context) []btctl.Device {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return btctl.Paired(ctx, bluetoothBus)
}

func removeStalePlugins(home string) error {
	return os.RemoveAll(filepath.Join(home, "plugins"))
}
