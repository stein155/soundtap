// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
)

// snapserver keeps its clients and groups in server.json and rewrites the whole file
// each time a client's lastSeen moves: every 15 to 30 s, all day. On the flash volume
// that was the one steady writer. So snapserver writes to tmpfs, and the file is kept on
// flash only when something other than lastSeen changed (a client's volume, mute,
// latency or group).
const (
	snapserverDataDir = "/run/soundtap/snapserver" // datadir in /etc/snapserver.conf; snapserverKeepDir is in home.go
	snapserverState   = "server.json"
	snapserverKeepAge = time.Minute
)

// Modes of snapserver's state: its directories and the kept copy are soundtapd's own;
// the tmpfs copy is opened to snapserver's user where it cannot be handed over.
const (
	snapserverDirMode    = 0o700
	snapserverFileMode   = 0o600
	snapserverSharedMode = 0o666
)

var snapserverLastSeen = regexp.MustCompile(`"lastSeen":\{[^}]*\}`)

// seedSnapserverState puts the kept server.json in snapserver's datadir after a boot.
// After a soundtapd restart the tmpfs copy is newer and stays, unless the kept copy is
// gone: a factory reset removed it and restarts soundtapd without a reboot, and the old
// clients and groups must not come back from tmpfs.
func seedSnapserverState(run, keep string, log *slog.Logger) {
	err := os.MkdirAll(run, snapserverDirMode)
	if err != nil {
		log.Warn("mkdir", "path", run, "err", err)

		return
	}

	dst := filepath.Join(run, snapserverState)

	data, err := os.ReadFile(filepath.Join(keep, snapserverState))
	if err != nil {
		// First boot or a factory reset: snapserver starts empty.
		rmErr := os.Remove(dst)
		if rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			log.Warn("clear snapserver state", "err", rmErr)
		}

		return
	}

	_, err = os.Stat(dst)
	if err == nil {
		return
	}

	err = os.WriteFile(dst, data, snapserverFileMode) //nolint:gosec // dst is a fixed path under /run
	if err != nil {
		log.Warn("seed snapserver state", "err", err)

		return
	}

	if !mayChown() {
		// ownTree cannot hand it to snapserver's user, which rewrites it in place. The
		// directory (snapcast:soundtapd 2770, init.d/soundtap) keeps everyone else out.
		_ = os.Chmod(dst, snapserverSharedMode) // reachable only through that directory
	}
}

// keepSnapserverState copies server.json to flash when it changed apart from lastSeen.
func keepSnapserverState(ctx context.Context, run, keep string, log *slog.Logger) {
	// No kept copy (or an unreadable one) compares unequal, so the first change is kept.
	kept, _ := os.ReadFile(filepath.Join(keep, snapserverState))
	last := snapserverLastSeen.ReplaceAll(kept, nil)

	ticker := time.NewTicker(snapserverKeepAge)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		var err error

		last, err = keepSnapserverOnce(run, keep, last)
		if err != nil {
			log.Warn("keep snapserver state", "err", err)
		}
	}
}

// errSnapserverStatePartial is a server.json read while snapserver was rewriting it.
var errSnapserverStatePartial = errors.New("snapserver state is not whole JSON yet; kept copy left as it is")

// keepSnapserverOnce writes run's server.json to keep when it differs from last, the
// kept copy without lastSeen, and returns the new last.
func keepSnapserverOnce(run, keep string, last []byte) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(run, snapserverState))
	if errors.Is(err, fs.ErrNotExist) {
		return last, nil
	}

	if err != nil {
		return last, fmt.Errorf("read snapserver state: %w", err)
	}

	now := snapserverLastSeen.ReplaceAll(data, nil)
	if bytes.Equal(now, last) {
		return last, nil
	}
	// snapserver rewrites the file in place: a read during that rewrite gets a part of
	// it, which would replace the good copy and seed the next boot with nothing. The next
	// tick reads it whole.
	if !json.Valid(data) {
		return last, errSnapserverStatePartial
	}

	err = os.MkdirAll(keep, snapserverDirMode)
	if err != nil {
		return last, fmt.Errorf("keep snapserver state: %w", err)
	}

	err = atomicjson.WriteFile(filepath.Join(keep, snapserverState), data, snapserverFileMode)
	if err != nil {
		return last, fmt.Errorf("keep snapserver state: %w", err)
	}

	return now, nil
}
