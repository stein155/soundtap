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

// versionParts is MAJOR, MINOR and PATCH.
const versionParts = 3

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
	none := Version{Major: 0, Minor: 0, Patch: 0, Alpha: 0}
	core, alpha, pre := strings.Cut(v, alphaSuffix)

	parts := strings.Split(core, ".")
	if len(parts) != versionParts {
		return none, false
	}

	var numbers [versionParts]int

	for i, part := range parts {
		if !digits(part) {
			return none, false
		}

		numbers[i], _ = strconv.Atoi(part)
	}

	out := Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2], Alpha: 0}

	if pre {
		if !digits(alpha) || alpha[0] == '0' {
			return none, false
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
// newer than other. An alpha comes before the release of its number, so
// 0.1.0-alpha.1 < 0.1.0-alpha.2 < 0.1.0 < 0.1.1-alpha.1.
func (v Version) Compare(other Version) int {
	byNumber := cmp.Or(cmp.Compare(v.Major, other.Major), cmp.Compare(v.Minor, other.Minor),
		cmp.Compare(v.Patch, other.Patch))
	if byNumber != 0 {
		return byNumber
	}

	switch {
	case v.Alpha == other.Alpha:
		return 0
	case v.Alpha == 0: // the release after other's alphas
		return 1
	case other.Alpha == 0:
		return -1
	}

	return cmp.Compare(v.Alpha, other.Alpha)
}
