// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package spotify

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// ---- IDs ----------------------------------------------------------------------------

const b62 = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

const (
	// b62Base is the base of a Spotify id: one digit per character of b62.
	b62Base = int64(len(b62))
	// b62Len is how many digits a Spotify id has.
	b62Len = 22
	// gidBase is the base a gid is written in: hexadecimal.
	gidBase = 16
)

// hexID turns a base62 id into the 32-digit hex gid the metadata endpoints use.
func hexID(id string) string {
	gid := new(big.Int)
	for _, digit := range id {
		gid.Mul(gid, big.NewInt(b62Base))
		gid.Add(gid, big.NewInt(int64(strings.IndexRune(b62, digit))))
	}

	return fmt.Sprintf("%032x", gid)
}

// b62ID is the inverse of hexID.
func b62ID(gid string) string {
	value, ok := new(big.Int).SetString(gid, gidBase)
	if !ok {
		return ""
	}

	out := make([]byte, b62Len)

	base, mod := big.NewInt(b62Base), new(big.Int)
	for i := b62Len - 1; i >= 0; i-- {
		value.DivMod(value, base, mod)
		out[i] = b62[mod.Int64()]
	}

	return string(out)
}

func uriOf(kind, gid string) string {
	if id := b62ID(gid); id != "" {
		return "spotify:" + kind + ":" + id
	}

	return ""
}

// imageID matches the i.scdn.co image ids the proxy is allowed to fetch.
var imageID = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ImageURL is where the proxy fetches an image id from.
func ImageURL(id string) (string, bool) {
	if !imageID.MatchString(id) {
		return "", false
	}

	return "https://i.scdn.co/image/" + id, true
}

func artworkPath(id string) string {
	if !imageID.MatchString(id) {
		return ""
	}

	return "/api/spotify/images/" + id
}

// artworkFromURL accepts only i.scdn.co image URLs; mosaics and anything else are dropped.
func artworkFromURL(imageURL string) string {
	const prefix = "https://i.scdn.co/image/"
	if !strings.HasPrefix(imageURL, prefix) {
		return ""
	}

	return artworkPath(strings.TrimPrefix(imageURL, prefix))
}
