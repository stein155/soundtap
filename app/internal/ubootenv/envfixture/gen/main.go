// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Command gen writes the fixture environment to stdout, for scripts that need it as a file.
package main

import (
	"os"

	"github.com/stein155/soundtap/app/internal/ubootenv/envfixture"
)

func main() { _, _ = os.Stdout.Write(envfixture.Image()) }
