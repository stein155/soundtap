// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stein155/soundtap/app/internal/install"
)

// Both package shapes have been measured on hardware and they disagree, so the
// length is decided by arithmetic rather than by which builder made the package.
// The long one gives "not multiple of eraseblock size"; the short one gives
// "error 0 (No error information)" straight after ubiformat's scan.
func TestUBIBodySizePicksTheWholeNumberOfEraseblocks(t *testing.T) {
	t.Parallel()

	const peb = 131072

	cases := []struct {
		name string
		size int64
		want int64
	}{
		{"ours, 2026-09-17: aligned once the overlap is dropped", 51118100, 51118080},
		{"stock's approved package: aligned as declared", 89653248, 89653248},
		// Neither length fits, so the package is handed over as declared and
		// ubiformat is left to refuse it.
		{"neither", 51118090, 51118090},
	}
	for _, c := range cases {
		if got := install.UBIBodySize(c.size, peb); got != c.want {
			t.Errorf("%s: install.UBIBodySize(%d) = %d, want %d", c.name, c.size, got, c.want)
		}
	}
	// A synthetic package in a test has no geometry behind it; leave it alone.
	if got := install.UBIBodySize(51118100, 0); got != 51118100 {
		t.Errorf("without an eraseblock size, install.UBIBodySize = %d", got)
	}
}

// The scanner streams the module's whole declared length; the bytes past the
// image belong to the next header and would make ubiformat refuse the image.
func TestTruncatingWriterDropsWhatIsNotTheImage(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	writer := &install.TruncatingWriter{W: &out, Left: 10}

	written, err := writer.Write([]byte("0123456789abcdef"))
	if err != nil || written != 16 {
		t.Fatalf("Write = %d, %v; the scanner must see its whole write accepted", written, err)
	}

	if out.String() != "0123456789" {
		t.Errorf("wrote %q", out.String())
	}

	written, err = writer.Write([]byte("more"))
	if err != nil || written != 4 {
		t.Errorf("second Write = %d, %v", written, err)
	}

	if out.String() != "0123456789" {
		t.Errorf("after the image ended, wrote %q", out.String())
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

// A short write into the ubiformat pipe must reach the scanner, or the install
// would pass its CRC check on bytes NAND never got.
func TestTruncatingWriterReportsShortWrites(t *testing.T) {
	t.Parallel()

	w := &install.TruncatingWriter{W: shortWriter{}, Left: 10}

	n, err := w.Write([]byte("0123"))
	if !errors.Is(err, io.ErrShortWrite) || n != 2 {
		t.Fatalf("Write = %d, %v; want 2, io.ErrShortWrite", n, err)
	}
}
