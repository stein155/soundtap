// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"syscall"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/hw/model"
	"github.com/stein155/soundtap/app/internal/supervise"
)

// The WLA's jack is analog and optical at once, and stock plays on both. The
// optical side is McASP1, clocked by its own 11.2896 MHz oscillator at 44100 Hz, so it
// cannot share the analog card's 46875 Hz stream: it gets its own aplay and its own
// resample from the Mixer's block, with the same volume.
const (
	spdifCard = "/proc/asound/SoundTapSPDIF"
	spdifDev  = "plughw:CARD=SoundTapSPDIF,DEV=0"
	spdifRate = 44100
	// spdifPipeBytes is Linux's default pipe size, what aplay's stdin has.
	spdifPipeBytes = 64 << 10
)

// spdifTee writes each block to the analog sink, which paces the Mixer, and to the
// optical output, which never holds it up.
type spdifTee struct {
	primary io.Writer
	spdif   *audio.S32Writer
	buf     []byte
}

func (t *spdifTee) Write(p []byte) (int, error) { return t.WriteGain(p, 1) }

func (t *spdifTee) WriteGain(p []byte, gain float64) (int, error) {
	_, _ = t.spdif.WriteGain(p, gain) // the optical side drops what it cannot take
	return audio.WriteGain(t.primary, p, gain, &t.buf)
}

// dropWriter writes whole blocks to a non-blocking pipe and drops a block that does not
// fit, so a partial write never shifts the frame alignment.
// the two clocks drift apart by tens of ppm, so about once an hour the optical
// side drops or repeats a few ms (pipe full, or an aplay underrun). Match the rates with
// the hw_ptr if anyone hears it.
type dropWriter struct {
	fd   int
	size int
}

func (w *dropWriter) fits(n int) bool {
	q, err := pipeQueued(uintptr(w.fd))
	return err == nil && q+n <= w.size
}

func (w *dropWriter) Write(p []byte) (int, error) {
	if !w.fits(len(p)) {
		return len(p), nil
	}
	_, _ = syscall.Write(w.fd, p)
	return len(p), nil
}

// withSPDIF tees sink to the optical output when this box has one. It returns sink and
// closeSink unchanged when it has none or aplay does not start.
func withSPDIF(ctx context.Context, sink io.Writer, closeSink func() error, log *slog.Logger) (io.Writer, func() error) {
	if board.Name != model.WLA {
		return sink, closeSink
	}
	if _, err := os.Stat(spdifCard); err != nil {
		return sink, closeSink
	}
	cmd := exec.CommandContext(ctx, "aplay", "-q", "-D", spdifDev, "-t", "raw",
		"-f", "S32_LE", "-r", fmt.Sprint(spdifRate), "-c", "2",
		fmt.Sprintf("--buffer-time=%d", audio.SinkBufferMicros),
		fmt.Sprintf("--period-time=%d", audio.SinkPeriodMicros))
	cmd.Stderr = supervise.ChildLog("aplay-spdif")
	stdin, err := cmd.StdinPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		log.Warn("optical output off", "err", err)
		return sink, closeSink
	}
	stop := func() error {
		_ = stdin.Close()
		_ = cmd.Wait()
		return closeSink()
	}
	// StdinPipe wraps the *os.File; Fd puts it in blocking mode, so switch it back after.
	f, ok := stdin.(interface{ Fd() uintptr })
	if !ok {
		log.Warn("optical output off", "err", "no pipe descriptor")
		_ = stdin.Close()
		_ = cmd.Wait()
		return sink, closeSink
	}
	fd := int(f.Fd())
	if err := syscall.SetNonblock(fd, true); err != nil {
		log.Warn("optical output off", "err", err)
		_ = stdin.Close()
		_ = cmd.Wait()
		return sink, closeSink
	}
	log.Info("optical output on", "dev", spdifDev, "rate", spdifRate)
	tee := &spdifTee{primary: sink, spdif: audio.NewS32Writer(&dropWriter{fd: fd, size: spdifPipeBytes}, spdifRate)}
	return tee, stop
}
