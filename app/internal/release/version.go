// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package release

import (
	"cmp"
	"strconv"
	"strings"
)

// alphaSuffix opens the one prerelease a version may carry: MAJOR.MINOR.PATCH-alpha.N
// (ADR 0035).
const alphaSuffix = "-alpha."

// Version is a SoundTap version as os/VERSION, app/VERSION, a tag and a statement carry it
// (ADR 0035): MAJOR.MINOR.PATCH for a release, MAJOR.MINOR.PATCH-alpha.N for an alpha. Each
// of MAJOR, MINOR and PATCH is one to nine decimal digits; N is a positive number of at most
// nine digits without a leading zero. Nothing else is a version.
type Version struct {
	Major, Minor, Patch int
	Alpha               int // N of -alpha.N; 0 for a release
}

// ParseVersion reads v strictly; ok is false for anything that is not a Version.
func ParseVersion(v string) (Version, bool) {
	core, alpha, pre := strings.Cut(v, alphaSuffix)
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var n [3]int
	for i, p := range parts {
		if !digits(p) {
			return Version{}, false
		}
		n[i], _ = strconv.Atoi(p)
	}
	out := Version{Major: n[0], Minor: n[1], Patch: n[2]}
	if pre {
		if !digits(alpha) || alpha[0] == '0' {
			return Version{}, false
		}
		out.Alpha, _ = strconv.Atoi(alpha)
	}
	return out, true
}

// digits reports one to nine decimal digits.
func digits(s string) bool {
	return s != "" && len(s) <= 9 && strings.Trim(s, "0123456789") == ""
}

// ValidVersion reports whether v is a Version.
func ValidVersion(v string) bool {
	_, ok := ParseVersion(v)
	return ok
}

// Prerelease reports an alpha.
func (v Version) Prerelease() bool { return v.Alpha > 0 }

// Compare orders versions by semver precedence: -1, 0 or +1 as v is older than, the same as or
// newer than w. An alpha comes before the release of its number, so
// 0.1.0-alpha.1 < 0.1.0-alpha.2 < 0.1.0 < 0.1.1-alpha.1.
func (v Version) Compare(w Version) int {
	if c := cmp.Or(cmp.Compare(v.Major, w.Major), cmp.Compare(v.Minor, w.Minor),
		cmp.Compare(v.Patch, w.Patch)); c != 0 {
		return c
	}
	switch {
	case v.Alpha == w.Alpha:
		return 0
	case v.Alpha == 0: // the release after w's alphas
		return 1
	case w.Alpha == 0:
		return -1
	}
	return cmp.Compare(v.Alpha, w.Alpha)
}
