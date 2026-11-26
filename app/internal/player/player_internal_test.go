// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package player

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/supervise"
)

// spawn is one decoder runPlayer started: the URL it was given and the pid file the
// stand-in writes.
type spawn struct {
	url string
	pid string
}

// fakeDecoder makes runPlayer start `sh -c script` instead of ffmpeg and reports each
// start on the returned channel. The script runs with $PIDFILE set.
func fakeDecoder(t *testing.T, script string) <-chan spawn {
	t.Helper()
	t.Setenv("AUDIO_NOINIT", "1")
	t.Setenv("AUDIO_MIX", "")
	dir := t.TempDir()
	spawns := make(chan spawn, 16)
	n := 0
	old := decoderCommand
	decoderCommand = func(ctx context.Context, _, _ string, args ...string) (*exec.Cmd, error) {
		n++
		pid := filepath.Join(dir, strconv.Itoa(n))

		url := ""
		if i := slices.Index(args, "-i"); i >= 0 && i+1 < len(args) {
			url = args[i+1]
		}

		cmd := exec.CommandContext(ctx, "sh", "-c", script)

		cmd.Env = append(os.Environ(), "PIDFILE="+pid)
		spawns <- spawn{url, pid}

		return cmd, nil
	}
	oldResolve := resolveRedirects
	resolveRedirects = func(_ context.Context, url string) string { return url }

	t.Cleanup(func() { decoderCommand, resolveRedirects = old, oldResolve })

	return spawns
}

// The names and statuses the tests select and wait for, as in the API.
const (
	sourceAirPlay = "AIRPLAY"
	playBuffering = "BUFFERING_STATE"
	playStopped   = "STOP_STATE"
)

// testConfig is the player's config in a test: its own receivers and stream cache, a
// decoder writing to the legacy path unless mixed, and flush and mute as given (nil: none).
func testConfig(flush func(), mixed bool, mute func(bool)) *Config {
	return &Config{
		Sink: "", Mixed: mixed, Mute: mute, Health: nil, Flush: flush,
		Receivers: supervise.NewProcesses(), Streams: newCache(), DecoderUser: "",
	}
}

func startPlayer(t *testing.T, resolver Resolver) *speakerstate.Speaker {
	t.Helper()

	return startPlayerWith(t, resolver, testConfig(nil, false, nil))
}

func startPlayerWith(t *testing.T, resolver Resolver, cfg *Config) *speakerstate.Speaker {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	speakerState := speakerstate.New()

	done := make(chan struct{})
	go func() {
		defer close(done)

		Run(ctx, speakerState, resolver, *cfg, slog.New(slog.DiscardHandler))
	}()

	t.Cleanup(func() { cancel(); <-done })

	return speakerState
}

func selectUPnP(t *testing.T, speakerState *speakerstate.Speaker, url string) {
	t.Helper()

	err := speakerState.Select(t.Context(), sourceUPnP, "", url, "track", "", "")
	if err != nil {
		t.Fatal(err)
	}
}

func nextSpawn(t *testing.T, spawns <-chan spawn, within time.Duration) spawn {
	t.Helper()

	select {
	case s := <-spawns:
		return s
	case <-time.After(within):
		t.Fatalf("no decoder started within %v", within)

		return spawn{url: "", pid: ""}
	}
}

func noSpawn(t *testing.T, spawns <-chan spawn, within time.Duration) {
	t.Helper()

	select {
	case s := <-spawns:
		t.Fatalf("decoder started again for %s", s.url)
	case <-time.After(within):
	}
}

// running waits until the stand-in has written pidFile and returns its pid.
func running(t *testing.T, pidFile string) int {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(pidFile)
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
			return pid
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("decoder %s never ran", pidFile)

	return 0
}

// gone waits until process pid has exited.
func gone(t *testing.T, pid int) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("decoder %d still running", pid)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func waitStatus(t *testing.T, speakerState *speakerstate.Speaker, want string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for speakerState.StatusSnapshot().PlayStatus != want {
		if time.Now().After(deadline) {
			t.Fatalf("play status %s, want %s", speakerState.StatusSnapshot().PlayStatus, want)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

const (
	trackA = "http://192.168.1.10/a.flac"
	trackB = "http://192.168.1.10/b.flac"
)

// A decoder that dies at once is started again after the backoff, not left dead.
//
//nolint:paralleltest // fakeDecoder swaps decoderCommand and resolveRedirects and sets the environment
func TestRunPlayerRetriesADecoderThatDies(t *testing.T) {
	spawns := fakeDecoder(t, "exit 1")
	speakerState := startPlayer(t, nil)
	selectUPnP(t, speakerState, trackA)
	first := nextSpawn(t, spawns, 2*time.Second)
	firstAt := time.Now()

	again := nextSpawn(t, spawns, retryBackoffMin+3*time.Second)
	if first.url != trackA || again.url != trackA {
		t.Fatalf("started %s then %s, want %s twice", first.url, again.url, trackA)
	}

	if waited := time.Since(firstAt); waited < retryBackoffMin/2 {
		t.Fatalf("retried after %v, want the %v backoff", waited, retryBackoffMin)
	}
}

// A UPnP file that plays to its end reports STOP and is not started again.
//
//nolint:paralleltest // fakeDecoder swaps decoderCommand and resolveRedirects and sets the environment
func TestRunPlayerUPnPTrackEndIsNotADrop(t *testing.T) {
	spawns := fakeDecoder(t, "exit 0")
	speakerState := startPlayer(t, nil)
	selectUPnP(t, speakerState, trackA)
	nextSpawn(t, spawns, 2*time.Second)
	waitStatus(t, speakerState, playStopped)
	noSpawn(t, spawns, retryBackoffMin+time.Second)
}

// A new selection kills the running decoder and starts one for the new location.
//
//nolint:paralleltest // fakeDecoder swaps decoderCommand and resolveRedirects and sets the environment
func TestRunPlayerNewSelectionReplacesTheDecoder(t *testing.T) {
	spawns := fakeDecoder(t, `echo $$ > "$PIDFILE"; exec sleep 30`)
	speakerState := startPlayer(t, nil)
	selectUPnP(t, speakerState, trackA)
	pid := running(t, nextSpawn(t, spawns, 2*time.Second).pid)
	selectUPnP(t, speakerState, trackB)

	if second := nextSpawn(t, spawns, 2*time.Second); second.url != trackB {
		t.Fatalf("started %s, want %s", second.url, trackB)
	}

	gone(t, pid)
}

// Pause stops the decoder; play starts it again.
//
//nolint:paralleltest // fakeDecoder swaps decoderCommand and resolveRedirects and sets the environment
func TestRunPlayerPauseStopsAndPlayResumes(t *testing.T) {
	spawns := fakeDecoder(t, `echo $$ > "$PIDFILE"; exec sleep 30`)
	speakerState := startPlayer(t, nil)
	selectUPnP(t, speakerState, trackA)
	pid := running(t, nextSpawn(t, spawns, 2*time.Second).pid)
	waitStatus(t, speakerState, playPlaying)
	speakerState.ApplyKey("PAUSE")
	waitStatus(t, speakerState, statusPaused)
	gone(t, pid)
	speakerState.ApplyKey("PLAY")

	if again := nextSpawn(t, spawns, 2*time.Second); again.url != trackA {
		t.Fatalf("resumed %s, want %s", again.url, trackA)
	}
}

// Where ffmpeg writes to the DAC itself, the player mutes when it kills a decoder; under
// the arbiter it leaves the mute to trackOwnership, so TuneIn stopping cannot mute AirPlay.
//
//nolint:paralleltest // fakeDecoder swaps decoderCommand and resolveRedirects and sets the environment
func TestRunPlayerMutesOnlyInTheLegacyPath(t *testing.T) {
	for _, mix := range []string{"", "1"} {
		t.Run("AUDIO_MIX="+mix, func(t *testing.T) {
			spawns := fakeDecoder(t, `echo $$ > "$PIDFILE"; exec sleep 30`)
			// Like the real line's cache, a repeated value is not an edge.
			var (
				mu    sync.Mutex
				mutes []string
			)

			mute := func(m bool) {
				mu.Lock()
				defer mu.Unlock()

				v := map[bool]string{true: "muted", false: "unmuted"}[m]
				if len(mutes) == 0 || mutes[len(mutes)-1] != v {
					mutes = append(mutes, v)
				}
			}
			edges := func() []string {
				mu.Lock()
				defer mu.Unlock()

				return slices.Clone(mutes)
			}
			speakerState := startPlayerWith(t, nil, testConfig(nil, mix == "1", mute))
			selectUPnP(t, speakerState, trackA)

			pid := running(t, nextSpawn(t, spawns, 2*time.Second).pid)
			if got := edges(); len(got) != 0 {
				t.Fatalf("mute changed %v while the first decoder started", got)
			}

			selectUPnP(t, speakerState, trackB)
			nextSpawn(t, spawns, 2*time.Second)
			gone(t, pid)

			var want []string
			if mix == "" {
				want = []string{"muted"}
			}

			if got := edges(); !slices.Equal(got, want) {
				t.Fatalf("mute edges %v, want %v", got, want)
			}
		})
	}
}

// A stream selected over a receiver stops that receiver before its decoder starts, so
// the two never write the bus together; the receiver taking over again stops the decoder.
//
//nolint:paralleltest // fakeDecoder swaps decoderCommand and resolveRedirects and sets the environment
func TestRunPlayerPreemptsTheOtherSource(t *testing.T) {
	spawns := fakeDecoder(t, `echo $$ > "$PIDFILE"; exec sleep 30`)
	stopped := make(chan struct{}, 4)

	// The loop flushes on every pass that changes the stream, after preempting: the
	// signal that it has seen a state, so AirPlay is the previous source, not skipped.
	flushed := make(chan struct{}, 4)
	cfg := testConfig(func() { flushed <- struct{}{} }, false, nil)
	t.Cleanup(cfg.Receivers.Register(sourceAirPlay, func() { stopped <- struct{}{} }, nil))
	speakerState := startPlayerWith(t, nil, cfg)
	passed := func() {
		t.Helper()

		select {
		case <-flushed:
		case <-time.After(2 * time.Second):
			t.Fatal("the player did not pick up the state")
		}
	}
	passed() // the first pass, on the state at start

	err := speakerState.SetSource(t.Context(), sourceAirPlay)
	if err != nil {
		t.Fatal(err)
	}

	passed()
	selectUPnP(t, speakerState, trackA)
	pid := running(t, nextSpawn(t, spawns, 2*time.Second).pid)

	select {
	case <-stopped:
	default:
		t.Fatal("decoder started without stopping AirPlay first")
	}

	err = speakerState.SetSource(t.Context(), sourceAirPlay)
	if err != nil {
		t.Fatal(err)
	}

	gone(t, pid)
	noSpawn(t, spawns, 500*time.Millisecond)
}

// A station whose stream dies is retried on the next stream the station lists, not the
// same dead one.
//
//nolint:paralleltest // fakeDecoder swaps decoderCommand and resolveRedirects and sets the environment
func TestRunPlayerRetriesTheNextStreamCandidate(t *testing.T) {
	spawns := fakeDecoder(t, "exit 1")
	// Port 1 refuses at once, so the read-ahead declines and ffmpeg gets the URL itself.
	streams := []string{"http://127.0.0.1:1/a.mp3", "http://127.0.0.1:1/b.mp3"}
	// The player reads the stream cache it was given, and a cached list is re-resolved
	// (back to its first URL) after one failure. The test's own cache starts empty, so
	// no other test decides which URL comes second.
	const loc = "/v1/playback/station/s12345"

	speakerState := startPlayer(t, answering(streams...))

	err := speakerState.Select(t.Context(), sourceTuneIn, "stationurl", loc, "Radio", "", "")
	if err != nil {
		t.Fatal(err)
	}

	first := nextSpawn(t, spawns, 5*time.Second)

	second := nextSpawn(t, spawns, retryBackoffMin+5*time.Second)
	if first.url != streams[0] || second.url != streams[1] {
		t.Fatalf("started %s then %s, want %v", first.url, second.url, streams)
	}
}

// roundTripFunc serves a test's HTTP without a network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A DLNA controller that hands over a .pls gets its first stream played, not the
// playlist file, which ffmpeg cannot open.
//
//nolint:paralleltest // swaps playlistClient, and fakeDecoder's package variables
func TestRunPlayerUPnPPlaylistPlaysItsStream(t *testing.T) {
	const stream = "http://192.168.1.10:8000/live.mp3"

	old := playlistClient
	playlistClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := "[playlist]\nNumberOfEntries=1\nFile1=" + stream + "\n"

		return &http.Response{
			StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{},
		}, nil
	})}

	t.Cleanup(func() { playlistClient = old })
	spawns := fakeDecoder(t, `echo $$ > "$PIDFILE"; exec sleep 30`)
	speakerState := startPlayer(t, nil)
	selectUPnP(t, speakerState, "http://192.168.1.10/radio.pls")

	if got := nextSpawn(t, spawns, 2*time.Second); got.url != stream {
		t.Fatalf("started %s, want %s", got.url, stream)
	}
}

// A stream that played and then drops shows buffering, not stopped, while it retries; after
// outageGiveUp the player stops trying, and PLAY tunes in again.
//
//nolint:paralleltest // swaps outageBuffering and outageGiveUp, and fakeDecoder's package variables
func TestRunPlayerDropBuffersThenGivesUp(t *testing.T) {
	oldBuf, oldGive := outageBuffering, outageGiveUp
	outageBuffering, outageGiveUp = time.Minute, 4*time.Second

	t.Cleanup(func() { outageBuffering, outageGiveUp = oldBuf, oldGive })
	once := filepath.Join(t.TempDir(), "played")
	spawns := fakeDecoder(t, `if [ ! -e `+once+` ]; then touch `+once+`; sleep 3; fi; exit 1`)
	speakerState := startPlayer(t, nil)
	selectUPnP(t, speakerState, trackA)
	nextSpawn(t, spawns, 2*time.Second)
	waitStatus(t, speakerState, playPlaying)
	nextSpawn(t, spawns, 5*time.Second) // the first retry, 2 s after the drop

	if st := speakerState.StatusSnapshot().PlayStatus; st != playBuffering {
		t.Fatalf("play status %s during the outage, want %s", st, playBuffering)
	}

	nextSpawn(t, spawns, 6*time.Second) // the second, 4 s later; it fails past outageGiveUp
	waitStatus(t, speakerState, playStopped)
	noSpawn(t, spawns, 10*time.Second)
	speakerState.ApplyKey("PLAY")
	nextSpawn(t, spawns, 2*time.Second)
}
