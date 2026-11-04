// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"math/cmplx"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestAudioRatesCompensateST10Clock(t *testing.T) {
	if SampleRate != 48000 || SinkRate != 48000 {
		t.Fatalf("audio rates bus=%d sink=%d", SampleRate, SinkRate)
	}
}

func TestRateConverterCompensatesMainlineClock(t *testing.T) {
	r := rateConverter{outRate: 46875}
	dst := make([]byte, blockBytes)
	frames := 0
	for i := 0; i < 50; i++ {
		frames += len(r.convert(dst, pcm(blockFrames, 1000))) / frameBytes
	}
	if frames < 46874 || frames > 46875 {
		t.Fatalf("converted one second to %d frames, want 46874..46875", frames)
	}
}

func TestFadePCMRemovesHardEdges(t *testing.T) {
	b := pcm(blockFrames, 10000)
	fadePCM(b, true, 0, 0)
	if first, last := sampleAt(b, 0), sampleAt(b, len(b)/2-2); first >= 100 || last != 10000 {
		t.Fatalf("fade in first=%d last=%d", first, last)
	}
	fadePCM(b, false, 10000, 10000)
	if first, last := sampleAt(b, 0), sampleAt(b, len(b)/2-2); first >= 10000 || last != 0 {
		t.Fatalf("fade out first=%d last=%d", first, last)
	}
	scaleInto(b, b, 1)
	if first := sampleAt(b, 0); first == 0 {
		t.Fatal("in-place scaling erased fade")
	}
}

// pcm builds a block of n stereo frames, every sample set to v.
func pcm(n int, v int16) []byte {
	b := make([]byte, n*frameBytes)
	for i := 0; i+1 < len(b); i += 2 {
		binary.LittleEndian.PutUint16(b[i:], uint16(v))
	}
	return b
}

func sampleAt(b []byte, i int) int16 {
	return int16(binary.LittleEndian.Uint16(b[i*2:]))
}

func TestMixIntoSaturates(t *testing.T) {
	dst := make([]byte, 8)
	src := pcm(2, 20000)
	ann := pcm(2, 20000)
	mixInto(dst, src, ann, 1.0, 1.0) // 40000 > int16 max -> clamp
	for i := 0; i < 4; i++ {
		if got := sampleAt(dst, i); got != 32767 {
			t.Fatalf("sample %d = %d, want 32767 (saturated)", i, got)
		}
	}
	mixInto(dst, pcm(2, -20000), pcm(2, -20000), 1.0, 1.0)
	if got := sampleAt(dst, 0); got != -32768 {
		t.Fatalf("negative clamp = %d, want -32768", got)
	}
}

func TestMixIntoGains(t *testing.T) {
	dst := make([]byte, 4)
	mixInto(dst, pcm(1, 1000), pcm(1, 4000), 0.2, 0.5) // 200 + 2000 = 2200
	if got := sampleAt(dst, 0); got != 2200 {
		t.Fatalf("mixed gain sample = %d, want 2200", got)
	}
}

func TestMixIntoShortInputsAreSilence(t *testing.T) {
	dst := make([]byte, 8)
	// src covers only the first frame; ann is nil. Rest must be silence, not garbage.
	mixInto(dst, pcm(1, 5000), nil, 1.0, 1.0)
	if sampleAt(dst, 0) != 5000 || sampleAt(dst, 1) != 5000 {
		t.Fatalf("first frame not passed through: %v", dst[:4])
	}
	if sampleAt(dst, 2) != 0 || sampleAt(dst, 3) != 0 {
		t.Fatalf("missing src should be silence, got %v", dst[4:])
	}
}

func TestRampToward(t *testing.T) {
	if got := rampToward(1.0, duckLevel, gainStep); got != 1.0-gainStep {
		t.Fatalf("ramp down step = %v", got)
	}
	// Never overshoot the target.
	if got := rampToward(duckLevel+gainStep/2, duckLevel, gainStep); got != duckLevel {
		t.Fatalf("ramp overshoot = %v, want %v", got, duckLevel)
	}
	if got := rampToward(0.5, 0.5, gainStep); got != 0.5 {
		t.Fatalf("ramp at target should hold, got %v", got)
	}
}

// Full duck lifecycle through step(): full -> ducked while clip plays -> restored.
func TestAnnounceDucksThenRestores(t *testing.T) {
	m := NewMixer(io.Discard, false)
	src := pcm(blockFrames, 10000)

	// No announcement: gain stays at full, source passes through untouched.
	out := m.step(src)
	if sampleAt(out, 0) != 10000 {
		t.Fatalf("idle gain not 1.0: sample=%d", sampleAt(out, 0))
	}

	// A clip longer than the ramp, so the source reaches full duck while it plays.
	clip := pcm(blockFrames*rampBlocks*3, 4000)
	m.Announce(bytes.NewReader(clip), 1.0)

	// Ramp all the way down: after rampBlocks steps the source must sit at duckLevel.
	for i := 0; i < rampBlocks; i++ {
		m.step(src)
	}
	if d := m.curGain - duckLevel; d > 1e-9 || d < -1e-9 {
		t.Fatalf("after ramp, curGain=%v want %v", m.curGain, duckLevel)
	}
	if !m.Announcing() {
		t.Fatalf("clip should still be playing during duck")
	}

	// Drain the rest of the clip; announcement then clears and heads back to full.
	for m.Announcing() {
		m.step(src)
	}
	for i := 0; i < rampBlocks; i++ {
		m.step(src)
	}
	if d := m.curGain - 1.0; d > 1e-9 || d < -1e-9 {
		t.Fatalf("after restore, curGain=%v want 1.0", m.curGain)
	}
	out = m.step(src)
	if sampleAt(out, 0) != 10000 {
		t.Fatalf("restored source not at full: sample=%d", sampleAt(out, 0))
	}
}

// While ducked, the clip is actually audible on top of the (quieted) source.
func TestAnnounceMixesOverSource(t *testing.T) {
	m := NewMixer(io.Discard, false)
	m.Announce(bytes.NewReader(pcm(blockFrames*4, 8000)), 1.0)
	out := m.step(pcm(blockFrames, 10000))
	// First block: gain = 1-step, source ~9000 + clip 8000 well above bare source.
	if got := sampleAt(out, 0); got <= 10000 {
		t.Fatalf("clip not mixed over source: sample=%d, want >10000", got)
	}
}

// A live source stays silent until Unhold, then fades in over holdFade.
func TestLiveSourceIsHeldUntilUnhold(t *testing.T) {
	m := NewMixer(io.Discard, false)
	src := pcm(blockFrames, 10000)
	m.SetLive(FollowerBuffer)
	for i := 0; i < 100; i++ {
		if got := sampleAt(m.step(src), 0); got != 0 {
			t.Fatalf("held block %d = %d, want silence", i, got)
		}
	}
	m.Unhold()
	if got := sampleAt(m.step(src), 0); got <= 0 || got >= 1000 {
		t.Fatalf("first released block = %d, want the start of a fade", got)
	}
	for i := 0; i < int(holdFade/(20*time.Millisecond)); i++ {
		m.step(src)
	}
	if got := sampleAt(m.step(src), 0); got != 10000 {
		t.Fatalf("after the fade = %d, want full level", got)
	}
}

// A follower joining while another source plays closes the gate over one faded block,
// not in one step from full level to silence: that step popped the ST20.
func TestHoldFadesOutASoundingSource(t *testing.T) {
	m := NewMixer(io.Discard, false)
	src := pcm(blockFrames, 10000)
	m.step(src) // the previous source is playing
	m.SetLive(FollowerBuffer)
	out := m.step(src)
	if first, last := sampleAt(out, 0), sampleAt(out, 2*blockFrames-1); first < 9500 || last != 0 {
		t.Fatalf("closing block runs %d..%d, want a fade from about 10000 to 0", first, last)
	}
	for i := range 10 {
		if got := sampleAt(m.step(src), 0); got != 0 {
			t.Fatalf("held block %d = %d, want silence", i, got)
		}
	}
}

func TestMasterVolume(t *testing.T) {
	m := NewMixer(io.Discard, false)
	src := pcm(blockFrames, 10000)

	// Full volume by default: source passes through.
	if got := sampleAt(m.step(src), 0); got != 10000 {
		t.Fatalf("default volume sample=%d, want 10000", got)
	}
	// Bose volume 50 is -31.25 dB, not half PCM amplitude. The first volume is taken at once.
	m.SetVolume(50)
	if got := sampleAt(m.step(src), 0); got < 273 || got > 275 {
		t.Fatalf("50%% volume sample=%d, want about 274", got)
	}
	settle := func() []byte {
		var out []byte
		for range 80 {
			out = m.step(src)
		}
		return out
	}
	// Zero volume mutes.
	m.SetVolume(0)
	if got := sampleAt(settle(), 0); got != 0 {
		t.Fatalf("0%% volume sample=%d, want 0", got)
	}
	// Out-of-range clamps to full.
	m.SetVolume(200)
	if got := sampleAt(settle(), 0); got != 10000 {
		t.Fatalf("clamped volume sample=%d, want 10000", got)
	}
}

// A later volume change glides: no block jumps more than a quarter of the way.
func TestVolumeChangeGlides(t *testing.T) {
	m := NewMixer(io.Discard, false)
	src := pcm(blockFrames, 10000)
	m.SetVolume(100)
	m.step(src)
	m.SetVolume(0)
	prev := 10000
	for i := range 80 {
		got := int(sampleAt(m.step(src), 0))
		if got > prev || prev-got > 2500 {
			t.Fatalf("block %d: %d after %d, want a step of at most a quarter", i, got, prev)
		}
		prev = got
	}
	if prev != 0 {
		t.Fatalf("settled at %d, want 0", prev)
	}
}

// Moving the bass keeps the filter's state; a reset is a discontinuity in the output.
func TestBassChangeKeepsFilterState(t *testing.T) {
	m := NewMixer(io.Discard, false)
	src := pcm(blockFrames, 10000)
	m.SetBass(-9)
	for range 20 {
		m.step(src)
	}
	z := m.bassFilter[0].z1
	m.SetBass(-6)
	if m.bassFilter[0].z1 != z || z == 0 {
		t.Fatalf("z1 = %g after SetBass, want %g kept", m.bassFilter[0].z1, z)
	}
}

func TestBassLowShelf(t *testing.T) {
	m := NewMixer(io.Discard, false)
	src := pcm(blockFrames, 10000)
	if got := sampleAt(m.step(src), 0); got != 10000 {
		t.Fatalf("neutral bass sample=%d, want 10000", got)
	}
	m.SetBass(-9)
	var out []byte
	for range 20 {
		out = m.step(src)
	}
	// DC sits on the shelf floor: -9 steps * 1.22 dB = -10.98 dB.
	if got := sampleAt(out, len(out)/2-2); got < 2780 || got > 2880 {
		t.Fatalf("-9 settled bass sample=%d, want about 2830", got)
	}
	m.SetBass(9)
	if got := sampleAt(m.step(src), 0); got != 10000 {
		t.Fatalf("clamped bass sample=%d, want 10000", got)
	}
}

func TestStockVolumeCurve(t *testing.T) {
	for _, tc := range []struct {
		volume int
		db     float64
	}{{1, -86.875}, {10, -66.39}, {50, -31.25}, {68, -20}, {84, -10}, {92, -5}, {100, 0}} {
		gotDB := 20 * math.Log10(stockVolumeGain(tc.volume))
		if math.Abs(gotDB-tc.db) > 0.01 {
			t.Errorf("volume %d = %.3f dB, want %.3f", tc.volume, gotDB, tc.db)
		}
	}
}

// The table stockVolumeGain approximates is real and readable: APServer carries it in
// .rodata at 0x2a52d0, 101 signed 32-bit entries in Q22, where 4194304 is unity gain.
// These are sampled straight out of that array, including both sides of each knee, so
// a change to the formula that drifts off Bose's curve fails here rather than on a
// speaker. Read on 2026-09-19 from opt/Bose/APServer in this board's stock rootfs.
func TestStockVolumeCurveMatchesAPServerTable(t *testing.T) {
	const unity = 4194304.0
	for _, tc := range []struct {
		volume int
		q22    int
	}{
		{1, 190}, {5, 587}, {9, 1815}, {10, 2009}, {11, 2225}, {25, 9266},
		{49, 106883}, {50, 114857}, {51, 123426}, {75, 694080}, {99, 3903102},
		{100, 4194304},
	} {
		want := float64(tc.q22) / unity
		got := stockVolumeGain(tc.volume)
		// 0.02 dB: the formula is a fit, and the widest gap over all 101 entries
		// is 0.01 dB.
		if d := math.Abs(20 * math.Log10(got/want)); d > 0.02 {
			t.Errorf("volume %d = %.6f, want %.6f (off by %.3f dB)", tc.volume, got, want, d)
		}
	}
	if got := stockVolumeGain(0); got != 0 {
		t.Errorf("volume 0 = %v, want silence", got)
	}
}

func TestArbiterExclusiveOwnershipPreempts(t *testing.T) {
	var preempted []string
	a := NewArbiter(NewMixer(io.Discard, false), func(old string) {
		preempted = append(preempted, old)
	})

	a.Acquire("TUNEIN")
	if a.Owner() != "TUNEIN" {
		t.Fatalf("owner=%q", a.Owner())
	}
	a.Acquire("TUNEIN") // idempotent, no preempt
	a.Acquire("AIRPLAY")
	a.Acquire("BLUETOOTH")
	if a.Owner() != "BLUETOOTH" {
		t.Fatalf("owner=%q", a.Owner())
	}
	want := []string{"TUNEIN", "AIRPLAY"}
	if len(preempted) != len(want) || preempted[0] != want[0] || preempted[1] != want[1] {
		t.Fatalf("preempted=%v want %v", preempted, want)
	}

	a.Release("TUNEIN") // not the owner -> no-op
	if a.Owner() != "BLUETOOTH" {
		t.Fatalf("release of non-owner changed owner to %q", a.Owner())
	}
	a.Release("BLUETOOTH")
	if a.Owner() != "" {
		t.Fatalf("owner after release=%q want empty", a.Owner())
	}
}

// A stream that ends mid-frame yields only whole frames: Run slices the last frame
// of every block and panicked on a one-byte tail.
func TestFeedDropsATrailingPartialFrame(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan pcmBlock, 4)
	go NewMixer(io.Discard, true).feed(ctx, bytes.NewReader(make([]byte, 2*frameBytes+1)), ch)
	select {
	case b := <-ch:
		if len(b.pcm) != 2*frameBytes {
			t.Fatalf("block = %d bytes, want %d", len(b.pcm), 2*frameBytes)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no block")
	}
}

// A source stopping (writer closes the FIFO) must NOT kill the pump: the next source
// has to still reach the sink. Prod opens the bus O_RDWR so the FIFO never EOFs on a
// gap; this reproduces that across a real FIFO with a writer close in the middle.
func TestFeedSurvivesWriterClose(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bus")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	rw, err := os.OpenFile(p, os.O_RDWR, 0) // reader end, prod style
	if err != nil {
		t.Fatalf("open rdwr: %v", err)
	}
	defer rw.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan pcmBlock, 4)
	go NewMixer(io.Discard, true).feed(ctx, rw, ch)

	write := func(v int16) {
		w, err := os.OpenFile(p, os.O_WRONLY, 0)
		if err != nil {
			t.Errorf("open wronly: %v", err)
			return
		}
		w.Write(pcm(blockFrames, v))
		w.Close() // source stops — the gap that used to kill the feeder
	}

	go write(1111)
	if got := recvSample(t, ch); got != 1111 {
		t.Fatalf("first block sample=%d want 1111", got)
	}
	// Second source after the first closed — must still get through.
	go write(2222)
	if got := recvSample(t, ch); got != 2222 {
		t.Fatalf("feeder died on writer close: second block sample=%d want 2222", got)
	}
}

func recvSample(t *testing.T, ch <-chan pcmBlock) int16 {
	t.Helper()
	select {
	case b := <-ch:
		return sampleAt(b.pcm, 0)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for a source block")
		return 0
	}
}

// Run drains the source bus to the sink and stops promptly on ctx cancel.
func TestRunPumpsSourceToSink(t *testing.T) {
	var sink lockedBuffer
	m := NewMixer(&sink, true) // paced: buffer never blocks
	pr, pw := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx, pr); close(done) }()

	// Push a constant tone through the bus.
	go func() {
		pw.Write(pcm(blockFrames*sourceBufferBlocks, 6000))
	}()

	// Wait for the property this test is about instead of sleeping a fixed 250 ms. The
	// mixer paces itself on a 20 ms ticker, so under -race on a loaded machine the writer
	// can be starved long enough that only prebuffer silence has landed by the deadline.
	for deadline := time.Now().Add(5 * time.Second); !bytes.Contains(sink.Bytes(), pcm(1, 6000)); {
		if time.Now().After(deadline) {
			t.Fatal("source tone never reached the sink")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if sink.Len() == 0 || sink.Len()%blockBytes != 0 {
		t.Fatalf("sink got %d bytes, want nonzero multiple of %d", sink.Len(), blockBytes)
	}
	// The tone (6000) must appear somewhere in the output.
	if !bytes.Contains(sink.Bytes(), pcm(1, 6000)) {
		t.Fatalf("source tone never reached the sink")
	}
	h := m.Health()
	if h.SourceBytes == 0 || h.SourceNonzeroBlocks == 0 || h.MixedNonzeroBlocks == 0 || h.SinkNonzeroBlocks == 0 {
		t.Fatalf("PCM health did not observe source/output audio: %+v", h)
	}
	if h.SinkBytes != uint64(sink.Len()) {
		t.Fatalf("sink health bytes=%d, writer received %d", h.SinkBytes, sink.Len())
	}
}

func TestPacedRunDoesNotFloodSilence(t *testing.T) {
	sink := &countingWriter{first: make(chan struct{})}
	m := NewMixer(sink, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = m.Run(ctx, bytes.NewReader(nil)); close(done) }()
	<-sink.first
	// Paced is at most one block per block time, however long the run took: a busy
	// machine stretches the run, it does not fail it. Timed from the first write, not
	// from when this goroutine woke: on a loaded machine the writes before that wake-up
	// counted, and the time they took did not.
	start := sink.at
	for deadline := start.Add(5 * time.Second); sink.writes.Load() < 3; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("paced mixer wrote only %d silence blocks in 5s", sink.writes.Load())
		}
	}
	cancel()
	<-done
	elapsed := time.Since(start)
	if blocks, most := sink.writes.Load(), uint64(elapsed/(blockFrames*time.Second/SampleRate))+3; blocks > most {
		t.Fatalf("paced mixer wrote %d silence blocks in %v, want at most %d", blocks, elapsed, most)
	}
	h := m.Health()
	if h.SourceNonzeroBlocks != 0 || h.MixedNonzeroBlocks != 0 {
		t.Fatalf("silence counted as nonzero PCM: %+v", h)
	}
}

type countingWriter struct {
	writes atomic.Uint64
	first  chan struct{}
	once   sync.Once
	at     time.Time // the first write; read only after first is closed
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes.Add(1)
	w.once.Do(func() { w.at = time.Now(); close(w.first) })
	return len(p), nil
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestRunRejectsShortDACWrite(t *testing.T) {
	m := NewMixer(shortWriter{}, false)
	err := m.Run(context.Background(), bytes.NewReader(pcm(blockFrames, 1)))
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte(io.ErrShortWrite.Error())) {
		t.Fatalf("Run error = %v, want short write", err)
	}
}

// The jitter buffer is the fix for the stutter/slow-playback report: a source that
// arrives in bursts must not have silence spliced into it. Feed a whole second of a
// constant tone in irregular chunks and require that every sample the sink receives
// is that tone — a single inserted silence block fails this.
func TestRunDoesNotSpliceSilenceIntoABurstySource(t *testing.T) {
	var sink lockedBuffer
	m := NewMixer(&sink, true)
	pr, pw := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { m.Run(ctx, pr); close(done) }()

	// Three seconds of tone, written in 100 ms bursts with 40 ms of nothing between —
	// far burstier than the real bus and enough to prime the production prebuffer.
	go func() {
		for i := 0; i < 30; i++ {
			pw.Write(pcm(blockFrames*5, 6000))
			time.Sleep(40 * time.Millisecond)
		}
	}()

	// Wait until the sink has taken a decent chunk of the stream AND that chunk has
	// reached the tone. Length alone is not enough: the mixer emits prebuffer silence
	// while it fills, so on a slow or -race-instrumented run 40 blocks of pure silence
	// can arrive before the writer's first burst — and then the assertions below are
	// examining the prebuffer rather than the stream.
	deadline := time.After(5 * time.Second)
	var b []byte
	start := 0
	for {
		b = sink.Bytes()
		for start+1 < len(b) && sampleAt(b, start/2) == 0 {
			start += 2
		}
		if len(b) >= 40*blockBytes && start+1 < len(b) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("sink got %d bytes, tone at %d", len(b), start)
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done

	// Everything from the first tone sample on must be unbroken.
	b = sink.Bytes()
	for i := start; i+1 < len(b); i += 2 {
		if got := sampleAt(b, i/2); got == 0 {
			t.Fatalf("silence spliced into the stream at byte %d of %d", i, len(b))
		}
	}
	if u := m.Health().Underruns; u != 0 {
		t.Fatalf("underruns = %d, want 0 for a source well inside the buffer", u)
	}
}

// Run must hold the stream at silence until the buffer has prebufferBlocks queued,
// so playback starts with slack in both directions rather than on the edge.
func TestRunPrebuffersBeforeReleasingAudio(t *testing.T) {
	var sink lockedBuffer
	m := NewMixer(&sink, true)
	pr, pw := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx, pr)

	// Fewer blocks than the prebuffer target: nothing may reach the sink yet.
	pw.Write(pcm(blockFrames*(prebufferBlocks-2), 6000))
	time.Sleep(150 * time.Millisecond)
	if b := sink.Bytes(); nonzeroPCM(b) {
		t.Fatal("audio released before the buffer was primed")
	}
	// Cross the threshold and it must start.
	pw.Write(pcm(blockFrames*4, 6000))
	deadline := time.After(2 * time.Second)
	for !nonzeroPCM(sink.Bytes()) {
		select {
		case <-deadline:
			t.Fatal("audio never started after the prebuffer filled")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// A live source (a multiroom follower's snapclient) starts after the short buffer: the
// 2 s prebuffer made a follower 2 s late, more than Snapcast's latency can make up.
func TestRunLiveSourceSkipsThePrebuffer(t *testing.T) {
	var sink lockedBuffer
	m := NewMixer(&sink, true)
	m.SetLive(FollowerBuffer)
	m.Unhold() // the hold is TestLiveSourceIsHeldUntilUnhold's; this is about timing
	pr, pw := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx, pr)

	go pw.Write(pcm(blockFrames*(flushBufferBlocks+2), 6000))
	deadline := time.After(2 * time.Second)
	for !nonzeroPCM(sink.Bytes()) {
		select {
		case <-deadline:
			t.Fatal("live audio held back for the full prebuffer")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Flush must drop the whole queue, not a token slice of it: the stale tail after a
// track change is what the user hears as the previous song playing over the new one.
func TestFlushDropsTheWholeJitterBuffer(t *testing.T) {
	m := NewMixer(io.Discard, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan pcmBlock, sourceBufferBlocks)
	pr, pw := io.Pipe()
	go m.feed(ctx, pr, ch)

	go pw.Write(pcm(blockFrames*sourceBufferBlocks, 6000))
	deadline := time.After(2 * time.Second)
	for len(ch) < prebufferBlocks {
		select {
		case <-deadline:
			t.Fatalf("only %d blocks queued", len(ch))
		case <-time.After(10 * time.Millisecond):
		}
	}
	m.Flush()
	for len(ch) > 0 { // Run's half of the flush
		<-ch
	}
	if n := len(ch); n != 0 {
		t.Fatalf("%d blocks survived the flush", n)
	}
}

// A preset switch kills the old ffmpeg and flushes. Whatever it had already written into
// the bus FIFO is the old station; it used to become the first blocks after the flush
// and play as a short blip of the previous station ahead of the new one.
func TestFlushDropsTheOldStreamStillInTheBusFIFO(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("depends on Linux pipe buffering")
	}
	p := filepath.Join(t.TempDir(), "bus")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	rw, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rw.Close()
	m := NewMixer(io.Discard, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan pcmBlock, 1) // full after one block: the feeder blocks like in production
	go m.feed(ctx, rw, ch)

	w, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Write(pcm(blockFrames*3, 1111)); err != nil { // old station, still queued
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	m.Flush()
	seen := m.flushGen.Load()
	// The new station keeps writing, as a real source does. The feeder may drop the
	// first of it along with the old stream; what matters is that nothing old follows.
	go func() {
		for ctx.Err() == nil {
			if _, err := w.Write(pcm(blockFrames, 2222)); err != nil {
				return
			}
		}
	}()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case b := <-ch:
			if b.gen < seen {
				continue // Run drops these
			}
			if v := sampleAt(b.pcm, 0); v != 2222 {
				t.Fatalf("block after the flush has sample %d, want the new stream (2222)", v)
			}
			return
		case <-deadline:
			t.Fatal("new stream never arrived")
		}
	}
}

// lockedBuffer is a bytes.Buffer safe to read while Run writes it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Len()
}

func (l *lockedBuffer) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.b.Bytes()...)
}

// TestSpotifyTrackChangedNoOldPCM is the regression guard for the Spotify track-change
// bleed bug: after a track_changed + Flush(), old-track PCM must stop reaching the sink
// at once. Dropping the jitter buffer is what achieves that — the only old audio the
// Mixer may still emit is the block already in hand when the flush landed. Uses
// distinct, non-overlapping values so fade-in of the new track cannot produce a false
// positive: round(32512*(k+1)/960)=257 has no integer solution for k in 0..959, so the
// old marker (257) never appears in new-PCM fade-in.
func TestSpotifyTrackChangedNoOldPCM(t *testing.T) {
	// The Mixer may emit the block it already held when Flush() landed, and nothing
	// beyond it. Padding the flush with silence used to hide this behind a window; it
	// was removed because silence queues behind the old audio instead of replacing it.
	const inFlightBlocks = 1
	const oldVal = int16(257)   // 0x0101 LE — recognisable old-track marker
	const newVal = int16(32512) // 0x7F00 LE — distinct; fade-in never crosses oldVal

	var sink lockedBuffer
	m := NewMixer(&sink, true) // paced: 20 ms/block

	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx, pr)

	// Write old-track PCM; wait for it to reach the sink AND for the write to finish
	// so the feeder pipe holds no old PCM when Flush() fires.
	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		pw.Write(pcm(blockFrames*(prebufferBlocks+8), oldVal))
	}()
	deadline := time.After(10 * time.Second)
	for !nonzeroPCM(sink.Bytes()) {
		select {
		case <-deadline:
			t.Fatal("old PCM never reached sink")
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case <-oldDone:
	case <-time.After(10 * time.Second):
		t.Fatal("old PCM write did not complete")
	}

	// Flush (simulates Spotify track_changed), let Run process the gen bump, then
	// queue new-track PCM.
	flushMark := len(sink.Bytes())
	m.Flush()
	time.Sleep(30 * time.Millisecond)
	go pw.Write(pcm(blockFrames*(prebufferBlocks+8), newVal))

	// Wait until new-track PCM appears in the sink.
	deadline = time.After(30 * time.Second)
	newPCMAt := -1
	for newPCMAt < 0 {
		b := sink.Bytes()
		for i := 0; i+1 < len(b); i += 2 {
			if int16(binary.LittleEndian.Uint16(b[i:])) == newVal {
				newPCMAt = i
				break
			}
		}
		if newPCMAt >= 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("new PCM never appeared after flush")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// A track boundary must not wait for the 2 s startup prebuffer when new PCM is
	// already available; that made every Spotify song change audibly pause.
	if gap := (newPCMAt - flushMark) / blockBytes; gap > 50 {
		t.Fatalf("track-change gap = %d blocks, want <= 50", gap)
	}

	// Core assertion: past the in-flight block no old-track byte pattern may survive.
	// the window is inFlightBlocks+1, and the extra block is the test's own
	// sampling race, not product slack. flushMark is read just before m.Flush(), and the
	// mixer can legitimately write one more old block in that gap, so from outside there
	// is no way to sample "the sink length at the instant the flush landed". Observed
	// failing on a loaded box at exactly flushMark+1*blockBytes. The guard this test
	// exists for -- old audio flowing on indefinitely after a track change -- is still
	// caught, because anything beyond two blocks still fails. Tighten this only by giving
	// Flush a way to report the sink length it saw; do not just lower the number.
	silenceZoneEnd := flushMark + (inFlightBlocks+1)*blockBytes
	b := sink.Bytes()
	for i := silenceZoneEnd; i+1 < len(b); i += 2 {
		if int16(binary.LittleEndian.Uint16(b[i:])) == oldVal {
			t.Fatalf("old-track PCM (value %d) at byte %d past the in-flight block (end %d, flush mark %d); track-change bleed regression",
				oldVal, i, silenceZoneEnd, flushMark)
		}
	}
}

// TestAnnounceDocksAndRestoresGain verifies the three invariants of the ducking arbiter:
// (a) after Announce() the source-bus gain drops to duckLevel,
// (b) after the announcement clip ends the gain returns to 1.0,
// (c) no silent gap is inserted between announcement end and restored source audio.
// All checks are synchronous through step() — no goroutines, no hardware.
func TestAnnounceDocksAndRestoresGain(t *testing.T) {
	src := pcm(blockFrames, 10000) // constant non-zero source

	// (a) Gain reaches duckLevel after rampBlocks steps.
	m := NewMixer(io.Discard, false)
	m.Announce(bytes.NewReader(pcm(blockFrames*rampBlocks*3, 5000)), 1.0)
	for i := 0; i < rampBlocks; i++ {
		m.step(src)
	}
	if d := math.Abs(m.curGain - duckLevel); d > 1e-9 {
		t.Fatalf("(a) curGain=%.6f after duck ramp, want %.6f", m.curGain, duckLevel)
	}

	// Drain remaining clip so the restore ramp can start.
	for m.Announcing() {
		m.step(src)
	}

	// (b) Gain returns to 1.0 after rampBlocks steps.
	for i := 0; i < rampBlocks; i++ {
		m.step(src)
	}
	if d := math.Abs(m.curGain - 1.0); d > 1e-9 {
		t.Fatalf("(b) curGain=%.6f after restore ramp, want 1.0", m.curGain)
	}

	// (c) No silent gap: the step that drains the clip to EOF must emit non-zero
	// source audio, and the immediately following step must also be non-zero.
	// Clip is exactly rampBlocks blocks; the (rampBlocks+1)'th step processes EOF.
	m2 := NewMixer(io.Discard, false)
	m2.Announce(bytes.NewReader(pcm(blockFrames*rampBlocks, 5000)), 1.0)
	var eofOut []byte
	for i := 0; i <= rampBlocks; i++ {
		eofOut = m2.step(src)
	}
	if !nonzeroPCM(eofOut) {
		t.Fatal("(c) EOF step produced silence — gap inserted at announcement end")
	}
	if out := m2.step(src); !nonzeroPCM(out) {
		t.Fatal("(c) first restored-source block is silent — gap after announcement")
	}
}

// Everything downstream of the jitter buffer is audio a flush can no longer drop, so
// it is what the user hears as the previous track bleeding into the new one. Guard
// the budget. The master volume is applied upstream of it as well, so the same budget
// is how late a volume key is heard.
func TestUnflushableSinkLatencyStaysSmall(t *testing.T) {
	const bytesPerSec = SampleRate * frameBytes
	micros := func(b int) int { return b * 1_000_000 / bytesPerSec }
	total := micros(blockBytes) + // the block the feeder holds
		micros(BusPipeBytes) + // source FIFO
		micros(SinkPipeBytes) + // aplay stdin
		SinkBufferMicros // the DAC ring
	if total > 500_000 {
		t.Fatalf("unflushable sink latency = %d us, want <= 500000", total)
	}
	// The DAC must be drained in fine grain: a coarse period drains the jitter
	// buffer in bursts, which is what made it collapse into inserted silence.
	if SinkPeriodMicros > 100_000 {
		t.Fatalf("sink period = %d us, too coarse for a %d us jitter budget",
			SinkPeriodMicros, sourceWait/time.Microsecond)
	}
	// The wait for a late block must never outlast the DAC ring, or waiting for the
	// source would itself starve the hardware.
	if sourceWait >= time.Duration(SinkBufferMicros)*time.Microsecond {
		t.Fatalf("sourceWait %v >= DAC ring %d us", sourceWait, SinkBufferMicros)
	}
}

// stalledReader blocks forever on the first read, the way an announcement fed by a
// stalled HTTP fetch or a wedged ffmpeg does.
type stalledReader struct{ released chan struct{} }

func (s *stalledReader) Read([]byte) (int, error) {
	<-s.released
	return 0, io.EOF
}

// An announcement that never produces a byte must not take the device down with it.
// stepInto used to read the clip while holding the mixer's lock, so a stalled clip
// stopped all PCM to the DAC and blocked SetVolume, Announce and Announcing behind
// the same mutex: a doorbell that could not be fetched silenced the speaker.
func TestStalledAnnouncementDoesNotStallTheMixer(t *testing.T) {
	clip := &stalledReader{released: make(chan struct{})}
	defer close(clip.released)

	m := NewMixer(io.Discard, false)
	m.Announce(clip, 1.0)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 8; i++ {
			// The source has to keep reaching the sink while the clip hangs. It is
			// ducked, because the ramp starts as soon as the announcement is asked
			// for, but it must not stop.
			if got := sampleAt(m.step(pcm(blockFrames, 10000)), 0); got == 0 {
				t.Errorf("block %d: source stopped while the clip stalled", i)
				return
			}
		}
		m.SetVolume(50) // would deadlock behind the mixer's lock
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the mixer stalled on an announcement that never produced a byte")
	}
}

// resampledTone runs a sine through the converter in blocks and returns one channel of
// the output, without the filter's start-up.
func resampledTone(t *testing.T, hz, amplitude float64, outRate int64, wide bool, blockFrames int) []float64 {
	t.Helper()
	const frames = SampleRate
	src := make([]byte, frames*frameBytes)
	for i := range frames {
		v := int16(math.Round(amplitude * math.Sin(2*math.Pi*hz*float64(i)/SampleRate)))
		binary.LittleEndian.PutUint16(src[i*frameBytes:], uint16(v))
		binary.LittleEndian.PutUint16(src[i*frameBytes+bytesPerSamp:], uint16(v))
	}
	r := rateConverter{outRate: outRate}
	dst := make([]byte, 2*blockFrames*frameBytes+2*frameBytes)
	var got []float64
	for off := 0; off < len(src); off += blockFrames * frameBytes {
		out := r.resample(dst, src[off:min(off+blockFrames*frameBytes, len(src))], wide, 1<<32)
		if wide {
			for i := 0; i+8 <= len(out); i += 8 {
				got = append(got, float64(int32(binary.LittleEndian.Uint32(out[i:])))/(1<<16))
			}
		} else {
			for i := 0; i+4 <= len(out); i += 4 {
				got = append(got, float64(int16(binary.LittleEndian.Uint16(out[i:]))))
			}
		}
	}
	return got[100:]
}

// toneAndResidual fits a sine at hz and returns its amplitude and what the fit leaves.
func toneAndResidual(x []float64, hz, rate float64) (float64, float64) {
	// Whole cycles only: anywhere else the two projections are not orthogonal.
	x = x[:int(math.Floor(float64(len(x))*hz/rate)*rate/hz)]
	var s, c float64
	w := 2 * math.Pi * hz / rate
	for i, v := range x {
		s += v * math.Sin(w*float64(i))
		c += v * math.Cos(w*float64(i))
	}
	s, c = 2*s/float64(len(x)), 2*c/float64(len(x))
	var rest float64
	for i, v := range x {
		d := v - s*math.Sin(w*float64(i)) - c*math.Cos(w*float64(i))
		rest += d * d
	}
	return math.Hypot(s, c), math.Sqrt(2 * rest / float64(len(x)))
}

// The reason this filter exists: linear interpolation lost 2.9 dB at 15 kHz, and its
// images reached -22 dB at 10 kHz.
func TestResampleKeepsTrebleAndRejectsImages(t *testing.T) {
	const amplitude = 16000.0
	for _, wide := range []bool{false, true} {
		for _, tone := range []struct {
			hz       float64
			maxDropD float64
		}{{100, 0.05}, {1000, 0.05}, {5000, 0.05}, {10000, 0.05}, {15000, 0.05}, {18000, 0.25}} {
			got := resampledTone(t, tone.hz, amplitude, 46875, wide, 960)
			level, rest := toneAndResidual(got, tone.hz, 46875)
			if d := 20 * math.Log10(level/amplitude); math.Abs(d) > tone.maxDropD {
				t.Errorf("wide %v %v Hz: level %.2f dB", wide, tone.hz, d)
			}
			if d := 20 * math.Log10(rest/amplitude); d > -60 {
				t.Errorf("wide %v %v Hz: residual %.1f dB", wide, tone.hz, d)
			}
		}
	}
}

// Every row must fit fir's int32 sum with any S16 input, and pass DC unchanged.
func TestResampleFilterRowsFitAndHaveUnityGain(t *testing.T) {
	for _, rate := range []int64{46875, 47000, 44100, 32000} {
		for p, row := range newResampleFilter(rate).coefs {
			sum, mag := 0, 0
			for _, c := range row {
				sum += int(c)
				mag += max(int(c), -int(c))
			}
			if sum != 1<<14 {
				t.Fatalf("rate %d phase %d: sum %d", rate, p, sum)
			}
			if mag*32768 > math.MaxInt32 {
				t.Fatalf("rate %d phase %d: magnitude %d can overflow", rate, p, mag)
			}
		}
	}
}

// A block size is an accident of timing and must never reach the output.
func TestResampleOutputIgnoresBlockSize(t *testing.T) {
	for _, wide := range []bool{false, true} {
		want := resampledTone(t, 3000, 12000, 46875, wide, 960)
		for _, size := range []int{1, 7, 23, 24, 100, 4096} {
			got := resampledTone(t, 3000, 12000, 46875, wide, size)
			n := min(len(want), len(got))
			if n < len(want)-1 {
				t.Fatalf("block %d: %d frames, want %d", size, len(got), len(want))
			}
			if !slices.Equal(got[:n], want[:n]) {
				t.Fatalf("wide %v block %d: output differs", wide, size)
			}
		}
	}
}

// Filter overshoot on a full-scale square wave has to saturate, never wrap. The same wave
// at half scale cannot clip, so doubled and clamped it is the reference.
func TestResampleSaturatesInsteadOfWrapping(t *testing.T) {
	square := func(amplitude int16) []byte {
		src := make([]byte, 4*blockBytes)
		for i := 0; i+bytesPerSamp <= len(src); i += bytesPerSamp {
			v := amplitude
			if (i/frameBytes/40)%2 == 1 {
				v = -amplitude - 1
			}
			binary.LittleEndian.PutUint16(src[i:], uint16(v))
		}
		return src
	}
	for _, wide := range []bool{false, true} {
		read := func(amplitude int16) []int64 {
			r := rateConverter{outRate: 46875}
			src := square(amplitude)
			out := r.resample(make([]byte, 2*len(src)+8), src, wide, 1<<32)
			var got []int64
			if wide {
				for i := 0; i+4 <= len(out); i += 4 {
					got = append(got, int64(int32(binary.LittleEndian.Uint32(out[i:])))>>16)
				}
			} else {
				for i := 0; i+2 <= len(out); i += 2 {
					got = append(got, int64(int16(binary.LittleEndian.Uint16(out[i:]))))
				}
			}
			return got
		}
		full, half := read(math.MaxInt16), read(math.MaxInt16/2)
		clipped := false
		for i := range full {
			want := min(max(2*half[i], math.MinInt16), math.MaxInt16)
			clipped = clipped || want == math.MaxInt16
			if d := full[i] - want; d < -4 || d > 4 {
				t.Fatalf("wide %v sample %d: got %d want %d", wide, i, full[i], want)
			}
		}
		if !clipped {
			t.Fatalf("wide %v: the wave never reached full scale, so this proved nothing", wide)
		}
	}
}

// The S32 path exists to keep resolution at low gain, so it must not round to 16 bits.
func TestResampleWideKeepsResolutionAtLowGain(t *testing.T) {
	src := pcm(blockFrames, 12345)
	r := rateConverter{outRate: 46875}
	out := r.resample(make([]byte, 2*len(src)+8), src, true, 193000) // about -87 dB
	v := int32(binary.LittleEndian.Uint32(out[len(out)-8:]))
	want := int64(12345) * 193000 >> 16
	if d := int64(v) - want; d < -2 || d > 2 {
		t.Fatalf("got %d want %d", v, want)
	}
}

// A silent block skips the interpolation; it must leave exactly what interpolating zeros
// leaves: the same bytes, and the same position for the next block.
func TestSkipSilenceMatchesInterpolation(t *testing.T) {
	loud := pcm(blockFrames, 8000)
	for _, outRate := range []int64{46875, 44100, 32000} {
		for _, wide := range []bool{false, true} {
			fast, slow := rateConverter{outRate: outRate}, rateConverter{outRate: outRate}
			dstF, dstS := make([]byte, 8*blockBytes), make([]byte, 8*blockBytes)
			for i, size := range []int{blockFrames, 7, blockFrames, 1, 3 * blockFrames, 0, blockFrames} {
				block := make([]byte, size*frameBytes)
				if i == 2 {
					block = loud
				}
				a := fast.resample(dstF, block, wide, 1<<32)
				b := slow.interpolate(dstS, block, wide, 1<<32)
				if !bytes.Equal(a, b) || fast.pos != slow.pos || fast.frac != slow.frac || fast.base != slow.base || fast.histL != slow.histL || fast.histR != slow.histR {
					t.Fatalf("rate %d wide %v block %d: %d bytes, interpolation %d bytes", outRate, wide, i, len(a), len(b))
				}
			}
		}
	}
}

// Once a clip has begun, every block it has ready must play. The mixer used to select
// between the clip and an already-closed channel, and Go picked the silent branch about
// half the time: chimes came out choppy and stretched.
func TestReadyAnnouncementBlocksPlayWithoutGaps(t *testing.T) {
	for run := 0; run < 10; run++ {
		m := NewMixer(io.Discard, false)
		m.Announce(bytes.NewReader(pcm(blockFrames*4, 8000)), 1.0)
		m.step(pcm(blockFrames, 0)) // first block: waits for the clip
		queued := func() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.ann) }
		for deadline := time.Now().Add(5 * time.Second); queued() < 3; time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("run %d: the reader queued %d of the remaining three blocks", run, queued())
			}
		}
		for i := 1; i < 4; i++ {
			if got := sampleAt(m.step(pcm(blockFrames, 0)), 0); got == 0 {
				t.Fatalf("run %d block %d: a ready clip block played as silence", run, i)
			}
		}
	}
}

type tapSink struct {
	lockedBuffer
	tap lockedBuffer
}

func (s *tapSink) Tap(p []byte) { _, _ = s.tap.Write(p) }

// The multiroom tap gets the mix before this speaker's bass; only the DAC gets the bass,
// so a follower does not stack its own on top of the leader's.
func TestTapIsBeforeBass(t *testing.T) {
	var sink tapSink
	m := NewMixer(&sink, true)
	m.SetBass(-9)
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = m.Run(ctx, pr) }()
	go func() { _, _ = pw.Write(pcm(blockFrames*sourceBufferBlocks*2, 6000)) }()
	for deadline := time.Now().Add(5 * time.Second); sink.tap.Len() < 20*blockBytes; {
		if time.Now().After(deadline) {
			t.Fatal("tap got too little audio")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	tap, dac := sink.tap.Bytes(), sink.Bytes()
	if !bytes.Contains(tap, pcm(blockFrames, 6000)) {
		t.Fatal("tap does not carry the unshelved tone")
	}
	if bytes.Contains(dac, pcm(blockFrames, 6000)) {
		t.Fatal("DAC got a block without the bass applied")
	}
}

// The tap carries the source without this speaker's announcement or its duck, at the
// nominal rate and through the rate converter alike: every room would ring the leader's
// doorbell otherwise.
func TestTapIsBeforeAnnouncements(t *testing.T) {
	maxSample := func(b []byte) int16 {
		var hi int16
		for i := 0; i+1 < len(b); i += 2 {
			hi = max(hi, int16(binary.LittleEndian.Uint16(b[i:])))
		}
		return hi
	}
	for _, rate := range []int{SampleRate, 47000} {
		var sink tapSink
		m := NewMixer(&sink, true)
		m.SetOutputRate(rate)
		pr, pw := io.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		go func() { _ = m.Run(ctx, pr) }()
		go func() {
			for ctx.Err() == nil {
				if _, err := pw.Write(pcm(blockFrames*10, 6000)); err != nil {
					return
				}
			}
		}()
		wait := func(what string, done func() bool) {
			for deadline := time.Now().Add(10 * time.Second); !done(); {
				if time.Now().After(deadline) {
					cancel()
					t.Fatalf("rate %d: %s", rate, what)
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		wait("tap got no audio", func() bool { return sink.tap.Len() >= 5*blockBytes })
		m.Announce(bytes.NewReader(pcm(blockFrames*20, 20000)), 1)
		wait("announcement never reached the DAC", func() bool { return maxSample(sink.Bytes()) > 6000 })
		wait("announcement never ended", func() bool { return !m.Announcing() })
		cancel()
		_ = pw.Close()
		// The stream starts as a step from silence; a band-limited step rings up to the Gibbs
		// overshoot of about 9% (6509 seen at 47000 Hz). A leaked announcement is 20000.
		if hi := maxSample(sink.tap.Bytes()); hi > 7000 {
			t.Errorf("rate %d: tap peaked at %d; the announcement leaked into it", rate, hi)
		}
	}
}

// A finite source shorter than the prebuffer still plays once it stops growing.
func TestRunPlaysSourceShorterThanPrebuffer(t *testing.T) {
	m := NewMixer(io.Discard, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := pcm(blockFrames*10, 6000) // 10 blocks, far below prebufferBlocks
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx, bytes.NewReader(src)) }()
	deadline := time.Now().Add(3 * time.Second)
	for m.Health().SourceBytes < uint64(len(src)) {
		if time.Now().After(deadline) {
			t.Fatalf("short source never left the prebuffer: %+v", m.Health())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

// Silence through a settled bass shelf stays silence without running the filter, and a
// tail still ringing is not cut off by that shortcut.
func TestBassOnSilenceSettles(t *testing.T) {
	m := NewMixer(io.Discard, false)
	m.SetBass(-9)
	loud := make([]byte, BlockBytes)
	for i := 0; i < len(loud); i += 2 {
		binary.LittleEndian.PutUint16(loud[i:], uint16(int16(20000)))
	}
	m.applyBass(loud)
	tail := make([]byte, BlockBytes)
	m.applyBass(tail)
	if AllZero(tail) {
		t.Fatal("the block after a loud one must carry the filter's tail")
	}
	for i := 0; i < 200 && !settled(m.bassFilter[:]); i++ {
		clear(tail)
		m.applyBass(tail)
	}
	if !settled(m.bassFilter[:]) {
		t.Fatal("the shelf never settled on silence")
	}
	clear(tail)
	m.applyBass(tail)
	if !AllZero(tail) {
		t.Fatal("silence through a settled shelf must stay silence")
	}
}

func TestConvertKnownMatchesConvert(t *testing.T) {
	a, b := &rateConverter{outRate: 46875}, &rateConverter{outRate: 46875}
	blocks := [][]byte{make([]byte, BlockBytes), make([]byte, BlockBytes), make([]byte, BlockBytes)}
	binary.LittleEndian.PutUint16(blocks[1][BlockBytes-4:], 1234) // a nonzero tail, then silence
	for i, blk := range blocks {
		want := append([]byte(nil), a.convert(make([]byte, 2*BlockBytes), blk)...)
		got, silent := b.convertKnown(make([]byte, 2*BlockBytes), blk, AllZero(blk))
		if silent && !AllZero(got) {
			t.Fatalf("block %d: reported silent but is not", i)
		}
		if string(got) != string(want) {
			t.Fatalf("block %d: convertKnown differs from convert", i)
		}
	}
}

func TestSetPrebuffer(t *testing.T) {
	m := NewMixer(io.Discard, true)
	if got := m.refillTarget(); got != prebufferBlocks {
		t.Fatalf("default target %d, want %d", got, prebufferBlocks)
	}
	m.SetPrebuffer(6 * time.Second)
	if got := m.refillTarget(); got != 300 {
		t.Fatalf("6 s target %d blocks, want 300", got)
	}
	m.SetLive(FollowerBuffer)
	if got := m.refillTarget(); got != flushBufferBlocks {
		t.Fatalf("live target %d, want %d", got, flushBufferBlocks)
	}
}

// A long prebuffer widens the channel; the feeder's buffers must cover all of it, or
// blocks still queued are rewritten before they play.
func TestLargePrebufferRetainsPCM(t *testing.T) {
	m := NewMixer(nil, true)
	m.SetPrebuffer(6 * time.Second)
	ch := make(chan pcmBlock, m.refillTarget()+sourceBufferBlocks-prebufferBlocks)
	data := make([]byte, 200*blockBytes)
	for i := range 200 {
		binary.LittleEndian.PutUint32(data[i*blockBytes:], uint32(i+1))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go m.feed(ctx, bytes.NewReader(data), ch)
	deadline := time.Now().Add(5 * time.Second)
	for len(ch) < 200 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(ch) != 200 {
		t.Fatalf("queued %d blocks, want 200", len(ch))
	}
	for want := uint32(1); want <= 200; want++ {
		if got := binary.LittleEndian.Uint32((<-ch).pcm); got != want {
			t.Fatalf("queued block %d holds block %d", want, got)
		}
	}
}

// A skip: the source writes the new track right after its flush, and the new track then
// stalls once. Its first blocks must play (the flush used to drop them with the old
// stream), and the stall must refill briefly, not the full prebuffer (6 s on the ST20).
func TestSkipKeepsTheNewTrackAndRefillsShort(t *testing.T) {
	m := NewMixer(io.Discard, true)
	m.SetPrebuffer(6 * time.Second)
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = m.Run(ctx, pr) }()

	played := func() int { return int(m.Health().SourceBytes / blockBytes) }
	waitPlayed := func(n int, within time.Duration, why string) {
		deadline := time.Now().Add(within)
		for played() < n {
			if time.Now().After(deadline) {
				t.Fatalf("%s: %d blocks played, want %d", why, played(), n)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	go func() { _, _ = pw.Write(pcm(blockFrames*20, 1000)) }() // the old track
	waitPlayed(20, 3*time.Second, "old track")
	m.Flush()
	go func() { _, _ = pw.Write(pcm(blockFrames*20, 2000)) }() // the new track, written at once
	// The feeder may drop the one block it was reading when the flush landed.
	waitPlayed(39, 2*time.Second, "new track's first blocks")
	time.Sleep(200 * time.Millisecond) // the stall: an underrun

	base := played()
	go func() { // the track resumes in real time
		for i := 0; i < 60 && ctx.Err() == nil; i++ {
			if _, err := pw.Write(pcm(blockFrames, 3000)); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	// 60 blocks are 1.2 s, short of the 6 s a full refill waits for: the bound only has
	// to outlast a busy machine, not race the writer.
	waitPlayed(base+1, 5*time.Second, "track waited for the full prebuffer after its first stall")
}

// The source's fixed input gain scales the source.
func TestSourceGainScalesTheSource(t *testing.T) {
	m := NewMixer(io.Discard, false)
	m.SetSourceGain(0.5)
	if got := sampleAt(m.step(pcm(blockFrames, 10000)), 0); got != 5000 {
		t.Fatalf("sample = %d, want 5000", got)
	}
}

// After a skip the new track waits for a short buffer before it starts, so librespot's
// first fetch does not stall it 80 ms in: a blip, a gap, then the track (ST300 2026-11-08).
func TestSkipBuffersBeforeTheNewTrackStarts(t *testing.T) {
	m := NewMixer(io.Discard, true)
	m.SetPrebuffer(time.Second)
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = m.Run(ctx, pr) }()
	played := func() int { return int(m.Health().SourceBytes / blockBytes) }

	go func() { _, _ = pw.Write(pcm(blockFrames*60, 1000)) }() // the old track
	for deadline := time.Now().Add(3 * time.Second); played() < 60; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("old track: %d blocks played", played())
		}
	}
	m.Flush()
	base := played()
	go func() { _, _ = pw.Write(pcm(blockFrames*flushBufferBlocks*2, 2000)) }() // more than 80 ms, less than the restart buffer
	time.Sleep(200 * time.Millisecond)
	if got := played() - base; got > 1 {
		t.Fatalf("new track started after %d blocks, before its restart buffer", got)
	}
}

// TestBassMatchesStock pins the shelf to what a stock ST20 measured at bass -9.
func TestBassMatchesStock(t *testing.T) {
	f := lowShelf(stockBassDBPerStep*-9, stockBassCorner)
	for _, c := range []struct{ hz, db float64 }{{50, -9.98}, {100, -7.47}, {200, -3.82}, {400, -1.24}, {1000, -0.21}} {
		w := 2 * math.Pi * c.hz / SampleRate
		z1, z2 := cmplx.Exp(complex(0, -w)), cmplx.Exp(complex(0, -2*w))
		h := (complex(f.b0, 0) + complex(f.b1, 0)*z1 + complex(f.b2, 0)*z2) / (1 + complex(f.a1, 0)*z1 + complex(f.a2, 0)*z2)
		if got := 20 * math.Log10(cmplx.Abs(h)); math.Abs(got-c.db) > 1 {
			t.Errorf("%g Hz: %.2f dB, stock %.2f dB", c.hz, got, c.db)
		}
	}
}

// What one block costs in the path every speaker uses while it plays.
func BenchmarkResampleWide(b *testing.B) {
	r := rateConverter{outRate: 46875}
	src := pcm(blockFrames, 9000)
	dst := make([]byte, 2*blockBytes+8)
	b.SetBytes(blockBytes)
	for b.Loop() {
		r.resample(dst, src, true, 1<<31)
	}
}

// A chime is meant to be as loud as music at the same volume, which only holds on the
// music's own curve. Volume/ceiling put one at 14 about 49 dB above the music at 14.
func TestVolumeGainIsTheMusicCurve(t *testing.T) {
	m := NewMixer(io.Discard, false)
	for _, vol := range []int{1, 14, 41, 70, 100} {
		m.SetVolume(vol)
		if got := VolumeGain(vol); got != m.master {
			t.Errorf("volume %d: gain %v, music %v", vol, got, m.master)
		}
	}
}

// The WLA's AUX trim is 1.22: a loud input must clip at full scale, not wrap to the other
// sign, which is heard as a crack.
func TestScaleIntoClipsAboveUnity(t *testing.T) {
	for _, tc := range []struct{ in, want int16 }{{32767, 32767}, {-32768, -32768}, {30000, 32767}, {1000, 1220}} {
		var src, dst [2]byte
		binary.LittleEndian.PutUint16(src[:], uint16(tc.in))
		scaleInto(dst[:], src[:], 1.22)
		if got := int16(binary.LittleEndian.Uint16(dst[:])); got != tc.want {
			t.Errorf("scaleInto(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// The producer has stopped. A flush must acknowledge an idle reader BEFORE the
// first new block arrives; otherwise that entire first block is discarded later.
func TestFlushWaitPreservesFirstBlockAfterIdleBoundary(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	m := NewMixer(io.Discard, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ch := make(chan pcmBlock, 4)
	go m.feed(ctx, r, ch)
	for m.bus.Load() == nil {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	for i := range 5 { // consecutive skips, including a reader with nothing to drain
		if err := m.FlushWait(ctx); err != nil {
			t.Fatal(err)
		}
		want := pcm(blockFrames, int16(1000+i))
		if _, err := w.Write(want); err != nil {
			t.Fatal(err)
		}
		select {
		case b := <-ch:
			if b.gen != m.flushGen.Load() || !bytes.Equal(b.pcm, want) {
				t.Fatalf("first block lost or stale: gen %d, sample %d", b.gen, sampleAt(b.pcm, 0))
			}
		case <-ctx.Done():
			t.Fatal("first new block was discarded", ctx.Err())
		}
	}
}

func TestFlushWaitRequiresRunningReader(t *testing.T) {
	m := NewMixer(io.Discard, true)
	if err := m.FlushWait(context.Background()); err == nil {
		t.Fatal("acknowledged without a bus")
	}
}

func TestFlushWaitDropsOldPartialBlockAndFIFO(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux FIFO ioctl")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	m := NewMixer(io.Discard, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ch := make(chan pcmBlock, 16)
	go m.feed(ctx, r, ch)
	// Includes an incomplete ReadFull at the boundary, not only whole blocks.
	if _, err := w.Write(pcm(blockFrames*2+17, 1111)); err != nil {
		t.Fatal(err)
	}
	for len(ch) < 2 {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if err := m.FlushWait(ctx); err != nil {
		t.Fatal(err)
	}
	gen := m.flushGen.Load()
	for len(ch) > 0 {
		if b := <-ch; b.gen >= gen {
			t.Fatal("old block marked as new")
		}
	}
	want := pcm(blockFrames, 2222)
	if _, err := w.Write(want); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-ch:
		if b.gen != gen || !bytes.Equal(b.pcm, want) {
			t.Fatal("old tail survived or new opening lost")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
