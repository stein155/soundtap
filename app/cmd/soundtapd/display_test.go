// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/wifi"
)

// The panel's connecting screen names the saved network, and nothing when there is none.
func TestNetworkNameForTheConnectingScreen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "wifi.conf")
	name := networkName(path)

	if got := name(); got != "" {
		t.Fatalf("no wifi.conf: %q, want nothing", got)
	}

	err := wifi.WriteConf(path, wifi.NewRecord("Thuis", "", "a secret passphrase"))
	if err != nil {
		t.Fatal(err)
	}

	if got := name(); got != "Thuis" {
		t.Fatalf("saved network: %q, want Thuis", got)
	}
}
