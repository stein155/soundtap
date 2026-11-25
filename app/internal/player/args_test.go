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
func TestFFmpegFollowsNoRedirectsItself(t *testing.T) {
	args := playbackInput("https://example.test/radio", false)
	if i := slices.Index(args, "-max_redirects"); i < 0 || args[i+1] != "0" {
		t.Fatalf("input args = %q, want -max_redirects 0", args)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/moved":
			http.Redirect(w, r, "/stream", http.StatusFound)
		case "/to-loopback":
			http.Redirect(w, r, "http://127.0.0.1:9/api/x", http.StatusFound)
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
	// Stock ST10/ST20 have no audio stage for bass: ffmpeg must not add one.
	if got := playbackAudioFilter(0.5, false); got != "volume=0.50" {
		t.Fatalf("filter = %q", got)
	}
}

func TestPlaybackAudioFilterFadesInNewStation(t *testing.T) {
	if got := playbackAudioFilter(1, true); got != "volume=1.00,afade=t=in:d=3:curve=cub" {
		t.Fatalf("fade-in filter = %q", got)
	}
}

func TestPlaybackFFmpegOverwritesFIFO(t *testing.T) {
	args := playbackFFmpegArgs("https://example.test/radio", "volume=1", "s16le", "48000", "/run/soundtap/source.pcm", false)
	if !slices.Contains(args, "-y") {
		t.Fatal("ffmpeg must overwrite the existing source FIFO")
	}
}

func TestPlaybackTuneInWritesRaw48K(t *testing.T) {
	args := playbackTuneInArgs("https://example.test/live", "volume=1", "s16le", "48000", "/run/soundtap/source.pcm", false)

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
	for _, args := range [][]string{
		playbackFFmpegArgs("https://example.test/radio", "volume=1", "s16le", "48000", "x", false),
		playbackTuneInArgs("https://example.test/live", "volume=1", "s16le", "48000", "x", false),
	} {
		i := slices.Index(args, "-i")
		if i < 2 || args[i-2] != "-protocol_whitelist" || strings.Contains(args[i-1], "file") {
			t.Errorf("args = %q, want -protocol_whitelist without file right before -i", args)
		}
	}
}

func TestDecoderStartupDeadline(t *testing.T) {
	if got := decoderStartupDeadline("ffmpeg"); got < 40*time.Second {
		t.Fatalf("ffmpeg startup deadline = %v, want at least 40s", got)
	}

	if got := decoderStartupDeadline("/usr/bin/ffmpeg"); got != bufferDeadline {
		t.Fatalf("TuneIn startup deadline = %v, want %v", got, bufferDeadline)
	}
}

func TestPlaybackOutputALSA(t *testing.T) {
	format, dev := playbackOutput("s16le", "alsa:plughw:0,0")
	if format != "alsa" || dev != "plughw:0,0" {
		t.Fatalf("playbackOutput = %q, %q; want alsa, plughw:0,0", format, dev)
	}
}

func TestSourcePreemptionTransitions(t *testing.T) {
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string

			p := supervise.NewProcesses()
			p.Register("AIRPLAY", func() { got = append(got, "AIRPLAY") }, nil)
			p.Register("SPOTIFY", func() { got = append(got, "SPOTIFY") }, nil)
			preemptSource(tt.previous, tt.next, func() { got = append(got, "decoder") }, p)

			if !slices.Equal(got, tt.want) {
				t.Fatalf("transition order = %v, want %v", got, tt.want)
			}
		})
	}
}

// The radio decoder is a stripped ffmpeg that only knows the filters its package
// enables; a filter missing there makes every station fail with "Filter not found".
func TestPlaybackAudioFilterOnlyUsesBuiltFilters(t *testing.T) {
	recipe, err := os.ReadFile("../../../os/package/soundtap-ffmpeg/soundtap-ffmpeg.mk")
	if err != nil {
		t.Fatal(err)
	}

	m := regexp.MustCompile(`(?m)^SOUNDTAP_FFMPEG_FILTERS = (.*)$`).FindSubmatch(recipe)
	if m == nil {
		t.Fatal("no SOUNDTAP_FFMPEG_FILTERS in soundtap-ffmpeg.mk")
	}

	built := strings.Fields(string(m[1]))

	for f := range strings.SplitSeq(playbackAudioFilter(1, true), ",") {
		name, _, _ := strings.Cut(f, "=")
		if !slices.Contains(built, name) {
			t.Errorf("filter %q is used but not built into ffmpeg (%s)", name, m[1])
		}
	}
}

// The cut-down ffmpeg only has the protocols its package lists; one the player relies
// on but that is not built fails every station with "Protocol not found".
func TestPlaybackInputOnlyUsesBuiltProtocols(t *testing.T) {
	recipe, err := os.ReadFile("../../../os/package/soundtap-ffmpeg/soundtap-ffmpeg.mk")
	if err != nil {
		t.Fatal(err)
	}

	m := regexp.MustCompile(`(?m)^SOUNDTAP_FFMPEG_PROTOCOLS = (.*)$`).FindSubmatch(recipe)
	if m == nil {
		t.Fatal("no SOUNDTAP_FFMPEG_PROTOCOLS in soundtap-ffmpeg.mk")
	}

	built := strings.Fields(string(m[1]))
	// crypto is whitelisted for AES HLS but need not be built: without it only that fails.
	need := []string{"http", "https", "tcp", "tls"}
	piped := playbackInput("", true)

	need = append(need, strings.Split(piped[slices.Index(piped, "-protocol_whitelist")+1], ",")...)
	for _, p := range need {
		if !slices.Contains(built, p) {
			t.Errorf("protocol %q is used but not built into ffmpeg (%s)", p, m[1])
		}
	}
}

// A stream soundtapd reads ahead arrives on stdin and ffmpeg may open nothing but pipe;
// an HLS manifest or playlist keeps its URL, the http whitelist and ffmpeg's reconnect.
func TestPlaybackInputPipedForDirectStreamsURLForHLS(t *testing.T) {
	const hls = "https://example.test/live/master.m3u8"

	piped := playbackTuneInArgs("https://example.test/live.mp3", "volume=1", "s16le", "48000", "x", true)

	i := slices.Index(piped, "-i")
	if i < 2 || piped[i+1] != "pipe:0" || piped[i-2] != "-protocol_whitelist" || piped[i-1] != "pipe" {
		t.Errorf("piped args = %q, want -protocol_whitelist pipe -i pipe:0", piped)
	}

	if slices.Contains(piped, "-reconnect") || slices.Contains(piped, "-rw_timeout") || slices.Contains(piped, "https://example.test/live.mp3") {
		t.Errorf("piped args = %q, must carry no URL and no http options", piped)
	}

	direct := playbackTuneInArgs(hls, "volume=1", "s16le", "48000", "x", false)

	i = slices.Index(direct, "-i")
	if i < 0 || direct[i+1] != hls || direct[i-1] != FFmpegProtocols || !slices.Contains(direct, "-reconnect") || !slices.Contains(direct, "-rw_timeout") {
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
	args := playbackFFmpegArgs("http://192.168.1.9/a.flac", "volume=1", "s16le", "48000", "-", false)
	if got := playbackSeek(args, 0); !slices.Equal(got, args) {
		t.Fatalf("at 0: %v", got)
	}

	got := strings.Join(playbackSeek(args, 90500*time.Millisecond), " ")
	if !strings.Contains(got, "-ss 90.500 -i http://192.168.1.9/a.flac") {
		t.Fatalf("args = %s", got)
	}
}
