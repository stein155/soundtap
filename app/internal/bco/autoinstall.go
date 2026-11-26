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

// The marker file: "<sha256> <attempts>" or "<sha256> ok", and its modes.
const (
	markerFields  = 2
	markerDirMode = 0o755
	markerMode    = 0o644
)

// firstRetryDelay is RetryDelay's first wait; each later one doubles, up to an hour.
const firstRetryDelay = 5 * time.Minute

// AutoInstall flashes the BCO image the OS ships (ADR 0031) when it differs from the last one
// this speaker tried, recorded in marker as "<sha256> <attempts>". The marker is written
// before the flash. It makes one attempt per call; the caller decides about another
// (RetryInstall). An image that failed is left alone while the BCO still runs our app,
// because the LAN runs through it, and tried again when it runs no app of ours (cut off
// after the erase, or still stock), on this boot and later ones, without a limit: without
// our app the ST20 has no network at all. A flash that worked is marked " ok"; if
// ownApp then says the BCO no longer runs it (stock was flashed back in the meantime), the
// image is installed again. The image is trusted because it came with the signed OS, not
// because it is on the allowlist. The bool says whether the BCO now runs a newly written
// image.
func AutoInstall(
	ctx context.Context, image, marker string, ownApp func() bool,
	flash func(context.Context, []byte) (string, error), log io.Writer,
) (bool, error) {
	img, err := os.ReadFile(image)
	if err != nil {
		return false, fmt.Errorf("read the BCO image: %w", err)
	}

	sum := sha256.Sum256(img)
	want := hex.EncodeToString(sum[:])
	tried := 0

	state := readMarker(marker, want)
	switch {
	case state.installed:
		if ownApp() {
			return false, nil
		}

		fmt.Fprint(log, "[bco] BCO no longer runs the own app\n")
	case state.tried:
		tried = state.attempts

		if ownApp() {
			return false, nil
		}

		fmt.Fprintf(log, "[bco] BCO runs no own app after %d failed flash(es); trying again\n", tried)
	default:
		// Never tried, or another image: install it.
	}

	err = os.MkdirAll(filepath.Dir(marker), markerDirMode)
	if err != nil {
		return false, fmt.Errorf("BCO image marker: %w", err)
	}

	err = AllowImage(img, want)
	if err != nil {
		return false, err
	}

	tried++

	err = atomicjson.WriteFile(marker, []byte(fmt.Sprintf("%s %d\n", want, tried)), markerMode)
	if err != nil {
		return false, fmt.Errorf("BCO image marker: %w", err)
	}

	fmt.Fprintf(log, "[bco] installing the OS's BCO image %s (attempt %d)\n", want[:16], tried)

	ver, err := flash(ctx, img)
	if err != nil {
		return false, fmt.Errorf("BCO image %s: %w", want[:16], err)
	}

	fmt.Fprintf(log, "[bco] BCO runs %s\n", ver)

	err = atomicjson.WriteFile(marker, []byte(want+" ok\n"), markerMode)
	if err != nil {
		return true, fmt.Errorf("BCO image marker: %w", err)
	}

	return true, nil
}

// markerState is what the marker records about one image.
type markerState struct {
	installed bool // flashed and marked " ok"
	tried     bool // flashed before without success
	attempts  int  // how often, when tried
}

// readMarker reads marker for the image whose sha256 is want. A missing marker, or one
// for another image, means never tried.
func readMarker(marker, want string) markerState {
	b, _ := os.ReadFile(marker)
	fields := strings.Fields(string(b))

	switch {
	case len(fields) == markerFields && fields[0] == want && fields[1] == "ok":
		return markerState{installed: true, tried: false, attempts: 0}
	case len(fields) >= 1 && fields[0] == want:
		attempts := 1 // a marker from before the count

		if len(fields) == markerFields {
			count, err := strconv.Atoi(fields[1])
			if err == nil {
				attempts = count
			}
		}

		return markerState{installed: false, tried: true, attempts: attempts}
	default:
		return markerState{installed: false, tried: false, attempts: 0}
	}
}

// RetryInstall says whether a failed AutoInstall is tried again: only while the BCO runs no
// app of ours, and so the speaker has no network to lose.
func RetryInstall(err error, ownApp func() bool) bool { return err != nil && !ownApp() }

// RetryDelay is the wait before the nth retry of a failed install (n from 1): 5 minutes,
// doubling up to an hour, so a BCO that keeps failing is not erased every few minutes.
func RetryDelay(n int) time.Duration {
	d := firstRetryDelay
	for i := 1; i < n && d < time.Hour; i++ {
		d *= 2
	}

	return min(d, time.Hour)
}
