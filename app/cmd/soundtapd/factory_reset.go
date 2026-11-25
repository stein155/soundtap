// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
func factoryReset(ctx context.Context, home, wifiConf string, log *slog.Logger, terminate func(), stores ...io.Closer) {
	log.Warn("factory reset requested")
	// Before anything goes: a reset is not the first start after an install from stock.
	err := markStockTaken(home)
	if err != nil {
		log.Error("factory reset: record stock takeover", "err", err)
	}

	closeResetStores(stores, log)

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
		clearBluetoothPairings(context.WithoutCancel(ctx), log) // a reset is not cut short
	}
	// Spotify's and BlueZ's state belongs to other users: soundtap-root removes and
	// recreates it when soundtapd runs as its own (ADR 0066).
	foreign := onSpeaker && rootHelper == nil
	if foreign {
		paths = append(paths, filepath.Join(persistentRoot, "spotify"))
		for _, dir := range bluetoothStateDirs() {
			paths = append(paths, filepath.Join(persistentRoot, dir))
		}
	}

	if onSpeaker && rootHelper != nil {
		foreignErr := rootHelper.ResetForeignState()
		if foreignErr != nil {
			log.Error("factory reset: Spotify and Bluetooth state", "err", foreignErr)
		}
	}

	removeResetPaths(paths, log)
	removeAtomicTemps(append(paths, wifiConf), log)
	removeResetQuarantine(home, log)

	if foreign {
		recreateBluetoothStateDirs(persistentRoot, log)
	}

	err = enterSetupMode(wifiConf)
	if err != nil {
		log.Error("factory reset: enter setup mode", "err", err)
	}

	log.Warn("factory reset complete: restarting into setup mode")
	terminate()
}

// closeResetStores closes the stores before their files go.
func closeResetStores(stores []io.Closer, log *slog.Logger) {
	for _, store := range stores {
		err := store.Close()
		if err != nil {
			log.Warn("factory reset: close store", "err", err) // its file is removed below anyway
		}
	}
}

// removeResetPaths removes every path a factory reset clears; a failure is logged and the
// rest still goes.
func removeResetPaths(paths []string, log *slog.Logger) {
	for _, path := range paths {
		err := os.RemoveAll(path)
		if err != nil {
			log.Error("factory reset: remove", "path", path, "err", err)
		}
	}
}

// removeResetQuarantine removes the corrupt copies Open moved aside: they hold the same
// data as the stores themselves.
func removeResetQuarantine(home string, log *slog.Logger) {
	for _, name := range []string{"presets.json.settings", "presets.json", "recent.json", "favorites.json"} {
		err := atomicjson.RemoveQuarantine(filepath.Join(home, name))
		if err != nil {
			log.Error("factory reset: remove corrupt copies", "store", name, "err", err)
		}
	}
}

// recreateBluetoothStateDirs puts back the emptied directories the /var/lib links point at.
func recreateBluetoothStateDirs(persistentRoot string, log *slog.Logger) {
	for _, dir := range bluetoothStateDirs() {
		err := os.MkdirAll(filepath.Join(persistentRoot, dir), bluetoothStateDirMode)
		if err != nil {
			log.Error("factory reset: recreate Bluetooth state directory", "dir", dir, "err", err)
		}
	}
}

// removeAtomicTemps deletes what an interrupted atomicjson.WriteFile leaves beside each
// path (".<name>.tmp-<random>"): a complete copy of the credentials the reset is meant to clear.
func removeAtomicTemps(paths []string, log *slog.Logger) {
	for _, path := range paths {
		orphans, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*"))
		for _, orphan := range orphans {
			err := os.Remove(orphan)
			if err != nil {
				log.Error("factory reset: remove temp file", "path", orphan, "err", err)
			}
		}
	}
}

// bluetoothStateDirMode keeps BlueZ's and bluealsa's state to their own users.
const bluetoothStateDirMode = 0o700

// bluetoothAskTimeout bounds one exchange with BlueZ over D-Bus.
const bluetoothAskTimeout = 5 * time.Second

// bluetoothStateDirs live under /mnt/nv behind /var/lib links: BlueZ's pairings and
// bluealsa's per-device volumes. A reset empties both and leaves the links' targets.
func bluetoothStateDirs() []string { return []string{"bluetooth", "bluealsa"} }

// clearBluetoothPairings removes every pairing BlueZ holds, within bluetoothAskTimeout of
// parent.
func clearBluetoothPairings(parent context.Context, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(parent, bluetoothAskTimeout)
	defer cancel()

	for _, device := range pairedBluetoothDevices(ctx) {
		err := btctl.Remove(ctx, bluetoothBus, device)
		if err != nil {
			log.Warn("factory reset: remove Bluetooth pairing", "address", device.Address, "err", err)
		}
	}
}

// pairedBluetoothDevices lists BlueZ's paired devices; nil when BlueZ cannot be asked.
func pairedBluetoothDevices(ctx context.Context) []btctl.Device {
	ctx, cancel := context.WithTimeout(ctx, bluetoothAskTimeout)
	defer cancel()

	return btctl.Paired(ctx, bluetoothBus)
}

func removeStalePlugins(home string) error {
	err := os.RemoveAll(filepath.Join(home, "plugins"))
	if err != nil {
		return fmt.Errorf("plugins: %w", err)
	}

	return nil
}

// retiredFiles are names in soundtapd's home that nothing reads any more, removed at every
// start: allow-unsigned switched on unsigned uploads on an alpha image, before every upload
// behind the press on the speaker took one, on every profile (ADR 0048).
func retiredFiles() []string { return []string{"allow-unsigned"} }

// removeRetiredFiles removes retiredFiles from home and leaves everything else alone.
func removeRetiredFiles(home string) error {
	var errs []error

	for _, name := range retiredFiles() {
		err := os.Remove(filepath.Join(home, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
