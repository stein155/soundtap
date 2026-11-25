// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package readahead reads a live radio stream ahead of the decoder, in a bounded
// in-memory buffer, and reconnects on its own when the network stalls (ADR 0041).
//
// ffmpeg used to open the station itself. It is paced by the mixer, so only about
// 3 s of decoded audio sat between the Wi-Fi and the DAC, and a stall of 15 s made
// ffmpeg give up, reconnect, and receive the server's burst-on-connect again: silence
// first, then pieces repeated. Here the compressed stream is read into a queue of at
// most Limit bytes (about 30 s of audio) and ffmpeg is fed from it through stdin, so a
// stall shorter than the queue is inaudible and the burst fills the queue at once.
package readahead

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/safehttp"
)

// ErrNotDirect means the URL is not a plain, endless audio stream (HLS, a playlist, a
// file with a length, ICY's non-HTTP greeting...). The caller hands the URL to ffmpeg.
var ErrNotDirect = errors.New("readahead: not a plain audio stream")

// errIdle ends a session that received nothing for IdleTimeout.
var errIdle = errors.New("no data")

// directTypes are the content types read here. Everything else, HLS and playlist
// types above all (audio/mpegurl, application/vnd.apple.mpegurl, audio/x-scpls), is
// ffmpeg's to open: it follows the playlist, which this package does not.
var directTypes = map[string]bool{
	"audio/mpeg": true, "audio/mp3": true, "audio/aac": true, "audio/aacp": true,
	"audio/ogg": true, "application/ogg": true,
}

// Config holds the bounds. Zero values take the defaults below.
type Config struct {
	Client      *http.Client  // nil: the shared client, which keeps TLS sessions between stations
	Trace       *Timing       // when set, receives where the first connect spent its time
	Log         *slog.Logger  // the player's logger (comp=player)
	Limit       int           // queue bytes; default 1 MiB, ~30 s at 256 kbps
	IdleTimeout time.Duration // no byte for this long drops the connection; default 15 s
	MaxOutage   time.Duration // no byte for this long ends the stream (EOF); default 60 s
	BackoffMin  time.Duration // default 250 ms
	BackoffMax  time.Duration // default 5 s
	ScanWait    time.Duration // quiet time that ends a burst scan; default 500 ms
	ScanMax     time.Duration // longest a burst scan holds bytes back; default 2 s
}

const (
	chunkSize = 16 << 10
	// tailLen is the window that identifies where a reconnect's burst overlaps what was
	// already received. Compressed audio does not repeat 4 KiB by accident, except in
	// digital silence, where a wrong match is harmless.
	tailLen = 4 << 10
)

func (c *Config) defaults() {
	if c.Client == nil {
		c.Client = sharedClient
	}

	if c.Log == nil {
		c.Log = slog.Default().With("comp", "player")
	}

	for _, d := range []struct {
		v *time.Duration
		d time.Duration
	}{
		{&c.IdleTimeout, 15 * time.Second},
		{&c.MaxOutage, 60 * time.Second},
		{&c.BackoffMin, 250 * time.Millisecond},
		{&c.BackoffMax, 5 * time.Second},
		{&c.ScanWait, 500 * time.Millisecond},
		{&c.ScanMax, 2 * time.Second},
	} {
		if *d.v <= 0 {
			*d.v = d.d
		}
	}

	if c.Limit <= 0 {
		c.Limit = 1 << 20
	}
}

// NewClient is the production client: public addresses only (safehttp), a bound on the
// response headers, and no overall timeout, because the body is a stream that never ends.
func NewClient() *http.Client {
	return newClient(nil)
}

func newClient(tlsCfg *tls.Config) *http.Client {
	tr := safehttp.SafeTransport()
	tr.ResponseHeaderTimeout = 10 * time.Second
	tr.TLSClientConfig = tlsCfg

	return &http.Client{Transport: tr}
}

// sharedClient resumes TLS sessions across connects: the ARMv7 handshake is the costly part
// of opening an https station, and a station's connection never goes back to the pool
// because its body does not end, so only the session cache carries over.
var sharedClient = newClient(&tls.Config{ClientSessionCache: tls.NewLRUClientSessionCache(32)})

// Warm opens and drops one https connection to url, so its TLS session is cached for the
// next Open. Anything else is left alone.
func Warm(ctx context.Context, url string) {
	if !strings.HasPrefix(url, "https://") {
		return
	}

	s := &Stream{cfg: Config{Client: sharedClient}, url: url}
	if resp, err := s.connect(ctx); err == nil {
		_ = resp.Body.Close()
	}
}

// Timing is where connect time went, summed over redirect hops, in milliseconds.
type Timing struct{ DNS, TCP, TLS, TTFB int64 }

func (t *Timing) trace() *httptrace.ClientTrace {
	var start, dnsAt, tcpAt, tlsAt time.Time

	span := func(from time.Time, to *int64) {
		if !from.IsZero() {
			*to += time.Since(from).Milliseconds()
		}
	}

	return &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { dnsAt = time.Now() },
		DNSDone:              func(httptrace.DNSDoneInfo) { span(dnsAt, &t.DNS) },
		ConnectStart:         func(_, _ string) { tcpAt = time.Now() },
		ConnectDone:          func(_, _ string, _ error) { span(tcpAt, &t.TCP) },
		TLSHandshakeStart:    func() { tlsAt = time.Now() },
		TLSHandshakeDone:     func(_ tls.ConnectionState, _ error) { span(tlsAt, &t.TLS) },
		WroteRequest:         func(httptrace.WroteRequestInfo) { start = time.Now() },
		GotFirstResponseByte: func() { span(start, &t.TTFB) },
	}
}

// Stream is one station being read ahead. Close it when the decoder is done.
type Stream struct {
	cfg    Config
	url    string
	ctx    context.Context
	cancel context.CancelFunc
	q      *queue
	tail   []byte // last bytes handed to the queue, only touched by the fetch goroutine
}

// Open connects to url and starts reading. It returns ErrNotDirect (or the connect
// error) without starting anything when the stream is not one to read ahead; the caller
// then gives the URL to ffmpeg unchanged. ctx ends the stream like Close; dial only
// aborts the first connect (the player cancels it when the selection moves on), and is
// ignored once Open has returned.
func Open(ctx, dial context.Context, cfg Config, url string) (*Stream, error) {
	cfg.defaults()

	sctx, cancel := context.WithCancel(ctx)
	s := &Stream{cfg: cfg, url: url, ctx: sctx, cancel: cancel, q: newQueue(cfg.Limit)}
	stop := context.AfterFunc(dial, cancel)

	resp, err := s.connect(sctx)
	if !stop() && err == nil { // dial ended first and took the stream down with it
		_ = resp.Body.Close()
		err = dial.Err()
	}

	if err != nil {
		cancel()

		return nil, err
	}

	safego.Go("readahead.run", func() { s.run(resp) })

	return s, nil
}

// connect issues the GET and accepts only a plain audio stream. No Icy-MetaData header
// is sent, so the body is pure audio; now-playing titles come from their own connection
// (internal/icy).
func (s *Stream) connect(ctx context.Context) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, http.NoBody)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "SoundTap/1.0")

	if s.cfg.Trace != nil {
		req = req.WithContext(httptrace.WithClientTrace(ctx, s.cfg.Trace.trace()))
	}

	resp, err := safehttp.Do(ctx, s.cfg.Client, req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK || resp.ContentLength >= 0 || !directType(resp.Header.Get("Content-Type")) {
		_ = resp.Body.Close()

		return nil, fmt.Errorf("%w: status %d, type %q, length %d", ErrNotDirect,
			resp.StatusCode, resp.Header.Get("Content-Type"), resp.ContentLength)
	}

	return resp, nil
}

func directType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)

	return err == nil && directTypes[strings.ToLower(mt)]
}

// WriteTo copies the buffered stream to w (ffmpeg's stdin) until the stream ends, is
// closed, or a write fails. It returns nil at the end of the stream.
func (s *Stream) WriteTo(w io.Writer) (int64, error) {
	var total int64

	for {
		b, err := s.q.get()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}

			return total, err
		}

		n, err := w.Write(b)

		total += int64(n)
		if err != nil {
			return total, err
		}
	}
}

// Close stops the fetching, drops the buffered audio and unblocks WriteTo.
func (s *Stream) Close() {
	s.cancel()
	s.q.abort()
}

// Buffered reports the queued bytes (for tests and logs).
func (s *Stream) Buffered() int { return s.q.len() }

// run keeps one connection after the other until the stream is closed or has been silent
// for MaxOutage, then ends the queue so the decoder sees EOF, exits, and the player's
// own supervisor starts over (which also resolves the station URL again).
func (s *Stream) run(resp *http.Response) {
	defer s.cancel()

	backoff := s.cfg.BackoffMin
	lastData := time.Now()

	for {
		if resp != nil {
			n, err := s.session(resp)
			_ = resp.Body.Close()
			resp = nil

			if s.ctx.Err() != nil {
				s.q.finish(nil)

				return
			}

			s.cfg.Log.Warn("radio read-ahead: connection lost", "bytes", n, "buffered", s.q.len(), "err", err)
			// Silent for IdleTimeout with nothing left to play: the listener already
			// hears silence, while the player still says PLAY_STATE. End the stream now
			// so the player shows buffering and starts over, instead of a minute later.
			if errors.Is(err, errIdle) && s.q.len() == 0 {
				s.cfg.Log.Warn("radio read-ahead: stalled with nothing buffered, ending the stream")
				s.q.finish(io.ErrUnexpectedEOF)

				return
			}

			if n > 0 {
				lastData = time.Now()
				backoff = s.cfg.BackoffMin
			}
		}

		for resp == nil {
			if time.Since(lastData) > s.cfg.MaxOutage {
				s.cfg.Log.Warn("radio read-ahead: giving up", "outage", s.cfg.MaxOutage)
				s.q.finish(io.ErrUnexpectedEOF)

				return
			}

			if !sleep(s.ctx, backoff) {
				s.q.finish(nil)

				return
			}

			var err error
			if resp, err = s.connect(s.ctx); err != nil {
				s.cfg.Log.Warn("radio read-ahead: reconnect failed", "err", err, "retry_in", backoff)
				backoff = min(backoff*2, s.cfg.BackoffMax)
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// session reads one connection into the queue and returns the bytes queued from it. A
// reader goroutine takes the socket, so this loop can enforce the idle timeout while the
// queue is empty and also wait on the queue when it is full (which stops reading and lets
// TCP push back on the server).
//
// After a reconnect (s.tail set) the server's burst starts with audio this stream already
// delivered. The first bytes are held back until the tail window turns up in them, and
// everything up to and including it is dropped, so playback continues byte-exact where it
// stopped. The scan ends without a match when the burst goes quiet for ScanWait, after
// ScanMax or after Limit bytes: then the server did not repeat anything (or the stall
// outlasted its burst) and what was read is queued unchanged. ScanMax matters for a station
// that never goes quiet: a burst comes at link speed, so it is in well within 2 s, while
// waiting for Limit at the stream's own rate held the audio back for a minute (WLA,
// 2026-11-15).
func (s *Stream) session(resp *http.Response) (int, error) {
	type frame struct {
		b   []byte
		err error
	}

	cctx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	ch := make(chan frame, 2)

	safego.Go("readahead.read", func() {
		defer close(ch)

		buf := make([]byte, chunkSize)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				select {
				case ch <- frame{b: bytes.Clone(buf[:n])}:
				case <-cctx.Done():
					return
				}
			}

			if err != nil {
				select {
				case ch <- frame{err: err}:
				case <-cctx.Done():
				}

				return
			}
		}
	})
	// cancel() above stops the goroutine at its next send; closing the body (the caller
	// does it right after) unblocks a Read that is waiting on the socket.

	queued := 0
	push := func(b []byte) bool {
		if len(b) == 0 {
			return true
		}

		queued += len(b)

		s.tail = append(s.tail, b...)
		if len(s.tail) > tailLen {
			s.tail = bytes.Clone(s.tail[len(s.tail)-tailLen:])
		}

		return s.q.put(b)
	}

	var scan []byte

	scanning := len(s.tail) >= tailLen/4
	tail := bytes.Clone(s.tail)
	scanEnd := time.Now().Add(s.cfg.ScanMax)

	timer := time.NewTimer(s.cfg.IdleTimeout)
	defer timer.Stop()

	for {
		select {
		case <-cctx.Done():
			return queued, cctx.Err()
		case <-timer.C:
			if scanning { // burst over without a match: queue it as it is
				scanning = false

				if !push(scan) {
					return queued, context.Canceled
				}

				scan = nil

				timer.Reset(s.cfg.IdleTimeout)

				continue
			}

			return queued, fmt.Errorf("%w for %s", errIdle, s.cfg.IdleTimeout)
		case f, ok := <-ch:
			if !ok {
				return queued, io.EOF
			}

			if f.err != nil {
				if scanning {
					push(scan)
				}

				return queued, f.err
			}

			b := f.b

			if scanning {
				from := max(len(scan)-len(tail)+1, 0)
				scan = append(scan, b...)

				i := bytes.Index(scan[from:], tail)
				switch {
				case i >= 0:
					skip := from + i + len(tail)
					s.cfg.Log.Info("radio read-ahead: skipped the repeated burst", "bytes", skip)
					b, scanning = bytes.Clone(scan[skip:]), false
					scan = nil
				case len(scan) >= s.cfg.Limit || time.Now().After(scanEnd):
					b, scanning = scan, false
					scan = nil
				default:
					timer.Reset(s.cfg.ScanWait)

					continue
				}
			}

			if !push(b) {
				return queued, context.Canceled
			}

			timer.Reset(s.cfg.IdleTimeout)
		}
	}
}

// queue is a FIFO of chunks bounded in bytes. put blocks while it is full (an oversized
// chunk is admitted into an empty queue, so nothing deadlocks); get blocks while empty.
type queue struct {
	mu     sync.Mutex
	cv     *sync.Cond
	chunks [][]byte
	size   int
	limit  int
	done   bool  // no more puts: get drains what is left, then returns err (or EOF)
	err    error // why it ended
	dead   bool  // aborted: get returns at once
}

func newQueue(limit int) *queue {
	q := &queue{limit: limit}
	q.cv = sync.NewCond(&q.mu)

	return q
}

func (q *queue) put(b []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for !q.dead && !q.done && q.size > 0 && q.size+len(b) > q.limit {
		q.cv.Wait()
	}

	if q.dead || q.done {
		return false
	}

	q.chunks = append(q.chunks, b)
	q.size += len(b)
	q.cv.Broadcast()

	return true
}

func (q *queue) get() ([]byte, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for !q.dead && !q.done && len(q.chunks) == 0 {
		q.cv.Wait()
	}

	if q.dead {
		return nil, context.Canceled
	}

	if len(q.chunks) == 0 {
		if q.err != nil {
			return nil, q.err
		}

		return nil, io.EOF
	}

	b := q.chunks[0]
	q.chunks[0] = nil
	q.chunks = q.chunks[1:]
	q.size -= len(b)
	q.cv.Broadcast()

	return b, nil
}

// finish ends the queue after what is queued has been read.
func (q *queue) finish(err error) {
	q.mu.Lock()
	q.done, q.err = true, err
	q.mu.Unlock()
	q.cv.Broadcast()
}

// abort drops everything queued and wakes both sides.
func (q *queue) abort() {
	q.mu.Lock()
	q.dead, q.chunks, q.size = true, nil, 0
	q.mu.Unlock()
	q.cv.Broadcast()
}

func (q *queue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.size
}
