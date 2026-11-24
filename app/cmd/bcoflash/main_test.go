// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/bco"
)

func TestCheckImage(t *testing.T) {
	img := make([]byte, bco.StockAppSize)
	img[0] = 0x5a // on no allowlist
	// Approvals last for the process: an image of its own per run, so a second run
	// (go test -count) does not find it approved already.
	if _, err := rand.Read(img[1:17]); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(img)
	if _, err := checkImage(img, nil, "", true); err == nil {
		t.Error("an image on no allowlist was accepted")
	}
	if _, err := checkImage(img, nil, strings.Repeat("0", 64), true); err == nil {
		t.Error("an approval for another hash was accepted")
	}
	if _, err := checkImage(img, nil, hex.EncodeToString(sum[:]), false); err == nil {
		t.Error("an approved image went ahead without -yes")
	}
	if _, err := checkImage(img, nil, hex.EncodeToString(sum[:]), true); err != nil {
		t.Errorf("approved image with -yes: %v", err)
	}
	if _, err := checkImage(img[:100], nil, "", true); err == nil {
		t.Error("a truncated image was accepted")
	}
	own := append([]byte{0x6b}, img[1:]...) // on no allowlist, and approved nowhere above
	if _, err := checkImage(own, append([]byte{1}, own[1:]...), "", true); err == nil {
		t.Error("an image that differs from the OS's own was accepted")
	}
	if _, err := checkImage(own, own, "", true); err != nil {
		t.Errorf("the OS's own image: %v", err)
	}
}
