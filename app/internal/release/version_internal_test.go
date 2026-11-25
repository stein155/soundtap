// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package release

import (
	"crypto/sha256"
	"testing"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	for text, want := range map[string]Version{
		"0.1.0":                         {0, 1, 0, 0},
		"10.20.30":                      {10, 20, 30, 0},
		"0.1.0-alpha.1":                 {0, 1, 0, 1},
		"0.1.0-alpha.12":                {0, 1, 0, 12},
		"1.2.3-alpha.999999999":         {1, 2, 3, 999999999},
		"123456789.0.999999999":         {123456789, 0, 999999999, 0},
		"001.0.0":                       {1, 0, 0, 0}, // as os/VERSION always allowed
		"0.0.0-alpha.100000000":         {0, 0, 0, 100000000},
		"999999999.999999999.0-alpha.9": {999999999, 999999999, 0, 9},
	} {
		got, ok := ParseVersion(text)
		if !ok || got != want || !ValidVersion(text) {
			t.Errorf("ParseVersion(%q) = %+v, %v; want %+v", text, got, ok, want)
		}
	}

	for _, invalid := range []string{
		"", "0.1", "0.1.0.0", "v0.1.0", "0.1.0-alpha", "0.1.0-alpha.", "0.1.0-alpha.0",
		"0.1.0-alpha.01", "0.1.0-alpha.1000000000", "0.1.0-beta.1", "0.1.0-rc.1", "0.1.0-ALPHA.1",
		"0.1.0-alpha.1.2", "0.1.0-alpha.1-alpha.2", "0.1.0+build", "0.1.0-alpha.+1", "+1.0.0",
		"1000000000.0.0", "0.1-alpha.1.0", "-alpha.1", "0.1.0 ", " 0.1.0", "stock",
	} {
		if _, ok := ParseVersion(invalid); ok || ValidVersion(invalid) {
			t.Errorf("%q parsed as a version", invalid)
		}
	}
}

// Semver precedence: the alphas of a version come before it, in order of N.
func TestVersionCompare(t *testing.T) {
	t.Parallel()

	order := []string{
		"0.0.9", "0.1.0-alpha.1", "0.1.0-alpha.2", "0.1.0-alpha.10", "0.1.0",
		"0.1.1-alpha.1", "0.1.1", "0.2.0", "1.0.0-alpha.1", "1.0.0", "10.0.0",
	}
	for firstRank, first := range order {
		firstVersion, _ := ParseVersion(first)
		for secondRank, second := range order {
			secondVersion, _ := ParseVersion(second)

			want := 0
			if firstRank < secondRank {
				want = -1
			} else if firstRank > secondRank {
				want = 1
			}

			if got := firstVersion.Compare(secondVersion); got != want {
				t.Errorf("%s vs %s = %d, want %d", first, second, got, want)
			}
		}
	}

	if v, _ := ParseVersion("0.1.0-alpha.1"); !v.Prerelease() {
		t.Error("0.1.0-alpha.1 is not a prerelease")
	}

	if v, _ := ParseVersion("0.1.0"); v.Prerelease() {
		t.Error("0.1.0 is a prerelease")
	}
}

// An alpha's statement signs and verifies like a release's, its version intact.
func TestAlphaStatement(t *testing.T) {
	t.Parallel()

	key, priv := testKey(t)
	file := sha256.Sum256([]byte("file"))
	s := Statement{Kind: KindCore, Profile: ProfileProduction, Version: "0.1.0-alpha.1", Digest: file[:]}

	got, err := Verify([]string{key}, mustSign(t, priv, s), KindCore)
	if err != nil || got.Version != "0.1.0-alpha.1" {
		t.Fatalf("Verify = %+v, %v", got, err)
	}
}
