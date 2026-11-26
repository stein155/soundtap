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
	// minScanTail is how much of the tail a reconnect needs before its burst is scanned.
	minScanTail = tailLen / 4
	// framesBuffered is how many chunks the socket reader may run ahead of the session.
	framesBuffered = 2
	// backoffFactor multiplies the wait between failed reconnects, up to BackoffMax.
	backoffFactor = 2
	// headerTimeout bounds the wait for a station's response headers.
	headerTimeout = 10 * time.Second
	// sessionCacheSize is how many stations' TLS sessions the shared client keeps.
	sessionCacheSize = 32
)

// The defaults of Config.
const (
	defaultLimit       = 1 << 20
	defaultIdleTimeout = 15 * time.Second
	defaultMaxOutage   = 60 * time.Second
	defaultBackoffMin  = 250 * time.Millisecond
	defaultBackoffMax  = 5 * time.Second
	defaultScanWait    = 500 * time.Millisecond
	defaultScanMax     = 2 * time.Second
)

func (c *Config) defaults() {
	if c.Client == nil {
		c.Client = sharedClient
	}

	if c.Log == nil {
		c.Log = slog.Default().With("comp", "player")
	}

	for _, field := range []struct {
		value    *time.Duration
		fallback time.Duration
	}{
		{&c.IdleTimeout, defaultIdleTimeout},
		{&c.MaxOutage, defaultMaxOutage},
		{&c.BackoffMin, defaultBackoffMin},
		{&c.BackoffMax, defaultBackoffMax},
		{&c.ScanWait, defaultScanWait},
		{&c.ScanMax, defaultScanMax},
	} {
		if *field.value <= 0 {
			*field.value = field.fallback
		}
	}

	if c.Limit <= 0 {
		c.Limit = defaultLimit
	}
}

// NewClient is the production client: public addresses only (safehttp), a bound on the
// response headers, and no overall timeout, because the body is a stream that never ends.
func NewClient() *http.Client {
	return clientWith(nil)
}

func clientWith(tlsCfg *tls.Config) *http.Client {
	transport := safehttp.SafeTransport()
	transport.ResponseHeaderTimeout = headerTimeout
	transport.TLSClientConfig = tlsCfg

	return &http.Client{Transport: transport}
}

// sharedClient resumes TLS sessions across connects: the ARMv7 handshake is the costly part
// of opening an https station, and a station's connection never goes back to the pool
// because its body does not end, so only the session cache carries over.
//
//nolint:gochecknoglobals // process-wide state: one TLS session cache for every station connect
var sharedClient = clientWith(&tls.Config{ClientSessionCache: tls.NewLRUClientSessionCache(sessionCacheSize)})

// Warm opens and drops one https connection to url, so its TLS session is cached for the
// next Open. Anything else is left alone.
func Warm(ctx context.Context, url string) {
	if !strings.HasPrefix(url, "https://") {
		return
	}

	stream := &Stream{
		cfg: Config{
			Client: sharedClient, Trace: nil, Log: nil, Limit: 0, IdleTimeout: 0, MaxOutage: 0,
			BackoffMin: 0, BackoffMax: 0, ScanWait: 0, ScanMax: 0,
		},
		url: url, cancel: nil, q: nil, tail: nil,
	}

	resp, err := stream.connect(ctx)
	if err == nil {
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
	cancel context.CancelFunc
	q      *queue
	tail   []byte // last bytes handed to the queue, only touched by the fetch goroutine
}

// Open connects to url and starts reading. It returns ErrNotDirect (or the connect
// error) without starting anything when the stream is not one to read ahead; the caller
// then gives the URL to ffmpeg unchanged. ctx ends the stream like Close; dial only
// aborts the first connect (the player cancels it when the selection moves on), and is
// ignored once Open has returned. cfg is copied: the caller's is left as it is.
func Open(ctx, dial context.Context, cfg *Config, url string) (*Stream, error) {
	conf := *cfg
	conf.defaults()

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &Stream{cfg: conf, url: url, cancel: cancel, q: newQueue(conf.Limit), tail: nil}
	stop := context.AfterFunc(dial, cancel)

	resp, err := stream.connect(streamCtx)
	if !stop() && err == nil { // dial ended first and took the stream down with it
		_ = resp.Body.Close()
		err = fmt.Errorf("readahead: connect: %w", dial.Err())
	}

	if err != nil {
		cancel()

		return nil, err
	}

	safego.Go("readahead.run", func() { stream.run(streamCtx, resp) })

	return stream, nil
}

func directType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	// The content types read here. Everything else, HLS and playlist types above all
	// (audio/mpegurl, application/vnd.apple.mpegurl, audio/x-scpls), is ffmpeg's to open:
	// it follows the playlist, which this package does not.
	switch strings.ToLower(mediaType) {
	case "audio/mpeg", "audio/mp3", "audio/aac", "audio/aacp", "audio/ogg", "application/ogg":
		return true
	default:
		return false
	}
}

// WriteTo copies the buffered stream to dst (ffmpeg's stdin) until the stream ends, is
// closed, or a write fails. It returns nil at the end of the stream.
func (s *Stream) WriteTo(dst io.Writer) (int64, error) {
	var total int64

	for {
		chunk, err := s.q.get()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}

			return total, err
		}

		written, err := dst.Write(chunk)

		total += int64(written)
		if err != nil {
			return total, fmt.Errorf("readahead: write: %w", err)
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

// connect issues the GET and accepts only a plain audio stream. No Icy-MetaData header
// is sent, so the body is pure audio; now-playing titles come from their own connection
// (internal/icy).
func (s *Stream) connect(ctx context.Context) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("readahead: build request: %w", err)
	}

	req.Header.Set("User-Agent", "SoundTap/1.0")

	if s.cfg.Trace != nil {
		req = req.WithContext(httptrace.WithClientTrace(ctx, s.cfg.Trace.trace()))
	}

	resp, err := safehttp.Do(ctx, s.cfg.Client, req)
	if err != nil {
		return nil, fmt.Errorf("readahead: connect: %w", err)
	}

	if resp.StatusCode != http.StatusOK || resp.ContentLength >= 0 || !directType(resp.Header.Get("Content-Type")) {
		_ = resp.Body.Close()

		return nil, fmt.Errorf("%w: status %d, type %q, length %d", ErrNotDirect,
			resp.StatusCode, resp.Header.Get("Content-Type"), resp.ContentLength)
	}

	return resp, nil
}

// run keeps one connection after the other until the stream is closed or has been silent
// for MaxOutage, then ends the queue so the decoder sees EOF, exits, and the player's
// own supervisor starts over (which also resolves the station URL again).
func (s *Stream) run(ctx context.Context, resp *http.Response) {
	defer s.cancel()

	backoff := s.cfg.BackoffMin
	lastData := time.Now()

	for {
		if resp != nil {
			queued, err := s.session(ctx, resp)
			_ = resp.Body.Close()

			if ctx.Err() != nil {
				s.q.finish(nil)

				return
			}

			s.cfg.Log.Warn("radio read-ahead: connection lost", "bytes", queued, "buffered", s.q.len(), "err", err)
			// Silent for IdleTimeout with nothing left to play: the listener already
			// hears silence, while the player still says PLAY_STATE. End the stream now
			// so the player shows buffering and starts over, instead of a minute later.
			if errors.Is(err, errIdle) && s.q.len() == 0 {
				s.cfg.Log.Warn("radio read-ahead: stalled with nothing buffered, ending the stream")
				s.q.finish(io.ErrUnexpectedEOF)

				return
			}

			if queued > 0 {
				lastData = time.Now()
				backoff = s.cfg.BackoffMin
			}
		}

		resp = s.reconnect(ctx, lastData, &backoff)
		if resp == nil {
			return
		}
	}
}

// reconnect connects again, backing off between failures. It returns nil when the stream
// is closed or has been silent for MaxOutage, after it ended the queue.
func (s *Stream) reconnect(ctx context.Context, lastData time.Time, backoff *time.Duration) *http.Response {
	for {
		if time.Since(lastData) > s.cfg.MaxOutage {
			s.cfg.Log.Warn("radio read-ahead: giving up", "outage", s.cfg.MaxOutage)
			s.q.finish(io.ErrUnexpectedEOF)

			return nil
		}

		if !sleep(ctx, *backoff) {
			s.q.finish(nil)

			return nil
		}

		resp, err := s.connect(ctx)
		if err == nil {
			return resp
		}

		s.cfg.Log.Warn("radio read-ahead: reconnect failed", "err", err, "retry_in", *backoff)
		*backoff = min(*backoff*backoffFactor, s.cfg.BackoffMax)
	}
}

func sleep(ctx context.Context, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// frame is one read from the socket: bytes, or the error that ended it.
type frame struct {
	data []byte
	err  error
}

// readBody reads the socket into frames until it fails or ctx ends, then closes frames.
func readBody(ctx context.Context, body io.Reader, frames chan<- frame) {
	defer close(frames)

	buf := make([]byte, chunkSize)
	for {
		read, err := body.Read(buf)
		if read > 0 {
			select {
			case frames <- frame{data: bytes.Clone(buf[:read]), err: nil}:
			case <-ctx.Done():
				return
			}
		}

		if err != nil {
			select {
			case frames <- frame{data: nil, err: err}:
			case <-ctx.Done():
			}

			return
		}
	}
}

// burst holds back the first bytes of a reconnect until they show where the server's
// burst overlaps what was already received.
type burst struct {
	scan     []byte    // bytes held back so far
	tail     []byte    // the last bytes the previous connection delivered
	scanning bool      // still looking for tail
	end      time.Time // when the scan gives up: ScanMax after the connect
}

// take adds chunk to the scan. It returns what to queue and true once the scan is over,
// or false while the scan goes on.
func (b *burst) take(chunk []byte, limit int, log *slog.Logger) ([]byte, bool) {
	if !b.scanning {
		return chunk, true
	}

	from := max(len(b.scan)-len(b.tail)+1, 0)
	b.scan = append(b.scan, chunk...)

	index := bytes.Index(b.scan[from:], b.tail)
	switch {
	case index >= 0:
		skip := from + index + len(b.tail)
		log.Info("radio read-ahead: skipped the repeated burst", "bytes", skip)

		rest := bytes.Clone(b.scan[skip:])
		b.scanning, b.scan = false, nil

		return rest, true
	case len(b.scan) >= limit || time.Now().After(b.end):
		return b.stop(), true
	default:
		return nil, false
	}
}

// stop ends the scan and returns what it held back, to be queued unchanged.
func (b *burst) stop() []byte {
	held := b.scan
	b.scanning, b.scan = false, nil

	return held
}

// intake counts what a session queues.
type intake struct {
	stream *Stream
	queued int
}

// push records chunk as the stream's latest bytes and queues it; false once the queue
// takes no more.
func (in *intake) push(chunk []byte) bool {
	if len(chunk) == 0 {
		return true
	}

	in.queued += len(chunk)

	stream := in.stream

	stream.tail = append(stream.tail, chunk...)
	if len(stream.tail) > tailLen {
		stream.tail = bytes.Clone(stream.tail[len(stream.tail)-tailLen:])
	}

	return stream.q.put(chunk)
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
func (s *Stream) session(ctx context.Context, resp *http.Response) (int, error) {
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	frames := make(chan frame, framesBuffered)

	safego.Go("readahead.read", func() { readBody(readCtx, resp.Body, frames) })
	// cancel() above stops the goroutine at its next send; closing the body (the caller
	// does it right after) unblocks a Read that is waiting on the socket.

	fed := &intake{stream: s, queued: 0}
	scan := &burst{
		scan:     nil,
		tail:     bytes.Clone(s.tail),
		scanning: len(s.tail) >= minScanTail,
		end:      time.Now().Add(s.cfg.ScanMax),
	}

	timer := time.NewTimer(s.cfg.IdleTimeout)
	defer timer.Stop()

	for {
		select {
		case <-readCtx.Done():
			return fed.queued, fmt.Errorf("readahead: session: %w", readCtx.Err())
		case <-timer.C:
			err := fed.quiet(scan, s.cfg.IdleTimeout)
			if err != nil {
				return fed.queued, err
			}

			timer.Reset(s.cfg.IdleTimeout)
		case got, open := <-frames:
			if !open {
				return fed.queued, io.EOF
			}

			wait, err := fed.receive(got, scan, &s.cfg)
			if err != nil {
				return fed.queued, err
			}

			timer.Reset(wait)
		}
	}
}

// quiet handles a connection that sent nothing for the timer's wait. A scan still
// running ends there, without a match, and what it held back is queued as it is; a
// connection that is not scanning has been idle for idle and ends with errIdle.
func (in *intake) quiet(scan *burst, idle time.Duration) error {
	if !scan.scanning {
		return fmt.Errorf("%w for %s", errIdle, idle)
	}

	if !in.push(scan.stop()) {
		return context.Canceled
	}

	return nil
}

// receive handles one read from the socket. It returns how long the next read may take,
// or the error that ends the session.
func (in *intake) receive(got frame, scan *burst, cfg *Config) (time.Duration, error) {
	if got.err != nil {
		if scan.scanning {
			in.push(scan.stop())
		}

		return 0, got.err
	}

	chunk, ready := scan.take(got.data, cfg.Limit, cfg.Log)
	if !ready {
		return cfg.ScanWait, nil
	}

	if !in.push(chunk) {
		return 0, context.Canceled
	}

	return cfg.IdleTimeout, nil
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
	q := &queue{
		mu: sync.Mutex{}, cv: nil, chunks: nil, size: 0, limit: limit, done: false, err: nil, dead: false,
	}
	q.cv = sync.NewCond(&q.mu)

	return q
}

func (q *queue) put(chunk []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for !q.dead && !q.done && q.size > 0 && q.size+len(chunk) > q.limit {
		q.cv.Wait()
	}

	if q.dead || q.done {
		return false
	}

	q.chunks = append(q.chunks, chunk)
	q.size += len(chunk)
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

	chunk := q.chunks[0]
	q.chunks[0] = nil
	q.chunks = q.chunks[1:]
	q.size -= len(chunk)
	q.cv.Broadcast()

	return chunk, nil
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
