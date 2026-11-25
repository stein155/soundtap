// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package osapi is the contract between SoundTap Core and the SoundTap app (ADR 0035). The Core
// image ships Path, holding the level of what it provides: driver netdev names, script
// interfaces, paths. The app records the level it was built against. Both come from the
// level file next to this one, so a Core and an app from the same commit always match.
// Raise it in the commit that changes something the app relies on.
package osapi

import (
	_ "embed"
	"os"
	"strconv"
	"strings"
)

// Path is where the Core image keeps the level it provides.
const Path = "/etc/soundtap/os-api"

//go:embed level
var level string

// Need is the Core level this build of the app needs.
func Need() int { return parse(level) }

// Have is the level the Core at path provides; 0 for a Core from before the contract.
func Have(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	return parse(string(b))
}

func parse(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}

	return n
}
