// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package player

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/supervise"
	"github.com/stein155/soundtap/app/internal/upnp"
)

// ffmpeg runs as root and must not follow a redirect to loopback: it follows none,
// and soundtapd hands it the URL its own redirects end at, checked on every hop.
//
//nolint:paralleltest // swaps package variables other tests read: playlistClient
func TestFFmpegFollowsNoRedirectsItself(t *testing.T) {
	args := playbackInput("https://example.test/radio")
	if at := slices.Index(args, "-max_redirects"); at < 0 || args[at+1] != "0" {
		t.Fatalf("input args = %q, want -max_redirects 0", args)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/moved":
			http.Redirect(w, r, "/stream", http.StatusFound)
		case "/to-loopback":
			http.Redirect(w, r, "http://127.0.0.1:9/api/x", http.StatusFound)
		default:
		}
	}))
	defer srv.Close()

	saved := playlistClient

	t.Cleanup(func() { playlistClient = saved })

	// The test server is on loopback, so dial it with a plain transport but keep
	// MediaClient's redirect rule.
	rules := upnp.MediaClient(2 * time.Second)
	playlistClient = &http.Client{Timeout: time.Second}

	if got := FFmpegURL(t.Context(), srv.URL+"/moved"); got != srv.URL+"/stream" {
		t.Errorf("ffmpegURL followed to %q, want %q", got, srv.URL+"/stream")
	}

	playlistClient = &http.Client{Timeout: time.Second, CheckRedirect: rules.CheckRedirect}

	if got := FFmpegURL(t.Context(), srv.URL+"/to-loopback"); got != srv.URL+"/to-loopback" {
		t.Errorf("redirect to loopback gave %q, want the URL unchanged", got)
	}

	playlistClient = saved

	if got := FFmpegURL(t.Context(), srv.URL+"/moved"); got != srv.URL+"/moved" {
		t.Errorf("production client reached loopback: %q", got)
	}
}

func TestPlaybackAudioFilterHasNoBass(t *testing.T) {
	t.Parallel()

	// Stock ST10/ST20 have no audio stage for bass: ffmpeg must not add one.
	if got := playbackAudioFilter(0.5, 0); got != "volume=0.50" {
		t.Fatalf("filter = %q", got)
	}
}

func TestPlaybackAudioFilterFadesInNewStation(t *testing.T) {
	t.Parallel()

	if got := playbackAudioFilter(1, radioFadeIn); got != "volume=1.00,afade=t=in:d=3:curve=cub" {
		t.Fatalf("fade-in filter = %q", got)
	}
}

func TestPlaybackFFmpegOverwritesFIFO(t *testing.T) {
	t.Parallel()

	args := playbackFFmpegArgs("https://example.test/radio", "volume=1", "s16le", "48000", "/run/soundtap/source.pcm")
	if !slices.Contains(args, "-y") {
		t.Fatal("ffmpeg must overwrite the existing source FIFO")
	}
}

func TestPlaybackTuneInWritesRaw48K(t *testing.T) {
	t.Parallel()

	args := playbackTuneInArgs("https://example.test/live", "volume=1", "s16le", "48000", "/run/soundtap/source.pcm")

	want := []string{"-f", "s16le", "-ar", "48000", "-ac", "2"}
	for i := 0; i+len(want) <= len(args); i++ {
		if slices.Equal(args[i:i+len(want)], want) {
			return
		}
	}

	t.Fatalf("TuneIn args = %q, want sequence %q", args, want)
}

// A playlist or HLS manifest can name file: or concat:; the whitelist must sit before -i
// to apply to the input and everything it opens.
func TestPlaybackArgsWhitelistProtocols(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		playbackFFmpegArgs("https://example.test/radio", "volume=1", "s16le", "48000", "x"),
		playbackTuneInArgs("https://example.test/live", "volume=1", "s16le", "48000", "x"),
	} {
		at := slices.Index(args, "-i")
		if at < 2 || args[at-2] != "-protocol_whitelist" || strings.Contains(args[at-1], "file") {
			t.Errorf("args = %q, want -protocol_whitelist without file right before -i", args)
		}
	}
}

func TestDecoderStartupDeadline(t *testing.T) {
	t.Parallel()

	if got := decoderStartupDeadline("ffmpeg"); got < 40*time.Second {
		t.Fatalf("ffmpeg startup deadline = %v, want at least 40s", got)
	}

	if got := decoderStartupDeadline("/usr/bin/ffmpeg"); got != bufferDeadline {
		t.Fatalf("TuneIn startup deadline = %v, want %v", got, bufferDeadline)
	}
}

func TestPlaybackOutputALSA(t *testing.T) {
	t.Parallel()

	format, dev := playbackOutput("s16le", "alsa:plughw:0,0")
	if format != "alsa" || dev != "plughw:0,0" {
		t.Fatalf("playbackOutput = %q, %q; want alsa, plughw:0,0", format, dev)
	}
}

func TestSourcePreemptionTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, previous, next string
		want                 []string
	}{
		{"TUNEIN to SPOTIFY", "TUNEIN", "SPOTIFY", []string{"decoder"}},
		{"TUNEIN to AIRPLAY", "TUNEIN", "AIRPLAY", []string{"decoder"}},
		{"SPOTIFY to TUNEIN", "SPOTIFY", "TUNEIN", []string{"decoder", "SPOTIFY"}},
		{"AIRPLAY to SPOTIFY", "AIRPLAY", "SPOTIFY", []string{"decoder", "AIRPLAY"}},
		{"station switch", "TUNEIN", "TUNEIN", []string{"decoder"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var got []string

			processes := supervise.NewProcesses()
			processes.Register("AIRPLAY", func() { got = append(got, "AIRPLAY") }, nil)
			processes.Register("SPOTIFY", func() { got = append(got, "SPOTIFY") }, nil)
			preemptSource(test.previous, test.next, func() { got = append(got, "decoder") }, processes)

			if !slices.Equal(got, test.want) {
				t.Fatalf("transition order = %v, want %v", got, test.want)
			}
		})
	}
}

// The radio decoder is a stripped ffmpeg that only knows the filters its package
// enables; a filter missing there makes every station fail with "Filter not found".
func TestPlaybackAudioFilterOnlyUsesBuiltFilters(t *testing.T) {
	t.Parallel()

	recipe, err := os.ReadFile("../../../os/package/soundtap-ffmpeg/soundtap-ffmpeg.mk")
	if err != nil {
		t.Fatal(err)
	}

	match := regexp.MustCompile(`(?m)^SOUNDTAP_FFMPEG_FILTERS = (.*)$`).FindSubmatch(recipe)
	if match == nil {
		t.Fatal("no SOUNDTAP_FFMPEG_FILTERS in soundtap-ffmpeg.mk")
	}

	built := strings.Fields(string(match[1]))

	for f := range strings.SplitSeq(playbackAudioFilter(1, radioFadeIn), ",") {
		name, _, _ := strings.Cut(f, "=")
		if !slices.Contains(built, name) {
			t.Errorf("filter %q is used but not built into ffmpeg (%s)", name, match[1])
		}
	}
}

// The cut-down ffmpeg only has the protocols its package lists; one the player relies
// on but that is not built fails every station with "Protocol not found".
func TestPlaybackInputOnlyUsesBuiltProtocols(t *testing.T) {
	t.Parallel()

	recipe, err := os.ReadFile("../../../os/package/soundtap-ffmpeg/soundtap-ffmpeg.mk")
	if err != nil {
		t.Fatal(err)
	}

	match := regexp.MustCompile(`(?m)^SOUNDTAP_FFMPEG_PROTOCOLS = (.*)$`).FindSubmatch(recipe)
	if match == nil {
		t.Fatal("no SOUNDTAP_FFMPEG_PROTOCOLS in soundtap-ffmpeg.mk")
	}

	built := strings.Fields(string(match[1]))
	piped := playbackInput(pipeInput)
	pipedProtocols := strings.Split(piped[slices.Index(piped, "-protocol_whitelist")+1], ",")
	// crypto is whitelisted for AES HLS but need not be built: without it only that fails.
	need := append([]string{"http", "https", "tcp", "tls"}, pipedProtocols...)
	for _, protocol := range need {
		if !slices.Contains(built, protocol) {
			t.Errorf("protocol %q is used but not built into ffmpeg (%s)", protocol, match[1])
		}
	}
}

// A stream soundtapd reads ahead arrives on stdin and ffmpeg may open nothing but pipe;
// an HLS manifest or playlist keeps its URL, the http whitelist and ffmpeg's reconnect.
func TestPlaybackInputPipedForDirectStreamsURLForHLS(t *testing.T) {
	t.Parallel()

	const hls = "https://example.test/live/master.m3u8"

	piped := playbackTuneInArgs(pipeInput, "volume=1", "s16le", "48000", "x")

	inputAt := slices.Index(piped, "-i")
	if inputAt < 2 || piped[inputAt+1] != "pipe:0" || piped[inputAt-2] != "-protocol_whitelist" ||
		piped[inputAt-1] != "pipe" {
		t.Errorf("piped args = %q, want -protocol_whitelist pipe -i pipe:0", piped)
	}

	if slices.Contains(piped, "-reconnect") || slices.Contains(piped, "-rw_timeout") ||
		slices.Contains(piped, "https://example.test/live.mp3") {
		t.Errorf("piped args = %q, must carry no URL and no http options", piped)
	}

	direct := playbackTuneInArgs(hls, "volume=1", "s16le", "48000", "x")

	inputAt = slices.Index(direct, "-i")
	if inputAt < 0 || direct[inputAt+1] != hls || direct[inputAt-1] != FFmpegProtocols ||
		!slices.Contains(direct, "-reconnect") || !slices.Contains(direct, "-rw_timeout") {
		t.Errorf("direct args = %q, want the URL with the http whitelist and reconnect", direct)
	}

	if slices.Contains(direct, "-reconnect_streamed") {
		t.Errorf("direct args = %q: on a body without a length this reconnects without delay", direct)
	}
	// Only the input changes: the filter, rate and format are byte-identical.
	tail := func(a []string) []string { return a[slices.Index(a, "-af"):] }
	if !slices.Equal(tail(piped), tail(direct)) {
		t.Errorf("decode args differ: %q vs %q", tail(piped), tail(direct))
	}
}

// A UPnP resume or Seek puts -ss before -i; at 0 the command is unchanged.
func TestPlaybackSeek(t *testing.T) {
	t.Parallel()

	args := playbackFFmpegArgs("http://192.168.1.9/a.flac", "volume=1", "s16le", "48000", "-")
	if got := playbackSeek(args, 0); !slices.Equal(got, args) {
		t.Fatalf("at 0: %v", got)
	}

	got := strings.Join(playbackSeek(args, 90500*time.Millisecond), " ")
	if !strings.Contains(got, "-ss 90.500 -i http://192.168.1.9/a.flac") {
		t.Fatalf("args = %s", got)
	}
}
