// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web //nolint:testpackage // needs the server internals, and ADR 0120 names this file

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// multiroom-server lives in this package, so its part of the /mnt/nv fixture sets is tested
// here; app/cmd/soundtapd/nvstate_test.go has the rest (ADR 0120).
func TestNVMultiroomServerFixtures(t *testing.T) {
	t.Parallel()

	const host, deviceID = "192.168.1.20", "P12345678"

	srv := new(Server)
	srv.homeDir, srv.log = t.TempDir(), quiet()

	err := srv.setFollow(host, deviceID)
	if err != nil {
		t.Fatal(err)
	}

	written, _ := os.ReadFile(srv.multiroomServerPath())

	sets, _ := filepath.Glob("../../testdata/nv-*/soundtap")
	if len(sets) == 0 {
		t.Fatal("no fixture sets")
	}

	for idx, home := range sets {
		b, err := os.ReadFile(filepath.Join(home, "multiroom-server"))
		if err != nil {
			t.Fatal(err)
		}
		// The last set in name order is the newest: what this build writes (nvCurrent there).
		if idx == len(sets)-1 && !bytes.Equal(b, written) {
			t.Errorf("%s: this build writes %q; a format change needs a migration (ADR 0120)", home, written)
		}

		r := new(Server)

		r.homeDir, r.log = home, quiet()
		if h, d := r.Follow(); h != host || d != deviceID {
			t.Errorf("%s: follows %q/%q, want %q/%q", home, h, d, host, deviceID)
		}
	}
}
