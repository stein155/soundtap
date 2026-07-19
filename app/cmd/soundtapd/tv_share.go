// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/hw/model"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/supervise"
)

// The ST300's processor plays the TV itself, so the Mixer has nothing of it to give a
// multiroom group. The processor does send its input back to the module, on McASP0's
// receive side (kernel patch 0011, ti,dit-rx-i2s). While the bar plays the TV and leads a
// group, that capture goes to the followers through the Snapcast tap in place of the
// Mixer's silence. The bar keeps playing the TV through the processor.

// tvSharing tells tapWriter.Tap to leave the tap to shareTV.
var tvSharing atomic.Bool

// The processor sends 44100 Hz S32_LE (measured 2026-11-08); the card declares
// that, and plughw turns it into the tap's S16_LE at 48000 like the AUX capture does.
const (
	tvCaptureDev = "plughw:0,0"
	// tvRetry bounds the restarts of a capture that keeps failing.
	tvRetry = 5 * time.Second
)

// runTVShare starts and stops the capture as the bar's source and group change.
func runTVShare(ctx context.Context, sp *speakerstate.Speaker, tapPath string, tapOn *atomic.Bool, log *slog.Logger) {
	if board.Name != model.ST300 || tapPath == "" {
		return
	}
	wanted := func() bool {
		st := sp.StatusSnapshot()
		return tapOn.Load() && !st.Standby && st.Source == sourceProduct
	}
	for ctx.Err() == nil {
		if !wanted() {
			sleepCtx(ctx, time.Second)
			continue
		}
		shareCtx, cancel := context.WithCancel(ctx)
		go func() {
			stillWanted := true
			for stillWanted {
				stillWanted = sleepCtx(shareCtx, time.Second) && wanted()
			}
			cancel()
		}()
		log.Info("sharing the TV input with the group")
		err := shareTV(shareCtx, tapPath)
		failed := shareCtx.Err() == nil
		cancel()
		if !failed {
			log.Info("TV share off")
			continue
		}
		log.Warn("TV share stopped", "err", err)
		sleepCtx(ctx, tvRetry)
	}
}

// shareTV copies the capture into the tap until ctx ends or the capture fails.
func shareTV(ctx context.Context, tapPath string) error {
	tap, err := os.OpenFile(tapPath, os.O_WRONLY|syscall.O_NONBLOCK, 0) //nolint:gosec // AUDIO_TAP comes from the init script
	if err != nil {
		return err
	}
	defer tap.Close()
	cmd := exec.CommandContext(ctx, "arecord", "-q", "-D", tvCaptureDev, "-t", "raw",
		"-f", "S16_LE", "-c", "2", "-r", strconv.Itoa(audio.SampleRate))
	cmd.Stderr = supervise.ChildLog("arecord")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	tvSharing.Store(true)
	defer tvSharing.Store(false)
	in := make([]byte, 4*1024)
	var copyErr error
	for {
		n, err := io.ReadFull(out, in)
		if n > 0 {
			writeTap(int(tap.Fd()), in[:n-n%4])
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				copyErr = err
			}
			break
		}
	}
	waitErr := cmd.Wait()
	if copyErr != nil {
		return copyErr
	}
	return waitErr
}
