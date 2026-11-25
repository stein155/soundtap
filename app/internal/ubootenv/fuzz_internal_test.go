// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package ubootenv

import (
	"hash/crc32"
	"testing"
)

// The environment comes off NAND, where a torn write or a foreign bootloader leaves
// anything, and as fw_printenv's text. `go test` runs the seeds; `go test -fuzz=FuzzX
// ./internal/ubootenv` searches further.

// window lays entries out as one redundant copy with a CRC that holds, so the fuzzer gets
// past the CRC to the entries.
func window(flag byte, entries []byte) []byte {
	body := make([]byte, Size-headerSize)
	copy(body, entries)

	return append(append(le32(crc32.ChecksumIEEE(body)), flag), body...)
}

// FuzzParseCopy: a copy that parses re-encodes to one that parses to the same entries.
func FuzzParseCopy(f *testing.F) {
	f.Add(byte(1), []byte("bootcmd=run slot_a\x00bootcount=0\x00\x00"))
	f.Add(byte(2), []byte("a=1\x00a=2\x00\x00"))
	f.Add(byte(0), []byte("=x\x00\x00"))
	f.Add(byte(7), []byte("k=\xff\x00\xffjunk"))
	f.Add(byte(0), []byte{})
	f.Fuzz(func(t *testing.T, flag byte, entries []byte) {
		for _, image := range [][]byte{window(flag, entries), append([]byte{1, 2, 3, 4, flag}, entries...)} {
			env, got, err := ParseCopy(image)
			if err != nil {
				continue
			}

			if got != flag && len(image) >= Size && image[crcSize] == flag {
				t.Fatalf("flag %d read as %d", flag, got)
			}

			again, err := env.EncodeCopy(got)
			if err != nil {
				continue // more than the window holds once sorted and terminated
			}

			back, flag2, err := ParseCopy(again)
			if err != nil || flag2 != got || !back.Equal(env) {
				t.Fatalf("round trip lost entries: %v %v", err, env.Text())
			}
		}
	})
}

// FuzzParseLegacy: the single 8 KiB copy, the same way.
func FuzzParseLegacy(f *testing.F) {
	f.Add([]byte("bootcmd=nboot\x00bootdelay=0\x00\x00"))
	f.Add([]byte("noequals\x00\x00"))
	f.Fuzz(func(t *testing.T, entries []byte) {
		body := make([]byte, LegacySize-crcSize)
		copy(body, entries)

		env, err := ParseLegacy(append(le32(crc32.ChecksumIEEE(body)), body...))
		if err != nil {
			return
		}

		again, err := env.EncodeLegacy()
		if err != nil {
			return
		}

		back, err := ParseLegacy(again)
		if err != nil || !back.Equal(env) {
			t.Fatalf("round trip lost entries: %v", err)
		}
	})
}

// FuzzParseText: fw_printenv's text reads back the same after Text, and the fw_setenv -s
// script between two environments, when Script will write one, turns the first into the
// second as libubootenv applies it.
func FuzzParseText(f *testing.F) {
	f.Add([]byte("bootcmd=run a\nbootcount=1\n"), []byte("bootcmd=run b\n"))
	f.Add([]byte("a=1\na=2\n"), []byte("b==\n"))
	f.Add([]byte("x\n"), []byte("=\n"))
	f.Add([]byte("board_serial=\xb8(b\n:\xd4=\xf1\nbootcount=1\n"), []byte("bootcount=2\n"))
	f.Fuzz(func(t *testing.T, fromText, toText []byte) {
		from, err := ParseText(fromText)
		if err != nil {
			return
		}

		back, err := ParseText(from.Text())
		if err != nil || !back.Equal(from) {
			t.Fatalf("Text round trip: %v %q", err, from.Text())
		}

		target, err := ParseText(toText)
		if err != nil {
			return
		}

		script, err := Script(from, target)
		if err != nil {
			return
		}

		applied := from.Clone()
		applied.Apply(script)

		if !applied.Equal(target) {
			t.Fatalf("script %q turns %q into %q, want %q", script, from.Text(), applied.Text(), target.Text())
		}
	})
}
