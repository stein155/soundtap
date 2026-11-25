// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package stu

import (
	"bytes"
	"io"
	"testing"
)

// FuzzScan feeds Scan what an upload or a USB stick may hold. It must never panic, and a
// package it accepts must be one of the chains it knows, whole: every byte accounted for.
// `go test` runs the seeds; `go test -fuzz=FuzzScan ./internal/stu` searches further.
func FuzzScan(f *testing.F) {
	f.Add(build(f, nil))
	f.Add(buildChain(f, ExpectedST20, nil))
	f.Add(buildChain(f, ExpectedWLANative, nil))
	f.Add(buildChain(f, ExpectedSoundTapTri, map[string]int{"soundtap-rootfs.binky": 0x1000}))
	short := build(f, nil)
	f.Add(short[:len(short)/2])
	f.Add([]byte("BOSE"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		var sunk int64

		pkg, err := Scan(bytes.NewReader(data), func(Module) io.Writer {
			return writerFunc(func(b []byte) (int, error) {
				sunk += int64(len(b))

				return len(b), nil
			})
		})
		if err != nil {
			return
		}

		if pkg.Size != int64(len(data)) {
			t.Fatalf("accepted a package of %d bytes as %d", len(data), pkg.Size)
		}

		if len(pkg.Modules) == 0 || len(pkg.Targets) == 0 {
			t.Fatalf("accepted a package without modules or a model: %+v", pkg)
		}

		var payload int64

		for _, m := range pkg.Modules {
			if m.Size <= 0 || m.Size > int64(len(data)) {
				t.Fatalf("module %s of %d bytes in a %d-byte package", m.Name, m.Size, len(data))
			}

			payload += m.Payload()
		}

		if sunk > payload+int64(len(pkg.Modules))*overlap {
			t.Fatalf("handed out %d payload bytes for modules of %d", sunk, payload)
		}
	})
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(b []byte) (int, error) { return w(b) }
