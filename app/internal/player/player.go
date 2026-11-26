// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package player plays the sources soundtapd decodes itself, radio (TUNEIN) and UPnP,
// through ffmpeg into the audio sink: it resolves a station to its streams, reads a radio
// stream ahead, confirms that audio reaches the DAC, and retries a stream that fails.
package player

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/readahead"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/supervise"
	"github.com/stein155/soundtap/app/internal/upnp"
)

// The source names and play statuses the player acts on, as they appear in the API.
const (
	sourceTuneIn  = "TUNEIN"
	sourceUPnP    = "UPNP"
	playPlaying   = "PLAY_STATE"
	statusPaused  = "PAUSE_STATE"
	defaultFormat = "s16le"
)

// Config is what the player plays into.
type Config struct {
	// Sink is where the decoder writes: the Mixer's source bus when Mixed, the audio
	// device otherwise.
	Sink string
	// Mixed says the Mixer owns the DAC (AUDIO_MIX=1): the bus stays fixed at S16_LE at
	// audio.SampleRate, the Mixer applies the volume and does the ducking.
	Mixed bool
	// Mute drives the amp mute. With the Mixer on, the Mixer owns the DAC and the mute
	// (via the ownership tracker) — TuneIn stopping must not mute AirPlay/BT — so the
	// player drives it only in the legacy direct-to-device path.
	Mute func(muted bool)
	// Health reports cumulative PCM flow, so the player can tell "ffmpeg is running"
	// apart from "audio is actually reaching the DAC". nil on the legacy path, where no
	// Mixer exists.
	Health func() audio.Health
	// Flush drops whatever is still queued between the bus and the DAC. nil on the legacy
	// path, where there is no Mixer to flush.
	Flush func()
	// Receivers are the external receivers' processes: a stream selected over one stops it.
	Receivers *supervise.Processes
	// Streams keeps resolved TuneIn stream lists; nil remembers nothing.
	Streams *StreamCache
	// DecoderUser is the system user every decoder runs as.
	DecoderUser string
}

const (
	// retryBackoffMin/Max bound how fast a failing station is retried. A station that
	// cannot be resolved (DNS not up yet right after boot is the common case) used to
	// stop retrying forever, because the player had already recorded the new state as
	// current and never saw a change again.
	retryBackoffMin = 2 * time.Second
	retryBackoffMax = 30 * time.Second

	// bufferDeadline is how long a stream may sit in BUFFERING_STATE before the player
	// gives up and reports it as stopped. It must exceed the stream-resolve timeout
	// plus a slow ffmpeg connect.
	bufferDeadline       = 25 * time.Second
	ffmpegBufferDeadline = 45 * time.Second

	// confirmBlocks is how much genuinely non-silent audio must reach the DAC before a
	// station counts as playing. A few blocks avoids confirming on a lone decode glitch.
	confirmBlocks = 8

	// backoffFactor multiplies the wait before each retry of a failing station.
	backoffFactor = 2

	// reportsBuffered is how many decoder exits and no-audio reports may wait for the loop.
	reportsBuffered = 4

	// fullVolume is the speaker volume at which ffmpeg plays at full scale.
	fullVolume = 100.0

	// directRate is the sample rate ffmpeg writes on the legacy direct-to-device path.
	directRate = "48000"
)

// A station that played and dropped shows "buffering" for outageBuffering before
// "stopped"; retries end outageGiveUp after the first failure, so music cannot come back
// hours later. Variables for the tests.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var (
	outageBuffering = time.Minute
	outageGiveUp    = 10 * time.Minute
)

func decoderStartupDeadline(decoder string) time.Duration {
	if decoder == "ffmpeg" {
		return ffmpegBufferDeadline
	}

	return bufferDeadline
}

// lookupWhileWanted runs lookup until it returns or wanted turns false, whichever comes
// first; wanted is checked again on every change signal. current is false when the
// selection moved on, and then url and err mean nothing.
func lookupWhileWanted(ctx context.Context, changed func() <-chan struct{}, wanted func() bool,
	lookup func(context.Context) (string, error),
) (string, bool, error) {
	lookupCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	safego.Go("lookupWhileWanted", func() {
		for {
			change := changed() // taken before the check, so no change slips between them

			if !wanted() {
				cancel()

				return
			}

			select {
			case <-change:
			case <-lookupCtx.Done():
				return
			}
		}
	})

	url, err := lookup(lookupCtx)

	if !wanted() {
		return "", false, nil
	}

	return url, true, err
}

// exit is a decoder's end as its watcher reports it.
type exit struct {
	gen   int
	clean bool // ffmpeg exited 0: it reached the end of its input
}

// state is what the speaker wants played, as the loop last acted on it.
type state struct {
	src, loc  string
	vol       int
	selection uint64
	paused    bool
}

// loop is one Run: the decoder it runs and what it knows of the attempts so far.
type loop struct {
	sp                 *speakerstate.Speaker
	tc                 Resolver
	cfg                Config
	log                *slog.Logger
	sink, rate, format string

	// gen tags each spawned ffmpeg. A watcher goroutine Waits on the process and reports
	// its gen on dead when it exits; the loop restarts playback if that gen is still
	// current (an unexpected exit — dropped stream) rather than one we killed on a source
	// change (stop bumps gen so its watcher's report is stale).
	cmd           *exec.Cmd
	gen           int
	dead          chan exit
	noAudio       chan uint64
	activePlayGen uint64
	// tailAt: when the last UPnP track ran to its end; the Mixer may still be playing it.
	// The next track of that controller continues behind it instead of flushing it.
	tailAt time.Time

	changed <-chan struct{}
	cur     state
	// A failed attempt must stay retryable. The loop used to record the new state as
	// current before resolving the stream, so after a transient resolve or spawn
	// failure `next == cur` forever and the speaker sat silently "playing" nothing
	// until the user changed something else.
	retryAt   time.Time
	backoff   time.Duration
	startedAt time.Time // when the current decoder was spawned
	// troubleAt: the first failed attempt since the last genuine change or the last drop of
	// a playing stream; heard: that trouble began as a drop of a stream that played.
	troubleAt time.Time
	heard     bool
	// Resolved stream URLs for the station currently selected — see streamCandidates.
	cands streamCandidates
}

// Run watches speakerState for TUNEIN and UPNP selections until ctx ends, resolves a station's
// stream URL through resolver, and plays it through ffmpeg into cfg.Sink. All calls are
// direct — no HTTP loopback.
//
//nolint:gocritic // hugeParam: Run keeps its own copy of cfg, filling in what the caller left out
func Run(ctx context.Context, speakerState *speakerstate.Speaker, resolver Resolver, cfg Config, log *slog.Logger) {
	if cfg.Mute == nil {
		cfg.Mute = func(bool) {}
	}

	run := &loop{
		sp: speakerState, tc: resolver, cfg: cfg, log: log,
		sink: cfg.Sink, rate: directRate, format: defaultFormat,
		cmd: nil, gen: 0,
		dead: make(chan exit, reportsBuffered), noAudio: make(chan uint64, reportsBuffered),
		activePlayGen: 0, tailAt: time.Time{},
		changed: nil, cur: state{src: "", loc: "", vol: 0, selection: 0, paused: false},
		retryAt: time.Time{}, backoff: 0, startedAt: time.Time{}, troubleAt: time.Time{}, heard: false,
		cands: streamCandidates{loc: "", urls: nil, i: 0, cached: false, cache: cfg.Streams},
	}
	if cfg.Mixed {
		// With the arbiter on, ffmpeg feeds the raw-PCM source bus (the Mixer owns the
		// DAC and does the ducking); the bus stays fixed at S16_LE 48 kHz stereo.
		run.format, run.rate = defaultFormat, strconv.Itoa(audio.SampleRate)
	}
	defer run.stop()
	// External receivers must preempt TuneIn before their first PCM reaches the bus:
	// the loop wakes on every state change (speakerstate.Changed), not on a poll.
	// The first pass reads the state as it already is: a selection made before this loop
	// started (a restore at boot, a quick first tap) would otherwise wait for the next
	// change to be played.
	start := make(chan struct{})
	close(start)

	run.changed = start
	for run.wait(ctx) {
		run.pass(ctx)
	}
}

// mute mutes the amp, in the legacy direct-to-device path only (Config.Mute).
func (l *loop) mute() {
	if !l.cfg.Mixed {
		l.cfg.Mute(true)
	}
}

func (l *loop) stop() {
	if l.cmd != nil && l.cmd.Process != nil {
		l.gen++ // supersede the watcher so its exit report is ignored
		_ = l.cmd.Process.Kill()
		l.cmd = nil
		l.mute()
	}
}

// fail records an attempt that produced no audio: tell the UI, unless a dropped stream
// is still inside its buffering grace, and schedule a retry instead of wedging.
func (l *loop) fail(playGen uint64) {
	if l.troubleAt.IsZero() {
		l.troubleAt = time.Now()
	}

	giveUp := time.Since(l.troubleAt) >= outageGiveUp
	if !l.heard || giveUp || time.Since(l.troubleAt) >= outageBuffering {
		l.sp.FailPlayback(playGen)
	} else if attempt := l.sp.BeginBuffering(l.cur.src); attempt != 0 {
		// Still buffering, under a new attempt: a late confirmation of the failed
		// one must not show PLAY.
		l.activePlayGen = attempt
	}

	if giveUp {
		l.log.Warn("stream failed for too long, giving up until the next selection or play")
		l.retryAt = time.Time{}

		return
	}

	l.backoff = min(max(l.backoff*backoffFactor, retryBackoffMin), retryBackoffMax)
	l.retryAt = time.Now().Add(l.backoff)
}

// wait waits for something to act on: a state change, a retry that is due, a decoder
// that ended or one that produced no audio. It reports false once ctx has ended.
func (l *loop) wait(ctx context.Context) bool {
	var retry <-chan time.Time // the backoff, when an attempt failed
	if !l.retryAt.IsZero() {
		retry = time.After(time.Until(l.retryAt))
	}

	select {
	case <-ctx.Done():
		return false
	case <-l.changed:
	case <-retry:
	case ended := <-l.dead:
		l.exited(ctx, ended)
	case failedGen := <-l.noAudio:
		if failedGen == l.activePlayGen {
			l.stop()
			l.fail(failedGen)
		}
	}

	return true
}

// exited takes a decoder's end.
func (l *loop) exited(ctx context.Context, ended exit) {
	if ended.gen != l.gen || ctx.Err() != nil {
		return
	}

	if ended.clean && l.cur.src == sourceUPnP {
		// A UPnP medium is a file: reaching its end is not a drop. Report STOPPED
		// and keep the source, so the controller moves on to its next track instead
		// of hearing this one again after the backoff. ffmpeg is done
		// seconds before the speaker is: the Mixer still holds the tail. STOPPED
		// goes out now, so the next track starts while that tail plays, and the
		// next track does not flush it but queues behind it.
		l.log.Info("upnp track ended")
		l.mute() // the legacy path only, where ffmpeg wrote to the DAC itself
		l.cmd = nil
		l.gen++
		l.tailAt = time.Now()
		l.sp.EndPlayback(l.activePlayGen)

		return
	}

	l.log.Warn("decoder exited unexpectedly, trying fallback")
	l.mute()
	l.cmd = nil
	l.gen++
	// A drop of a stream that played starts a new outage and shows buffering;
	// a decoder that dies right after every spawn climbs the same ladder as a
	// failed start.
	wasPlaying := l.sp.StatusSnapshot().PlayStatus == playPlaying
	if wasPlaying {
		l.troubleAt, l.heard = time.Time{}, true
	}

	if wasPlaying || time.Since(l.startedAt) > retryBackoffMax {
		l.backoff = 0
	}

	l.fail(l.activePlayGen)
}

// pass acts on the state as it is now.
func (l *loop) pass(ctx context.Context) {
	l.changed = l.sp.Changed()
	src, loc, vol, _, selection := l.sp.PlaybackSelection()
	// A paused radio stream has nothing to hold: stop it, and tune in again on play.
	paused := l.sp.StatusSnapshot().PlayStatus == statusPaused
	next := state{src, loc, vol, selection, paused}

	retryDue := !l.retryAt.IsZero() && !time.Now().Before(l.retryAt)
	if next == l.cur && !retryDue {
		return
	}

	if !retryDue {
		// A genuine state change starts the backoff ladder and the outage over.
		l.backoff = 0
		l.troubleAt = time.Time{}
		l.heard = false
	}

	l.retryAt = time.Time{}
	cur := l.cur
	change := l.compare(cur, next, retryDue)
	l.cur = next

	if !change.restart {
		return // e.g. volume-only change under AUDIO_MIX — the Mixer handles it live
	}

	l.tailAt = time.Time{}
	l.handOver(cur.src, src)
	// A continuation keeps the tail: its blocks then count towards confirmPlayback,
	// so the next track can read as playing up to a tail (~4 s) early.
	// a next track that runs but stays silent is not caught then; skip the
	// queued blocks in confirmPlayback if that ever shows up.
	if !change.continuation {
		l.flush()
	}
	// soundtapd streams two source kinds through ffmpeg: TUNEIN (resolve station ->
	// stream URL) and UPNP (the location IS the media URL, from a DLNA controller).
	if (src != sourceTuneIn && src != sourceUPnP) || loc == "" {
		return
	}

	if paused {
		l.stop()

		return
	}

	// The first attempt may play a cached stream list; a retry moves past what failed.
	pick := l.cands.url
	if retryDue {
		pick = l.cands.retry
	}

	var fade time.Duration
	if change.fadeIn && !change.continuation {
		fade = radioFadeIn
	}

	l.play(ctx, next, pick, fade)
}

// change is what moving from one state to the next asks of the decoder.
type change struct {
	restart      bool // the decoder starts over
	fadeIn       bool // the new stream fades in
	continuation bool // next continues a UPnP track that just ended
}

// compare says what moving from cur to next asks of the decoder: a restart, a fade-in, and
// whether next continues a UPnP track that just ended.
func (l *loop) compare(cur, next state, retryDue bool) change {
	newStream := next.src != cur.src || next.loc != cur.loc || next.selection != cur.selection
	// Restart ffmpeg only when something it actually bakes in changed: source, stream,
	// Bass is not one of them: the Mixer's shelf applies it, so a bass change
	// must not respawn the stream. Volume lives in
	// the Mixer under AUDIO_MIX (a vol-only change must NOT
	// respawn ffmpeg — that used to leak a second decoder onto the bus); in the legacy
	// path volume is an ffmpeg filter, so it still forces a restart.
	return change{
		restart: retryDue || newStream || next.paused != cur.paused || (!l.cfg.Mixed && next.vol != cur.vol),
		// A new station (or a dropped one coming back) fades in; a volume
		// respawn of the same stream must not dip the sound every time.
		fadeIn: retryDue || newStream,
		// Gapless: the next track of the same controller, right after the last one ended.
		// Only a new location counts; anything else (Stop, pause, another source)
		// flushes as before.
		continuation: !l.tailAt.IsZero() && time.Since(l.tailAt) < tailMax && !retryDue &&
			cur.src == sourceUPnP && next.src == sourceUPnP && next.loc != cur.loc &&
			next.paused == cur.paused,
	}
}

// handOver stops what played before src: the decoder, and the receiver that owned the amp.
func (l *loop) handOver(previous, src string) {
	preemptSource(previous, src, l.stop, l.cfg.Receivers)
}

// flush drops the outgoing stream's queued audio. Killing its ffmpeg leaves ~300 ms
// sitting in the jitter buffer and the pipes, and that tail does two visible
// things: you hear the end of the previous station over the start of the new one,
// and confirmPlayback counts it. The counters advance at the moment the Mixer
// *writes* to the sink, so a tail of a few hundred milliseconds is easily the
// confirmBlocks the new attempt is waiting for — the UI leaves "buffering" while
// the new stream has not produced a single byte, and then sits silent until it
// does. Flushing first makes the Mixer write silence (which is not counted), so
// confirmation once again means this stream's audio.
func (l *loop) flush() {
	if l.cfg.Flush != nil {
		l.cfg.Flush()
	}
}

// picker returns the stream to try for a TuneIn location: streamCandidates.url or retry.
type picker func(ctx context.Context, resolver Resolver, loc string) (string, error)

// play starts the stream next selects, fading it in over fade.
func (l *loop) play(ctx context.Context, next state, pick picker, fade time.Duration) {
	src, loc := next.src, next.loc
	// From here the UI shows "buffering" until PCM actually reaches the DAC.
	// playGen names this attempt so a slow stream cannot report on behalf of the
	// station the user switched to in the meantime.
	playGen := l.sp.BeginBuffering(src)
	beganAt := time.Now()
	l.activePlayGen = playGen

	// wanted is false once the selection moved on, or playback stopped or paused.
	wanted := func() bool {
		nowSrc, nowLoc, _, _, nowSelection := l.sp.PlaybackSelection()

		return nowSrc == src && nowLoc == loc && nowSelection == next.selection &&
			l.sp.StatusSnapshot().PlayStatus != statusPaused
	}

	url, current, err := l.streamURL(ctx, src, loc, pick, wanted)
	if !current {
		return
	}

	if err != nil {
		if errors.Is(err, errNotMedia) {
			l.log.Warn("refusing UPNP location that is not a media URL", "loc", loc)
		} else {
			l.log.Warn("no stream url", "src", src, "loc", loc, "err", err, "retry_in", l.backoff)
		}

		l.fail(playGen)

		return
	}

	// A plain radio stream is read ahead here and fed to ffmpeg on stdin, so a Wi-Fi
	// stall shorter than the buffer is not heard and a reconnect does not repeat the
	// server's burst (ADR 0041). UPnP media are files with a length and keep ffmpeg's
	// own reader, as does anything the reader declines (HLS, playlists, ICY).
	var (
		feed  *readahead.Stream
		trace readahead.Timing
	)

	resolvedAt := time.Now() // stream URL known; what follows is the connect to the station

	if src == sourceTuneIn {
		var ok bool

		feed, ok = l.readAhead(ctx, url, wanted, &trace)
		if !ok {
			return
		}
	}
	// A stream read ahead arrives on stdin; ffmpeg opens anything else itself.
	input := pipeInput
	if feed == nil {
		input = resolveRedirects(ctx, url)
	}

	decoder, args := l.decoderArgs(src, input, next.vol, fade)
	l.log.Info("play", "src", src, "decoder", decoder, "url", url, "vol", next.vol, "dev", l.sink,
		"resolve_ms", resolvedAt.Sub(beganAt).Milliseconds(), "connect_ms", time.Since(resolvedAt).Milliseconds(),
		"dns_ms", trace.DNS, "tcp_ms", trace.TCP, "tls_ms", trace.TLS, "ttfb_ms", trace.TTFB)
	l.spawn(ctx, decoder, args, feed, playGen)
}

// errNotMedia is a UPnP location that is not a media URL.
var errNotMedia = errors.New("not a media URL")

// streamURL resolves what src and loc play, a TuneIn location through pick. The second
// result is false when the selection moved on meanwhile.
func (l *loop) streamURL(ctx context.Context, src, loc string, pick picker, wanted func() bool,
) (string, bool, error) {
	if src == sourceUPnP {
		// Defence in depth: /api/upnp/play validates this, but a DLNA controller on
		// the LAN can also set the location, and ffmpeg would open file:, concat: or
		// pipe: just as willingly as http. A .pls or plain .m3u, which controllers
		// send for radio and ffmpeg cannot open, plays its first entry.
		if !upnp.MediaURL(ctx, loc) {
			return "", true, errNotMedia
		}

		return lookupWhileWanted(ctx, l.sp.Changed, wanted, func(lookupCtx context.Context) (string, error) {
			urls, err := ownStreamURLs(lookupCtx, loc)
			if err != nil {
				return "", err
			}

			return urls[0], nil
		})
	}
	// The lookup can take up to 10 s. A new selection, another source or standby
	// cancels it, and its result is dropped: the loop picks up the new state at once
	// instead of starting the old station's decoder after it.
	return lookupWhileWanted(ctx, l.sp.Changed, wanted, func(lookupCtx context.Context) (string, error) {
		return pick(lookupCtx, l.tc, loc)
	})
}

// readAhead opens url for reading ahead; a nil stream leaves ffmpeg to open it. ok is
// false when the selection moved on meanwhile.
func (l *loop) readAhead(ctx context.Context, url string, wanted func() bool, trace *readahead.Timing,
) (*readahead.Stream, bool) {
	var feed *readahead.Stream

	_, current, _ := lookupWhileWanted(ctx, l.sp.Changed, wanted, func(dial context.Context) (string, error) {
		var err error

		feed, err = readahead.Open(ctx, dial, &readahead.Config{
			Client: nil, Trace: trace, Log: l.log, Limit: 0, IdleTimeout: 0, MaxOutage: 0,
			BackoffMin: 0, BackoffMax: 0, ScanWait: 0, ScanMax: 0,
		}, url)
		if err != nil {
			l.log.Info("radio read-ahead off, ffmpeg opens the stream", "url", url, "err", err)
		}

		return "", nil
	})
	if !current {
		if feed != nil {
			feed.Close()
		}

		return nil, false
	}

	return feed, true
}

// decoderArgs is the decoder to start for src and its arguments; input is the URL ffmpeg
// opens, or pipeInput for a stream that comes on stdin.
func (l *loop) decoderArgs(src, input string, vol int, fade time.Duration) (string, []string) {
	gain := float64(vol) / fullVolume
	if l.cfg.Mixed {
		gain = 1.0 // the Mixer applies master volume for all sources; ffmpeg stays full-scale
	}

	audioFilter := playbackAudioFilter(gain, fade)
	outputFormat, outputDev := playbackOutput(l.format, l.sink)
	// Buildroot's ffmpeg, cut down in the defconfig to the audio parts radio and
	// AirPlay use; a general build maps dozens of video libraries and takes tens of
	// seconds to start on this box.
	if src == sourceTuneIn && l.cfg.Mixed {
		return "/usr/bin/ffmpeg", playbackTuneInArgs(input, audioFilter, l.format, l.rate, l.sink)
	}

	args := playbackFFmpegArgs(input, audioFilter, outputFormat, l.rate, outputDev)
	if src == sourceUPnP {
		args = playbackSeek(args, l.sp.StartAt())
	}

	return "ffmpeg", args
}

// spawn starts the decoder, feeds it the read-ahead stream, and watches it.
func (l *loop) spawn(ctx context.Context, decoder string, args []string, feed *readahead.Stream, playGen uint64) {
	cmd, err := decoderCommand(ctx, l.cfg.DecoderUser, decoder, args...)
	l.cmd = cmd
	out := supervise.ChildLog(decoder)
	cmd.Stdout, cmd.Stderr = out, out

	var stdin io.WriteCloser
	if err == nil && feed != nil {
		// Not cmd.Stdin = reader: Wait would then wait for a copy goroutine that
		// can be stuck on a stalled stream after ffmpeg is gone.
		stdin, err = cmd.StdinPipe()
	}

	if err == nil {
		err = cmd.Start()
	}

	if err != nil {
		if feed != nil {
			feed.Close()
		}

		l.log.Error("ffmpeg start", "err", err, "retry_in", l.backoff)
		l.cmd = nil
		l.mute()
		l.fail(playGen)

		return
	}

	l.startedAt = time.Now()
	// Watch this process: report its gen when it exits so the loop can restart a
	// dropped stream. It also reaps the process (stop only kills).
	gen, dead := l.gen, l.dead

	if feed != nil {
		safego.Go("playerFeed", func() { // ends when the stream does or ffmpeg stops reading
			_, _ = feed.WriteTo(stdin)
			_ = stdin.Close()
		})
	}

	safego.Go("playerWait", func() {
		err := cmd.Wait()

		if feed != nil {
			feed.Close()
		}

		select {
		case dead <- exit{gen: gen, clean: err == nil}:
		default:
		}
	})

	health, sp, noAudio, log := l.cfg.Health, l.sp, l.noAudio, l.log

	safego.Go("confirmPlayback", func() {
		confirmPlayback(ctx, sp, health, playGen, decoderStartupDeadline(decoder), noAudio, log)
	})
}
