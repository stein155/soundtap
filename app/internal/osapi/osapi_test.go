// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package osapi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNeedIsTheEmbeddedLevel(t *testing.T) {
	if Need() < 1 {
		t.Fatalf("Need() = %d; the level file must hold a positive number", Need())
	}
}

func TestHave(t *testing.T) {
	dir := t.TempDir()

	write := func(name, body string) string {
		p := filepath.Join(dir, name)

		err := os.WriteFile(p, []byte(body), 0o644)
		if err != nil {
			t.Fatal(err)
		}

		return p
	}
	for _, tc := range []struct {
		path string
		want int
	}{
		{write("ok", "3\n"), 3},
		{write("spaces", "  7 "), 7},
		{write("garbage", "three"), 0},
		{write("empty", ""), 0},
		{filepath.Join(dir, "missing"), 0}, // an OS from before the contract
	} {
		if got := Have(tc.path); got != tc.want {
			t.Errorf("Have(%s) = %d, want %d", filepath.Base(tc.path), got, tc.want)
		}
	}
}
