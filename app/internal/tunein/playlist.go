// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package tunein

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// maxPlaylist bounds a playlist read. A real one is a few lines.
const maxPlaylist = 64 << 10

// errPlaylistStatus is a playlist URL answering with a status other than 200.
var errPlaylistStatus = errors.New("HTTP")

// PlaylistStreams returns the streams behind a URL the user gave: the URL itself, or what
// a .pls or plain .m3u file lists, because that is what a station's site tends to offer and
// ffmpeg opens neither. An .m3u that lists nothing absolute is left to ffmpeg, which reads
// an HLS playlist under that name.
func PlaylistStreams(ctx context.Context, client *http.Client, raw string) ([]string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("playlist: %w", err)
	}

	ext := strings.ToLower(path.Ext(u.Path))
	if ext != ".pls" && ext != ".m3u" {
		return []string{raw}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("playlist: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("playlist: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("playlist %s: %w %d", raw, errPlaylistStatus, resp.StatusCode)
	}

	listed, err := playlistEntries(io.LimitReader(resp.Body, maxPlaylist))
	if err != nil {
		return nil, err
	}

	if len(listed) == 0 {
		return []string{raw}, nil
	}

	return listed, nil
}

// playlistEntries reads the absolute URLs of a playlist: `File1=http://...` in a .pls, the
// bare line in an .m3u.
func playlistEntries(r io.Reader) ([]string, error) {
	var urls []string

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if key, value, ok := strings.Cut(line, "="); ok && strings.HasPrefix(strings.ToLower(key), "file") {
			line = strings.TrimSpace(value)
		}

		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			urls = append(urls, line)
		}
	}

	err := scanner.Err()
	if err != nil {
		return urls, fmt.Errorf("playlist: read: %w", err)
	}

	return urls, nil
}
