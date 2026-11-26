// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package readahead_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/readahead"
)

// gen is the station: byte i of the endless stream, the same for every listener.
func gen(from, to int) []byte {
	out := make([]byte, to-from)
	for i := range out {
		x := uint32(from+i) * 2654435761
		out[i] = byte(x>>13 ^ x>>21)
	}

	return out
}

func testCfg() *readahead.Config {
	return &readahead.Config{
		Client: &http.Client{}, Trace: nil, Log: nil,
		Limit: 64 << 10, IdleTimeout: 200 * time.Millisecond, MaxOutage: 5 * time.Second,
		BackoffMin: 10 * time.Millisecond, BackoffMax: 50 * time.Millisecond,
		ScanWait: 100 * time.Millisecond, ScanMax: 0,
	}
}

func send(w http.ResponseWriter, data []byte) {
	for len(data) > 0 {
		n := min(len(data), 4096)
		_, _ = w.Write(data[:n])
		data = data[n:]
	}

	w.(http.Flusher).Flush()
}

// The first connection delivers 100000 bytes and then stalls; the station moves on to
// 150000 and the connection is dropped (idle timeout). The reconnect's burst holds the
// last 80000 bytes, 30000 of them already received. The output must be the station's
// bytes 0..200000 once each, in order.
func TestStallDropReconnectIsContinuous(t *testing.T) {
	t.Parallel()

	var conns atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")

		switch conns.Add(1) {
		case 1:
			send(w, gen(0, 100000))
			<-r.Context().Done() // stalls until the client gives up
		case 2:
			send(w, gen(70000, 150000)) // burst on connect
			time.Sleep(50 * time.Millisecond)
			send(w, gen(150000, 200000)) // live
			<-r.Context().Done()
		default:
			<-r.Context().Done()
		}
	}))
	defer srv.Close()

	// The decoder is behind, as it is while it plays the server's opening burst: the
	// stall hits a queue that still holds audio, so the stream rides it out.
	cfg := testCfg()
	cfg.Limit = 1 << 20

	stream, err := readahead.Open(t.Context(), t.Context(), cfg, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var out syncBuf

	go func() {
		time.Sleep(2 * cfg.IdleTimeout)

		_, _ = stream.WriteTo(&out)
	}()

	want := gen(0, 200000)

	waitFor(t, 10*time.Second, func() bool { return out.Len() >= len(want) })
	time.Sleep(200 * time.Millisecond) // anything extra (a repeat) would show up now

	if got := out.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("output is %d bytes, want the station's %d bytes once each, in order", len(got), len(want))
	}

	if c := conns.Load(); c < 2 {
		t.Fatalf("connections = %d, want a reconnect", c)
	}
}

// A server that does not repeat anything on reconnect (no burst) loses nothing and
// repeats nothing either: the scan ends on its own and the live bytes are queued.
func TestReconnectWithoutBurst(t *testing.T) {
	t.Parallel()

	var conns atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/aac")

		if conns.Add(1) == 1 {
			send(w, gen(0, 50000))

			return // clean close
		}

		send(w, gen(60000, 90000)) // 10000 bytes were missed, no overlap
		<-r.Context().Done()
	}))
	defer srv.Close()

	stream, err := readahead.Open(t.Context(), t.Context(), testCfg(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var out syncBuf
	go func() { _, _ = stream.WriteTo(&out) }()

	want := append(gen(0, 50000), gen(60000, 90000)...)

	waitFor(t, 10*time.Second, func() bool { return out.Len() >= len(want) })

	if got := out.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("output is %d bytes, want %d", len(got), len(want))
	}
}

// A live station with no repeat that never goes quiet: the scan must give up after
// ScanMax, not hold the audio until Limit bytes have come in at the stream's own rate.
func TestScanEndsOnAStationThatNeverGoesQuiet(t *testing.T) {
	t.Parallel()

	var conns atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")

		if conns.Add(1) == 1 {
			send(w, gen(0, 50000))

			return
		}

		for i := 60000; r.Context().Err() == nil; i += 1000 { // 1000 bytes every 20 ms
			send(w, gen(i, i+1000))
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()

	cfg := testCfg()
	cfg.ScanWait, cfg.ScanMax = time.Second, 300*time.Millisecond

	stream, err := readahead.Open(t.Context(), t.Context(), cfg, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var out syncBuf
	go func() { _, _ = stream.WriteTo(&out) }()

	waitFor(t, 5*time.Second, func() bool { return conns.Load() >= 2 })

	start := time.Now()

	waitFor(t, 5*time.Second, func() bool { return out.Len() > 50000 })
	// Without ScanMax the 64 KiB Limit takes about 1.3 s at this rate.
	if d := time.Since(start); d > 800*time.Millisecond {
		t.Fatalf("live bytes after the reconnect took %v, want them within ScanMax", d)
	}
}

// With nobody reading, the queue stops at Limit and the server is held back by TCP.
func TestBufferIsBounded(t *testing.T) {
	t.Parallel()

	var sent atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")

		chunk := gen(0, 16<<10)
		for r.Context().Err() == nil && sent.Load() < 256<<20 {
			_, err := w.Write(chunk)
			if err != nil {
				return
			}

			sent.Add(int64(len(chunk)))
		}
	}))
	defer srv.Close()

	cfg := testCfg()
	cfg.IdleTimeout = 5 * time.Second

	stream, err := readahead.Open(t.Context(), t.Context(), cfg, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	waitFor(t, 5*time.Second, func() bool { return stream.Buffered() >= cfg.Limit-(16<<10) })
	time.Sleep(300 * time.Millisecond)

	if n := stream.Buffered(); n > cfg.Limit {
		t.Fatalf("buffered %d bytes, limit %d", n, cfg.Limit)
	}

	if got := sent.Load(); got > 64<<20 {
		t.Fatalf("server sent %d bytes into an unread buffer: no back-pressure", got)
	}
}

func TestNotDirectStreamsAreDeclined(t *testing.T) {
	t.Parallel()

	for name, handler := range map[string]http.HandlerFunc{
		"hls": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte("#EXTM3U\n"))
		},
		"m3u": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "audio/mpegurl")
			send(w, []byte("x"))
		},
		"pls": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "audio/x-scpls")
			send(w, []byte("x"))
		},
		"no type": func(w http.ResponseWriter, _ *http.Request) { send(w, []byte("x")) },
		"missing": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"a file": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Header().Set("Content-Length", "1")
			_, _ = w.Write([]byte("x"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(handler)
			defer srv.Close()

			stream, err := readahead.Open(t.Context(), t.Context(), testCfg(), srv.URL)
			if err == nil {
				stream.Close()
				t.Fatal("opened a stream that ffmpeg must open itself")
			}

			if name != "missing" && !errors.Is(err, readahead.ErrNotDirect) {
				t.Fatalf("err = %v, want ErrNotDirect", err)
			}
		})
	}
}

// A stream that stays dead for MaxOutage ends with EOF after the buffered bytes, so the
// decoder exits and the player's supervisor starts over.
func TestGivesUpAfterMaxOutage(t *testing.T) {
	t.Parallel()

	var conns atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if conns.Add(1) > 1 {
			http.Error(w, "gone", http.StatusServiceUnavailable)

			return
		}

		w.Header().Set("Content-Type", "audio/mpeg")
		send(w, gen(0, 5000))
	}))
	defer srv.Close()

	cfg := testCfg()
	cfg.MaxOutage = 300 * time.Millisecond

	stream, err := readahead.Open(t.Context(), t.Context(), cfg, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var out syncBuf

	done := make(chan error, 1)

	go func() {
		_, err := stream.WriteTo(&out)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("WriteTo = %v, want io.ErrUnexpectedEOF after the outage", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WriteTo did not end after the outage limit")
	}

	if !bytes.Equal(out.Bytes(), gen(0, 5000)) {
		t.Fatalf("got %d bytes, want the 5000 received before the outage", out.Len())
	}
}

// A stall that finds nothing queued ends the stream at the idle timeout, not after
// MaxOutage: the listener already hears silence, and the player should say so.
func TestStallWithEmptyQueueEndsAtOnce(t *testing.T) {
	t.Parallel()

	var conns atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conns.Add(1)
		w.Header().Set("Content-Type", "audio/mpeg")
		send(w, gen(0, 5000))
		<-r.Context().Done()
	}))
	defer srv.Close()

	cfg := testCfg() // MaxOutage 5 s, IdleTimeout 200 ms

	stream, err := readahead.Open(t.Context(), t.Context(), cfg, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var out syncBuf

	done := make(chan struct{})

	go func() { _, _ = stream.WriteTo(&out); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a stall with nothing buffered waited for MaxOutage")
	}

	if c := conns.Load(); c != 1 {
		t.Fatalf("connections = %d, want no reconnect", c)
	}

	if !bytes.Equal(out.Bytes(), gen(0, 5000)) {
		t.Fatalf("got %d bytes, want the 5000 received", out.Len())
	}
}

// Close, and the context, stop both the fetching and a blocked WriteTo at once.
func TestCloseStopsEverything(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/ogg")
		send(w, gen(0, 1000))
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	stream, err := readahead.Open(ctx, t.Context(), testCfg(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	var out syncBuf

	done := make(chan struct{})

	go func() { _, _ = stream.WriteTo(&out); close(done) }()

	waitFor(t, 5*time.Second, func() bool { return out.Len() == 1000 })
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("WriteTo still blocked after the context ended")
	}
}

func TestDialCancelAbortsFirstConnect(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	dial, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)

	start := time.Now()

	_, err := readahead.Open(t.Context(), dial, testCfg(), srv.URL)
	if err == nil {
		t.Fatal("Open succeeded after its dial context ended")
	}

	if time.Since(start) > 3*time.Second {
		t.Fatal("Open ignored its dial context")
	}
}

type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	written, _ := s.buf.Write(p) // a bytes.Buffer write always returns a nil error

	return written, nil
}

func (s *syncBuf) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.Len()
}

func (s *syncBuf) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	return bytes.Clone(s.buf.Bytes())
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()

	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}

	t.Fatal("condition not reached in time")
}
