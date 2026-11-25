// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package rootd_test

import (
	"bufio"
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/rootd"
)

// An accepted script touches only flash.EnvKeys, whatever its bytes, and no input panics.
func FuzzCheckEnvScript(f *testing.F) {
	for _, seed := range []string{
		"bootcmd=run nand_boot2;\nupgrade_available=1\n", "altbootcmd\n", "bootargs=init=/bin/sh\n",
		"\tbootcmd x\n", "bootcmd\x00\n", "# comment\nbootlimit=4\n", "=x\n", " \n",
	} {
		f.Add([]byte(seed))
	}

	allowed := flash.EnvKeys()

	f.Fuzz(func(t *testing.T, script []byte) {
		if rootd.CheckEnvScript(script) != nil {
			return
		}

		s := bufio.NewScanner(bytes.NewReader(script))
		s.Buffer(nil, rootd.MaxMessage)

		for s.Scan() {
			line := s.Text()
			if strings.Trim(line, " \t\r") == "" || strings.HasPrefix(line, "#") {
				continue
			}

			key := strings.TrimLeft(line, " \t")
			if i := strings.IndexAny(key, "= \t"); i >= 0 {
				key = key[:i]
			}

			if !slices.Contains(allowed, key) {
				t.Fatalf("accepted %q, which sets %q", script, key)
			}
		}
	})
}

// A host name the helper accepts is one DNS label of lowercase letters, digits and hyphens.
func FuzzCheckHostname(f *testing.F) {
	for _, seed := range []string{"kitchen", "-x", "a.b", "Kitchen", "x\n", ""} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, name string) {
		if rootd.CheckHostname(name) != nil {
			return
		}

		if name == "" || len(name) > 63 || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" ||
			name[0] == '-' || name[len(name)-1] == '-' {
			t.Fatalf("accepted %q", name)
		}
	})
}
