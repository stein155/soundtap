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
	t.Parallel()

	img := make([]byte, bco.StockAppSize)
	img[0] = 0x5a // on no allowlist
	// Approvals last for the process: an image of its own per run, so a second run
	// (go test -count) does not find it approved already.
	_, err := rand.Read(img[1:17])
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(img)

	_, err = checkImage(img, nil, imageApproval{allow: "", yes: true})
	if err == nil {
		t.Error("an image on no allowlist was accepted")
	}

	_, err = checkImage(img, nil, imageApproval{allow: strings.Repeat("0", 64), yes: true})
	if err == nil {
		t.Error("an approval for another hash was accepted")
	}

	_, err = checkImage(img, nil, imageApproval{allow: hex.EncodeToString(sum[:]), yes: false})
	if err == nil {
		t.Error("an approved image went ahead without -yes")
	}

	_, err = checkImage(img, nil, imageApproval{allow: hex.EncodeToString(sum[:]), yes: true})
	if err != nil {
		t.Errorf("approved image with -yes: %v", err)
	}

	_, err = checkImage(img[:100], nil, imageApproval{allow: "", yes: true})
	if err == nil {
		t.Error("a truncated image was accepted")
	}

	own := append([]byte{0x6b}, img[1:]...) // on no allowlist, and approved nowhere above

	_, err = checkImage(own, append([]byte{1}, own[1:]...), imageApproval{allow: "", yes: true})
	if err == nil {
		t.Error("an image that differs from the OS's own was accepted")
	}

	_, err = checkImage(own, own, imageApproval{allow: "", yes: true})
	if err != nil {
		t.Errorf("the OS's own image: %v", err)
	}
}
