// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package osapi_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/osapi"
)

func TestNeedIsTheEmbeddedLevel(t *testing.T) {
	t.Parallel()

	if osapi.Need() < 1 {
		t.Fatalf("Need() = %d; the level file must hold a positive number", osapi.Need())
	}
}

func TestHave(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write := func(name, body string) string {
		path := filepath.Join(dir, name)

		err := os.WriteFile(path, []byte(body), 0o644)
		if err != nil {
			t.Fatal(err)
		}

		return path
	}
	for _, check := range []struct {
		path string
		want int
	}{
		{write("ok", "3\n"), 3},
		{write("spaces", "  7 "), 7},
		{write("garbage", "three"), 0},
		{write("empty", ""), 0},
		{filepath.Join(dir, "missing"), 0}, // an OS from before the contract
	} {
		if got := osapi.Have(check.path); got != check.want {
			t.Errorf("Have(%s) = %d, want %d", filepath.Base(check.path), got, check.want)
		}
	}
}
