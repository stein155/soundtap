// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
)

// AutoInstall flashes the BCO image the OS ships (ADR 0031) when it differs from the last one
// this speaker tried, recorded in marker as "<sha256> <attempts>". The marker is written
// before the flash. It makes one attempt per call; the caller decides about another
// (RetryInstall). An image that failed is left alone while the BCO still runs our app,
// because the LAN runs through it, and tried again when it runs no app of ours (cut off
// after the erase, or still stock), on this boot and later ones, without a limit: without
// our app the ST20 has no network at all. A flash that worked is marked " ok"; if
// ownApp then says the BCO no longer runs it (stock was flashed back in the meantime), the
// image is installed again. The image is trusted because it came with the signed OS, not
// because it is on the allowlist. flashed says whether the BCO now runs a newly written
// image.
func AutoInstall(ctx context.Context, image, marker string, ownApp func() bool, flash func(context.Context, []byte) (string, error), log io.Writer) (flashed bool, err error) {
	img, err := os.ReadFile(image)
	if err != nil {
		return false, err
	}

	sum := sha256.Sum256(img)
	want := hex.EncodeToString(sum[:])
	tried := 0

	b, _ := os.ReadFile(marker) // missing: never tried
	switch f := strings.Fields(string(b)); {
	case len(f) == 2 && f[0] == want && f[1] == "ok":
		if ownApp() {
			return false, nil
		}

		fmt.Fprintf(log, "[bco] BCO no longer runs the own app\n")
	case len(f) >= 1 && f[0] == want:
		tried = 1 // a marker from before the count

		if len(f) == 2 {
			if n, err := strconv.Atoi(f[1]); err == nil {
				tried = n
			}
		}

		if ownApp() {
			return false, nil
		}

		fmt.Fprintf(log, "[bco] BCO runs no own app after %d failed flash(es); trying again\n", tried)
	}

	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return false, err
	}

	if err := AllowImage(img, want); err != nil {
		return false, err
	}

	tried++
	if err := atomicjson.WriteFile(marker, []byte(fmt.Sprintf("%s %d\n", want, tried)), 0o644); err != nil {
		return false, err
	}

	fmt.Fprintf(log, "[bco] installing the OS's BCO image %s (attempt %d)\n", want[:16], tried)

	ver, err := flash(ctx, img)
	if err != nil {
		return false, fmt.Errorf("BCO image %s: %w", want[:16], err)
	}

	fmt.Fprintf(log, "[bco] BCO runs %s\n", ver)

	return true, atomicjson.WriteFile(marker, []byte(want+" ok\n"), 0o644)
}

// RetryInstall says whether a failed AutoInstall is tried again: only while the BCO runs no
// app of ours, and so the speaker has no network to lose.
func RetryInstall(err error, ownApp func() bool) bool { return err != nil && !ownApp() }

// RetryDelay is the wait before the nth retry of a failed install (n from 1): 5 minutes,
// doubling up to an hour, so a BCO that keeps failing is not erased every few minutes.
func RetryDelay(n int) time.Duration {
	d := 5 * time.Minute
	for i := 1; i < n && d < time.Hour; i++ {
		d *= 2
	}

	return min(d, time.Hour)
}
