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
	"sync"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/childproc"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// auxCaptureCmd records the line input in the bus format. Under the mainline kernel
// nothing wires the analogue input to the amplifier the way stock snd-shelby2 does, so
// AUX is captured and played through the Mixer like every other source: volume, bass
// and announcements apply to it, and the Mixer stays the only DAC writer (ADR 0012).
// A variable for tests.
//
//nolint:gochecknoglobals // a test seam: tests capture from a script
var auxCaptureCmd = func(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, "arecord", "-q", "-D", "plughw:0,0", "-f", "S16_LE", "-r", "48000", "-c", "2",
		fmt.Sprintf("--period-size=%d", auxCapturePeriodFrames),
		fmt.Sprintf("--buffer-size=%d", auxCaptureBufferFrames),
		"-t", "raw", "-")
}

// AUX's delay is what sits between the line input and the DAC. Every other source
// keeps the deep defaults; these hold only while AUX plays.
const (
	// auxCapturePeriodFrames is how long a sample waits in the capture before arecord hands
	// it on: 10.7 ms. The codec runs at 46875 Hz, 125/128 of the bus rate, so plug converts
	// exactly 500 hardware frames per period; the McASP takes any period (rx-num-evt 1). A
	// 960-frame period rounded to 938, captured 530 ppm slow and drained the live buffer
	// into an underrun every 40 s on the WLA.
	auxCapturePeriodFrames = 512
	// auxCaptureBufferFrames only absorbs a late arecord; it adds no delay while it keeps up.
	auxCaptureBufferFrames = 10240
	// The DAC ring while AUX owns it: 80 ms in 10 ms periods, against 320 ms in 80 ms for
	// every other source. aplay holds a period before it writes, so both count. What
	// covers a stalled Mixer is the ring, the pipe and aplay's period, about 100 ms;
	// the deep ring stays for the network sources, which need it. A 40 ms ring
	// underran on the ST20 every 15-60 s and once in 10 min on the ST10 (2026-11-12).
	auxSinkBufferMicros = 80_000
	auxSinkPeriodMicros = 10_000
	auxSinkPipeBytes    = 4096 // one page, the smallest pipe Linux makes: 10.9 ms of S32
	// auxSinkFrameBytes is a stereo S32 frame, the format the pipe into aplay carries.
	auxSinkFrameBytes = 8
	// A capture that exits is restarted after a backoff that grows by auxCaptureBackoffGrowth
	// up to auxCaptureMaxBackoff.
	auxCaptureBackoffGrowth = 2
	auxCaptureMaxBackoff    = 30 * time.Second
)

// sinkTiming is how deep the chain after the Mixer runs: aplay's ring and period and the
// pipe into it.
type sinkTiming struct{ bufferMicros, periodMicros, pipeBytes int }

// defaultSinkTiming is the chain every source but AUX plays through.
func defaultSinkTiming() sinkTiming {
	return sinkTiming{audio.SinkBufferMicros, audio.SinkPeriodMicros, audio.SinkPipeBytes}
}

// auxSinkTiming is the shallow chain AUX plays through.
func auxSinkTiming() sinkTiming {
	return sinkTiming{auxSinkBufferMicros, auxSinkPeriodMicros, auxSinkPipeBytes}
}

// auxOwnsSink is set while AUX is the source; the ALSA sink then reopens on auxSinkTiming.
//
//nolint:gochecknoglobals // process-wide state: the source router sets it, the DAC pump reads it
var auxOwnsSink atomic.Bool

func currentSinkTiming() sinkTiming {
	if auxOwnsSink.Load() {
		return auxSinkTiming()
	}

	return defaultSinkTiming()
}

// auxBudget is AUX's worst-case delay from the line input to the DAC: a capture period,
// the Mixer block it fills, the live buffer (the block the Mixer is writing counts in
// it), the pipe into aplay, the period aplay holds and its ring.
func auxBudget() time.Duration {
	capture := auxCapturePeriodFrames * time.Second / audio.SampleRate
	pipe := auxSinkPipeBytes * time.Second / (auxSinkFrameBytes * mcaspCardRate)

	return capture + blockDuration + auxLiveBuffer + pipe + (auxSinkPeriodMicros+auxSinkBufferMicros)*time.Microsecond
}

// timedSink is the ALSA sink. When the wanted sinkTiming changes it reopens aplay on the
// Mixer's own write, so Run keeps its state, and then flushes: what the source queued
// while the old ring played out would otherwise stay in front of AUX as delay.
// the reopen stops and restarts the McASP, as a pump restart does.
type timedSink struct {
	open  func(sinkTiming) (io.Writer, func() error, error)
	flush func()
	log   *slog.Logger

	mu     sync.Mutex
	timing sinkTiming
	w      io.Writer
	close  func() error
	closed bool
	buf    []byte // the Mixer's goroutine only
}

func openTimedSink(ctx context.Context, dev string, flush func(), log *slog.Logger) (io.Writer, func() error, error) {
	sink := &timedSink{
		open: nil, flush: flush, log: log,
		mu: sync.Mutex{}, timing: currentSinkTiming(), w: nil, close: nil, closed: false, buf: nil,
	}
	sink.open = func(t sinkTiming) (io.Writer, func() error, error) {
		opened, err := openAudioSink(ctx, dev, t, log)
		if err != nil {
			return nil, nil, err
		}

		sink, closeSink := opened.w, opened.close

		sink, closeSink = withSPDIF(ctx, sink, closeSink, log)

		return sink, closeSink, nil
	}

	var err error

	sink.w, sink.close, err = sink.open(sink.timing)
	if err != nil {
		return nil, nil, err
	}

	return sink, sink.Close, nil
}

func (s *timedSink) Write(p []byte) (int, error) { return s.WriteGain(p, 1) }

func (s *timedSink) WriteGain(pcm []byte, gain float64) (int, error) {
	w, err := s.current()
	if err != nil {
		return 0, err
	}

	n, err := audio.WriteGain(w, pcm, gain, &s.buf)
	if err != nil {
		return n, fmt.Errorf("write DAC: %w", err)
	}

	return n, nil
}

func (s *timedSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	return s.close()
}

// current is the open sink, reopened first if the wanted timing changed. Closing the old
// one lets aplay play out its ring.
func (s *timedSink) current() (io.Writer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	want := currentSinkTiming()
	if s.closed || want == s.timing {
		return s.w, nil
	}

	_ = s.close()

	sink, closeSink, err := s.open(want)
	if err != nil {
		s.closed = true

		return nil, fmt.Errorf("reopen DAC: %w", err)
	}

	s.w, s.close, s.timing = sink, closeSink, want
	s.log.Info("DAC reopened",
		"buffer_us", want.bufferMicros, "period_us", want.periodMicros, "pipe_bytes", want.pipeBytes)
	s.flush()

	return sink, nil
}

// runAuxCapture keeps a capture writing into the bus for as long as AUX is selected.
// A capture that exits while AUX stays selected is restarted after a backoff that
// doubles up to 30 s.
func runAuxCapture(ctx context.Context, speaker *speakerstate.Speaker, bus string, log *slog.Logger) {
	var (
		stop  context.CancelFunc
		done  <-chan struct{}
		retry <-chan time.Time
	)

	backoff := time.Second

	for {
		changed := speaker.Changed()

		want := speaker.StatusSnapshot().Source == sourceAux
		if want && stop == nil && retry == nil {
			stop, done = startAuxCapture(ctx, bus, log)
		}

		if !want && stop != nil {
			stop()
			<-done
			stop, done, backoff = nil, nil, time.Second
		}

		select {
		case <-ctx.Done():
			if stop != nil {
				stop()
				<-done
			}

			return
		case <-changed:
		case <-done:
			stop, done = nil, nil

			log.Warn("aux capture exited; retrying", "after", backoff)
			retry = time.After(backoff)
			backoff = min(auxCaptureBackoffGrowth*backoff, auxCaptureMaxBackoff)
		case <-retry:
			retry = nil
		}
	}
}

// startAuxCapture starts one capture into bus. done closes once it has exited.
func startAuxCapture(ctx context.Context, bus string, log *slog.Logger) (context.CancelFunc, <-chan struct{}) {
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	// soundtapd holds the bus open for reading, so this open does not block.
	out, err := os.OpenFile(bus, os.O_WRONLY, 0)
	if err != nil {
		log.Error("aux capture: open bus", "err", err)
		close(done)

		return cancel, done
	}

	cmd := auxCaptureCmd(cctx)
	cmd.Stdout = out
	childproc.DieWithParent(cmd)

	startErr := cmd.Start()
	if startErr != nil {
		log.Error("aux capture: start", "err", startErr)

		_ = out.Close()

		close(done)

		return cancel, done
	}

	log.Info("aux capture started", "pid", cmd.Process.Pid,
		"capture_period_frames", auxCapturePeriodFrames, "capture_buffer_frames", auxCaptureBufferFrames,
		"live_buffer", auxLiveBuffer, "dac_buffer_us", auxSinkBufferMicros, "dac_period_us", auxSinkPeriodMicros,
		"sink_pipe_bytes", auxSinkPipeBytes, "budget", auxBudget())
	go func() {
		err := cmd.Wait()
		_ = out.Close()

		log.Info("aux capture stopped", "err", err)
		close(done)
	}()

	return cancel, done
}
