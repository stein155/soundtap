// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package icy reads "now playing" metadata straight from a Shoutcast/Icecast
// audio stream. Almost every internet-radio stream interleaves a small text
// block (the ICY protocol) carrying the current StreamTitle — usually
// "Artist - Title" — so this is the one station-agnostic way to know what is on
// air without a per-broadcaster API. It carries no cover art (title text only);
// see internal/artwork for that.
package icy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/stein155/soundtap/app/internal/safehttp"
)

// maxMetaInt bounds how many audio bytes we are willing to read before a
// metadata block, so a stream advertising an absurd icy-metaint can't make us
// download megabytes. Real streams use 1k–64k.
const maxMetaInt = 512 << 10 // 512 KiB

// maxBlocks caps how many metadata blocks we read while waiting for a non-empty
// title. Icecast sends the current title in the first block after connect, so
// one is normally enough; a couple more cover the rare empty-first-block case.
const maxBlocks = 3

// metaUnit is the size of the metadata length byte's unit: the block is that many
// 16-byte units long.
const metaUnit = 16

// ErrStatus is returned when the stream answers with a status other than 200.
var ErrStatus = errors.New("icy: status")

// StreamTitle opens streamURL with ICY metadata enabled and returns the first
// non-empty StreamTitle it sees (e.g. "Artist - Title"). It returns "" with a
// nil error when the stream carries no metadata — that is an expected outcome,
// not a failure. client must follow redirects (streamtheworld and friends 302 to
// a real edge); http.DefaultClient does.
func StreamTitle(ctx context.Context, client *http.Client, streamURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("icy: build request: %w", err)
	}

	req.Header.Set("Icy-Metadata", "1")
	req.Header.Set("User-Agent", "SoundTap/1.0")

	resp, err := safehttp.Do(ctx, client, req)
	if err != nil {
		return "", fmt.Errorf("icy: open stream: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w %d", ErrStatus, resp.StatusCode)
	}

	metaint, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Icy-Metaint")))
	if err != nil || metaint <= 0 || metaint > maxMetaInt {
		return "", nil // stream does not interleave metadata
	}

	var lenBuf [1]byte

	for range maxBlocks {
		// Skip the audio bytes up to the next metadata block.
		_, err = io.CopyN(io.Discard, resp.Body, int64(metaint))
		if err != nil {
			return "", fmt.Errorf("icy: skip audio: %w", err)
		}
		// One length byte in 16-byte units, then that many bytes of metadata.
		_, err = io.ReadFull(resp.Body, lenBuf[:])
		if err != nil {
			return "", fmt.Errorf("icy: read metadata length: %w", err)
		}

		size := int(lenBuf[0]) * metaUnit
		if size == 0 {
			continue // no metadata in this block; try the next
		}

		meta := make([]byte, size)

		_, err = io.ReadFull(resp.Body, meta)
		if err != nil {
			return "", fmt.Errorf("icy: read metadata: %w", err)
		}

		if title := parseStreamTitle(string(meta)); title != "" {
			return title, nil
		}
	}

	return "", nil
}

// parseStreamTitle pulls the value out of a metadata block that looks like
// "StreamTitle='Artist - Title';StreamUrl='...';". Returns "" when absent.
func parseStreamTitle(meta string) string {
	const key = "StreamTitle='"

	_, after, ok := strings.Cut(meta, key)
	if !ok {
		return ""
	}

	rest := after
	// The value ends at the closing "';"; fall back to the last "'" for the last
	// field, which some servers don't terminate with a semicolon. The last one, not
	// the first: "Sweet Child O' Mine" has its own.
	end := strings.Index(rest, "';")
	if end < 0 {
		end = strings.LastIndexByte(strings.TrimRight(rest, "\x00"), '\'')
	}

	if end < 0 {
		return ""
	}

	title := strings.TrimSpace(decodeText(rest[:end]))
	// Qmusic marks ad breaks with StreamTitle='commercial-in'; that is not a track.
	if !strings.Contains(title, " - ") && strings.HasPrefix(strings.ToLower(title), "commercial") {
		return ""
	}

	return title
}

// decodeText normalises a StreamTitle value to UTF-8. The ICY spec nominally
// wants UTF-8, but many stations send the block in Windows-1252 (or plain
// Latin-1), so a byte like 0xE9 ("é") would otherwise be invalid UTF-8 and
// render as the replacement character. Valid UTF-8 is returned untouched;
// anything else is decoded byte-for-byte through Windows-1252.
func decodeText(text string) string {
	if utf8.ValidString(text) {
		return text
	}

	var out strings.Builder
	out.Grow(len(text))

	for index := range len(text) {
		if r, ok := win1252[text[index]]; ok {
			out.WriteRune(r)
		} else {
			// Bytes outside the 0x80–0x9F block map straight to the matching
			// Unicode code point (Latin-1 == the low Unicode range).
			out.WriteRune(rune(text[index]))
		}
	}

	return out.String()
}

// win1252 holds the Windows-1252 code points that differ from Latin-1, all in
// the 0x80–0x9F range (curly quotes, dashes, the euro sign, and friends).
//
//nolint:gochecknoglobals // a read-only lookup table: the code page itself, built once
var win1252 = map[byte]rune{
	0x80: '€', 0x82: '‚', 0x83: 'ƒ', 0x84: '„', 0x85: '…', 0x86: '†', 0x87: '‡',
	0x88: 'ˆ', 0x89: '‰', 0x8A: 'Š', 0x8B: '‹', 0x8C: 'Œ', 0x8E: 'Ž', 0x91: '‘',
	0x92: '’', 0x93: '“', 0x94: '”', 0x95: '•', 0x96: '–', 0x97: '—', 0x98: '˜',
	0x99: '™', 0x9A: 'š', 0x9B: '›', 0x9C: 'œ', 0x9E: 'ž', 0x9F: 'Ÿ',
}

// SplitArtistTitle splits a StreamTitle on the first " - " into artist and
// title, the near-universal convention. When there is no separator the whole
// string is the title and artist is empty.
//
// Qmusic sends "Title - ARTIST" and TuneIn has nothing to correct it with, so
// an all-caps second half after a mixed-case first half is read as the artist.
// guesses wrong on "Artist - CAPS TITLE" (Kendrick Lamar - HUMBLE.);
// a per-station order is the upgrade if that shows up.
func SplitArtistTitle(streamTitle string) (string, string) {
	trimmed := strings.TrimSpace(streamTitle)

	before, after, ok := strings.Cut(trimmed, " - ")
	if !ok {
		return "", trimmed
	}

	first, second := strings.TrimSpace(before), strings.TrimSpace(after)
	if shouting(second) && first != strings.ToUpper(first) {
		return second, first
	}

	return first, second
}

// shouting reports whether text has a word in capitals and no lower case. Two letters in a
// row make the word: an initialism like "S.O.S." is a song title as often as an artist.
func shouting(text string) bool {
	if text != strings.ToUpper(text) {
		return false
	}

	prev := false

	for _, r := range text {
		letter := unicode.IsLetter(r)
		if letter && prev {
			return true
		}

		prev = letter
	}

	return false
}
