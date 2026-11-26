// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package player

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// FFmpegProtocols is the -protocol_whitelist for every ffmpeg input. A stream URL is
// already checked to be http(s) (upnp.MediaURL), but a playlist or HLS manifest behind it
// can name file:, concat: or another nested protocol, which ffmpeg would otherwise open
// (ADR 0041). crypto is AES-128 HLS.
const FFmpegProtocols = "http,https,tcp,tls,crypto"

// pipeInput is the ffmpeg input of a stream soundtapd reads ahead itself and feeds on stdin.
const pipeInput = "pipe:0"

// playbackInput is the ffmpeg input. A plain stream that soundtapd reads ahead itself
// (internal/readahead, ADR 0041) arrives on stdin (input pipeInput), so the only protocol
// ffmpeg may open is pipe and its own -reconnect/-rw_timeout (http options) do not apply.
// Everything else, HLS and playlists above all, keeps the URL and the http whitelist.
//
// No -reconnect_streamed: on a body without a length ffmpeg then reconnects at once, for
// as long as the server answers, which was 90 requests a second to a web page pasted as a
// radio URL. A stream that ends goes to the player's own retry, which backs off.
func playbackInput(input string) []string {
	if input == pipeInput {
		return []string{"-protocol_whitelist", "pipe", "-i", pipeInput}
	}

	return []string{
		"-reconnect", "1", "-reconnect_delay_max", "5", "-max_redirects", "0",
		"-rw_timeout", "15000000", "-protocol_whitelist", FFmpegProtocols, "-i", input,
	}
}

// FFmpegURL is url after its redirects, followed here under MediaURL's rules on every hop
// and dial, because ffmpeg (-max_redirects 0) follows none itself. When that
// fails the URL goes as it is: a redirect then stops in ffmpeg instead of reaching
// loopback. HLS is not covered: ffmpeg opens the variant playlists and segments a
// manifest names itself, on any host and with its default redirects, since
// -max_redirects is not passed on to those nested opens.
func FFmpegURL(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return url
	}

	resp, err := playlistClient.Do(req)
	if err != nil {
		return url
	}

	_ = resp.Body.Close()

	return resp.Request.URL.String()
}

// resolveRedirects is FFmpegURL for the player loop; its tests play addresses that do not
// answer.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var resolveRedirects = FFmpegURL

// playbackFFmpegArgs builds the decoder command for a network stream.
//
// The defaults are wrong for live radio in three expensive ways, all of which the
// user hears as "the station takes forever to start":
//   - avformat probes for analyzeduration (5 s) / probesize (5 MB) before emitting a
//     single sample. On a 128 kbps stream that is five seconds of wall clock.
//   - the output AVIO buffer (32 KiB) is not flushed per packet, so another ~170 ms
//     of audio is held back before the first byte reaches the bus.
//   - without -reconnect a momentary drop kills the process, which costs a full
//     respawn plus another station resolve.
//
// rw_timeout bounds a half-open TCP connection, which otherwise hangs forever with
// no exit for the supervisor to react to.
func playbackFFmpegArgs(input, audioFilter, format, rate, dev string) []string {
	args := []string{
		"-y", "-nostdin", "-loglevel", "warning",
		"-fflags", "nobuffer", "-flags", "low_delay",
		"-probesize", "32768", "-analyzeduration", "0",
	}
	args = append(args, playbackInput(input)...)

	return append(args, "-af", audioFilter,
		"-f", format, "-ar", rate, "-ac", "2", "-flush_packets", "1", dev)
}

// playbackSeek starts a UPnP file at its offset: -ss before -i seeks in the input,
// which ffmpeg does over HTTP with a range request.
func playbackSeek(args []string, offset time.Duration) []string {
	if offset <= 0 {
		return args
	}

	i := slices.Index(args, "-i")
	ss := strconv.FormatFloat(offset.Seconds(), 'f', 3, 64)

	return slices.Concat(args[:i], []string{"-ss", ss}, args[i:])
}

func playbackTuneInArgs(input, audioFilter, format, rate, dev string) []string {
	return playbackFFmpegArgs(input, audioFilter, format, rate, dev)
}

func playbackOutput(format, dev string) (string, string) {
	if target, ok := strings.CutPrefix(dev, "alsa:"); ok {
		return "alsa", target
	}

	return format, dev
}
