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

// hexID turns a base62 id into the 32-digit hex gid the metadata endpoints use.
func hexID(id string) string {
	n := new(big.Int)
	for _, c := range id {
		n.Mul(n, big.NewInt(62))
		n.Add(n, big.NewInt(int64(strings.IndexRune(b62, c))))
	}

	return fmt.Sprintf("%032x", n)
}

// b62ID is the inverse of hexID.
func b62ID(gid string) string {
	n, ok := new(big.Int).SetString(gid, 16)
	if !ok {
		return ""
	}

	out := make([]byte, 22)

	base, mod := big.NewInt(62), new(big.Int)
	for i := 21; i >= 0; i-- {
		n.DivMod(n, base, mod)
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
func artworkFromURL(u string) string {
	const prefix = "https://i.scdn.co/image/"
	if !strings.HasPrefix(u, prefix) {
		return ""
	}

	return artworkPath(strings.TrimPrefix(u, prefix))
}
