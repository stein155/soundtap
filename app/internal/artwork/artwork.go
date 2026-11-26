// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package artwork looks up cover art for a track via the public iTunes Search
// API. It is deliberately station-agnostic: given an "artist title" string
// (e.g. from an ICY StreamTitle) it returns an album-cover URL, so SoundTap can
// show cover art for any station without a per-broadcaster integration. No key,
// no account.
package artwork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/stein155/soundtap/app/internal/safehttp"
)

// base is the iTunes Search API root.
const base = "https://itunes.apple.com"

// maxBody caps how much of a search answer is read: one result is a few KiB.
const maxBody = 256 << 10

// ErrStatus is returned when the search API answers with a status other than 200.
var ErrStatus = errors.New("artwork: status")

// searchResult is the part of the iTunes Search answer this package reads.
type searchResult struct {
	Results []searchHit `json:"results"`
}

// searchHit is one result of a search.
type searchHit struct {
	ArtworkURL100 string `json:"artworkUrl100"`
}

// Search returns a cover-art URL for the given "artist title" term, or "" (nil
// error) when nothing matches — an expected outcome, not a failure. The image
// is requested at 600×600; iTunes serves any size on the same path.
func Search(ctx context.Context, client *http.Client, term string) (string, error) {
	return searchAt(ctx, client, base, term)
}

// searchAt is Search against the API root root; tests point it at a local server.
func searchAt(ctx context.Context, client *http.Client, root, term string) (string, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return "", nil
	}

	u := fmt.Sprintf("%s/search?media=music&entity=song&limit=1&term=%s", root, url.QueryEscape(term))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("artwork: build request: %w", err)
	}

	req.Header.Set("User-Agent", "SoundTap/1.0")

	resp, err := safehttp.Do(ctx, client, req)
	if err != nil {
		return "", fmt.Errorf("artwork: search: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w %d", ErrStatus, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", fmt.Errorf("artwork: read answer: %w", err)
	}

	var result searchResult

	err = json.Unmarshal(body, &result)
	if err != nil || len(result.Results) == 0 {
		return "", nil
	}

	art := result.Results[0].ArtworkURL100
	if art == "" {
		return "", nil
	}
	// The API returns a 100×100 thumbnail; the same path serves larger sizes.
	return strings.Replace(art, "100x100", "600x600", 1), nil
}
