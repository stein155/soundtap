// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package ubootenv

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/ubootenv/envfixture"
)

// The pre-install environment window (see envfixture). It is the format
// specification: anything this package does must survive a round trip through it
// unchanged, CRC included.
func factoryEnv(t *testing.T) []byte {
	t.Helper()

	return envfixture.Image()
}

// Re-encoding an untouched environment must reproduce it byte for byte. This is
// what proves the CRC window, the entry order and the padding byte are all right
// — get any of the three wrong and U-Boot silently falls back to its built-in
// default and boots PING as though nothing had been asked.
func TestReencodingTheFactoryEnvironmentChangesNothing(t *testing.T) {
	t.Parallel()

	factory := factoryEnv(t)

	env, err := ParseLegacy(factory)
	if err != nil {
		t.Fatal(err)
	}

	out, err := env.EncodeLegacy()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(out, factory) {
		t.Fatal("re-encoding the factory environment did not reproduce it")
	}
}

// The selector this whole round trip turns on, read off the board: bootcmd runs
// nand_boot for PING and nand_boot2 for PONG, and those run nand_args/nand_args2,
// which carry ubi.mtd=8 and ubi.mtd=9.
func TestFactoryEnvironmentSelectsPing(t *testing.T) {
	t.Parallel()

	env, err := ParseLegacy(factoryEnv(t))
	if err != nil {
		t.Fatal(err)
	}

	for key, want := range map[string]string{
		"bootcmd":    "run nand_boot;",
		"nand_args":  "ubi.mtd=8,2048",
		"nand_args2": "ubi.mtd=9,2048",
	} {
		got, ok := env.Get(key)
		if !ok {
			t.Fatalf("%s is missing", key)
		}

		if !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", key, got, want)
		}
	}
}

func TestSetReplacesInPlaceAndStaysValid(t *testing.T) {
	t.Parallel()

	env, err := ParseLegacy(factoryEnv(t))
	if err != nil {
		t.Fatal(err)
	}

	env.Set("bootcmd", "run nand_boot2;")

	out, err := env.EncodeLegacy()
	if err != nil {
		t.Fatal(err)
	}

	again, err := ParseLegacy(out)
	if err != nil {
		t.Fatalf("a rewritten environment must parse: %v", err)
	}

	if got, _ := again.Get("bootcmd"); got != "run nand_boot2;" {
		t.Errorf("bootcmd = %q", got)
	}

	if got, _ := again.Get("nand_args2"); !strings.Contains(got, "ubi.mtd=9,2048") {
		t.Errorf("rewriting bootcmd damaged nand_args2: %q", got)
	}
}

// A corrupt environment must be refused, not rebuilt on top of the damage: U-Boot
// still falls back to a working built-in default, and overwriting it takes that
// away.
func TestCorruptEnvironmentIsRefused(t *testing.T) {
	t.Parallel()

	damaged := append([]byte(nil), factoryEnv(t)...)

	damaged[200] ^= 0xff

	_, err := ParseLegacy(damaged)
	if !errors.Is(err, ErrBadCRC) {
		t.Fatalf("damaged environment error = %v, want ErrBadCRC", err)
	}
}

func TestShortDumpIsRefused(t *testing.T) {
	t.Parallel()

	_, err := ParseLegacy(factoryEnv(t)[:LegacySize-1])
	if err == nil {
		t.Fatal("a truncated dump was accepted")
	}
}

func TestOversizedEnvironmentIsRefused(t *testing.T) {
	t.Parallel()

	env, err := ParseLegacy(factoryEnv(t))
	if err != nil {
		t.Fatal(err)
	}

	env.Set("huge", strings.Repeat("x", LegacySize))

	_, err = env.EncodeLegacy()
	if err == nil {
		t.Fatal("an environment larger than the window was encoded")
	}
}

// fw_setenv -s is how soundtapd writes the environment; the script has to turn the
// environment fw_printenv showed into exactly the one the edit asked for.
func TestScriptRoundTripsThroughFwPrintenvText(t *testing.T) {
	t.Parallel()

	image, err := ParseLegacy(envfixture.Image())
	if err != nil {
		t.Fatal(err)
	}

	before, err := ParseText(image.Text())
	if err != nil || !before.Equal(image) {
		t.Fatalf("fw_printenv text does not parse back: %v", err)
	}

	after := before.Clone()
	after.Set("bootcmd", "run nand_boot2")
	after.Set("rt_try", "1")
	after.Delete("nand_boot")

	script, err := Script(before, after)
	if err != nil {
		t.Fatal(err)
	}

	got := before.Clone()
	got.Apply(script)

	if !got.Equal(after) {
		t.Fatalf("script %q did not produce the edit", script)
	}

	if empty, _ := Script(after, after.Clone()); len(empty) != 0 {
		t.Fatalf("unchanged environment produced script %q", empty)
	}

	for _, bad := range []string{"", "a\nb", strings.Repeat("x", scriptLine)} {
		broken := before.Clone()
		broken.Set("k", bad)

		_, err := Script(before, broken)
		if err == nil {
			t.Errorf("Script accepted k=%q", bad)
		}
	}
}

// The redundant environment: each copy round-trips, and the copy U-Boot boots is the one
// that checks out, of two the one with the newer flag, 0 after 255.
func TestTheCopyUBootBootsFrom(t *testing.T) {
	t.Parallel()

	env, err := ParseLegacy(factoryEnv(t))
	if err != nil {
		t.Fatal(err)
	}

	other := env.Clone()
	other.Set("bootcmd", "run nand_boot2;")

	copyOf := func(e *Env, flag byte) []byte {
		image, encodeErr := e.EncodeCopy(flag)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}

		return image
	}
	torn := copyOf(env, 9)

	torn[300] ^= 0xff
	for _, test := range []struct {
		name               string
		primary, redundant []byte
		want               int
		flag               byte
	}{
		{
			"a fresh pair boots the primary",
			copyOf(env, FreshPrimaryFlag), copyOf(other, FreshRedundantFlag), Primary, 2,
		},
		{"the newer flag wins", copyOf(env, 4), copyOf(other, 5), Redundant, 5},
		{"0 comes after 255", copyOf(env, 255), copyOf(other, 0), Redundant, 0},
		{"255 comes before 0", copyOf(env, 0), copyOf(other, 255), Primary, 0},
		{"equal flags keep the primary", copyOf(env, 7), copyOf(other, 7), Primary, 7},
		{"a torn copy loses", torn, copyOf(other, 1), Redundant, 1},
		{"the single copy is not a copy", append(factoryEnv(t), make([]byte, Size)...), copyOf(other, 1), Redundant, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, which, flag, currentErr := Current(test.primary, test.redundant)
			if currentErr != nil || which != test.want || flag != test.flag {
				t.Fatalf("copy %d flag %d err %v, want copy %d flag %d", which, flag, currentErr, test.want, test.flag)
			}

			want := env
			if test.want == Redundant {
				want = other
			}

			if !got.Equal(want) {
				t.Error("the environment is not that copy's")
			}
		})
	}

	none, _, _, err := Current(torn, torn)
	if none != nil || !errors.Is(err, ErrBadCRC) {
		t.Errorf("two torn copies: %v, want ErrBadCRC", err)
	}

	big := env.Clone()
	big.Set("owner_key", strings.Repeat("k", Size))

	_, err = big.EncodeCopy(1)
	if err == nil {
		t.Error("a copy larger than 32 KiB was encoded")
	}
}

// fw_printenv prints a value holding a newline over several lines: a line that does not
// start with a key continues the value before it, as one that does starts the next entry.
func TestParseTextContinuesAMultiLineValue(t *testing.T) {
	t.Parallel()

	text := "bootcmd=run a\nboard_serial=\xb8(b\f\n\xb2%\xc1\n\n:\xd4=\xf1 x\nbootcount=1\n"

	env, err := ParseText([]byte(text))
	if err != nil {
		t.Fatal(err)
	}

	if v, _ := env.Get("board_serial"); v != "\xb8(b\f\n\xb2%\xc1\n\n:\xd4=\xf1 x" {
		t.Errorf("board_serial = %q", v)
	}

	if v, _ := env.Get("bootcount"); v != "1" || len(env.Keys()) != 3 {
		t.Errorf("bootcount = %q, keys %q", v, env.Keys())
	}

	back, err := ParseText(env.Text())
	if err != nil || !back.Equal(env) {
		t.Errorf("round trip: %v", err)
	}

	_, err = ParseText([]byte("\xb8(b\nbootcmd=run a\n"))
	if err == nil {
		t.Error("a first line that is no entry was read")
	}
}
