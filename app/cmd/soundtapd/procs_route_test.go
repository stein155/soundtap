// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasDefaultRoute(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write := func(body string) string {
		path := filepath.Join(dir, "route")

		err := os.WriteFile(path, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		return path
	}
	head := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"

	lan := "wlan0\t0058A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n"
	if hasDefaultRoute(write(head + lan)) {
		t.Error("a table with only the LAN route has a default route")
	}

	if !hasDefaultRoute(write(head + "wlan0\t00000000\t0158A8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" + lan)) {
		t.Error("the default route was missed")
	}

	if hasDefaultRoute(filepath.Join(dir, "missing")) || hasDefaultRoute(write("")) {
		t.Error("no table read as a default route")
	}
}
