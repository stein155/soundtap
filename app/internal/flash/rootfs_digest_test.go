// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/flash/flashtest"
)

const testPEB = 0x20000

// testVolume is a little over two LEBs, so the last one is padded.
func testVolume() []byte {
	v := make([]byte, 2*(testPEB-4096)+1000)
	for i := range v {
		v[i] = byte(i*31 + i>>9)
	}
	return v
}

// writtenSlot streams image into the alternate rootfs the way an install does, and returns
// the installer and the commands it ran, volume reads included as "open:<device>".
func writtenSlot(t *testing.T, image []byte, open func(string) (io.ReadCloser, error)) (*Installer, *[]string) {
	t.Helper()
	cmds := &[]string{}
	in := &Installer{
		Target: Target{Alternate: Slot{Name: "PONG", KernelMTD: 7, RootfsMTD: 9,
			RootfsSize: 0x6000000, RootfsEraseSize: testPEB}},
		Run: func(stdin io.Reader, name string, _ ...string) error {
			*cmds = append(*cmds, name)
			if stdin != nil {
				_, err := io.Copy(io.Discard, stdin)
				return err
			}
			return nil
		},
		Open: func(name string) (io.ReadCloser, error) {
			*cmds = append(*cmds, "open:"+name)
			return open(name)
		},
	}
	w, wait := in.StartRootfs(int64(len(image)))
	if _, err := w.Write(image); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := wait(); err != nil {
		t.Fatal(err)
	}
	return in, cmds
}

func bootFiles(_ string, args ...string) ([]byte, error) { return flashtest.BootFile(args[0]), nil }

// failingReader returns data and then err.
type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func (r *failingReader) Close() error { return nil }

// The whole volume is read back and held to what was streamed into ubiformat. A slot that
// reads back short, with an I/O error, or with one bit different is refused, and refused
// before it is mounted: VerifyRootfs is what stands between the write and the selection.
func TestVerifyRootfsReadsTheWholeVolumeBack(t *testing.T) {
	image := flashtest.UBIImage(testPEB, testVolume())
	volume := flashtest.UBIVolume(image, testPEB)
	flipped := bytes.Clone(volume)
	flipped[len(flipped)/2] ^= 0x10
	for _, tc := range []struct {
		name string
		back io.ReadCloser
		want string
	}{
		{"clean", io.NopCloser(bytes.NewReader(volume)), ""},
		{"short read", io.NopCloser(bytes.NewReader(volume[:len(volume)-1])), "reads back"},
		{"I/O error", &failingReader{data: volume[:testPEB], err: errors.New("input/output error")}, "input/output error"},
		{"digest mismatch", io.NopCloser(bytes.NewReader(flipped)), "does not read back as written"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, cmds := writtenSlot(t, image, func(string) (io.ReadCloser, error) { return tc.back, nil })
			err := in.VerifyRootfs(bootFiles, "rhino")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("clean slot refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if got := strings.Join(*cmds, " "); got != "ubiformat ubiattach open:/dev/ubi7_0 ubidetach" {
				t.Fatalf("commands %q: a slot that does not read back went on to be mounted", got)
			}
		})
	}
}

// A volume extends past what the image fills: the read covers the image's LEBs, no more.
func TestVerifyRootfsIgnoresTheVolumeBeyondTheImage(t *testing.T) {
	image := flashtest.UBIImage(testPEB, testVolume())
	back := append(flashtest.UBIVolume(image, testPEB), bytes.Repeat([]byte{0xff}, 3*testPEB)...)
	in, _ := writtenSlot(t, image, func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(back)), nil })
	if err := in.VerifyRootfs(bootFiles, "rhino"); err != nil {
		t.Fatalf("autoresized volume refused: %v", err)
	}
}

// ubiformat writes no eraseblock the image leaves out, and UBI reads an unmapped LEB as
// erased: the digest has to agree.
func TestRootfsDigestReadsASkippedLEBAsErased(t *testing.T) {
	image := flashtest.UBIImage(testPEB, testVolume())
	volume := flashtest.UBIVolume(image, testPEB)
	// Drop LEB 1 (the fourth eraseblock) from the image; on flash it reads as 0xFF.
	sparse := append(bytes.Clone(image[:3*testPEB]), image[4*testPEB:]...)
	leb := testPEB - 4096
	back := bytes.Clone(volume)
	copy(back[leb:2*leb], bytes.Repeat([]byte{0xff}, leb))
	d := newRootfsDigest(testPEB, 0x6000000)
	_, _ = d.Write(sparse)
	if err := d.verify(func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(back)), nil }, "7"); err != nil {
		t.Fatalf("sparse image: %v", err)
	}
}

// An image the digest cannot follow is refused rather than waved through unverified.
func TestRootfsDigestRefusesWhatItCannotFollow(t *testing.T) {
	image := flashtest.UBIImage(testPEB, testVolume())
	outOfOrder := append(bytes.Clone(image[:2*testPEB]), image[3*testPEB:4*testPEB]...)
	outOfOrder = append(outOfOrder, image[2*testPEB:3*testPEB]...)
	farLEB := bytes.Clone(image)
	binary.BigEndian.PutUint32(farLEB[2*testPEB+2048+12:], 0xfffffff0)
	never := func(string) (io.ReadCloser, error) { return nil, errors.New("must not read") }
	for _, tc := range []struct {
		name      string
		peb, size int64
		stream    []byte
	}{
		{"not UBI", testPEB, 0x6000000, bytes.Repeat([]byte("rootfs"), testPEB)},
		{"ends inside an eraseblock", testPEB, 0x6000000, image[:len(image)-1]},
		{"LEBs out of order", testPEB, 0x6000000, outOfOrder},
		{"LEB past the partition", testPEB, 0x6000000, farLEB},
		{"no volume", testPEB, 0x6000000, image[:2*testPEB]},
		{"unknown geometry", 0, 0x6000000, image},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newRootfsDigest(tc.peb, tc.size)
			_, _ = d.Write(tc.stream)
			if err := d.verify(never, "7"); err == nil || !strings.Contains(err.Error(), "cannot verify") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// VerifyRootfs with nothing streamed has nothing to hold the slot to.
func TestVerifyRootfsRefusesWithoutAWrite(t *testing.T) {
	in := &Installer{Run: func(io.Reader, string, ...string) error { return nil }}
	if err := in.VerifyRootfs(bootFiles, "rhino"); err == nil {
		t.Fatal("verified a slot nothing was written to")
	}
}

// The digest follows what ubiformat took, in pieces of any size.
func TestRootfsDigestFollowsAStreamInPieces(t *testing.T) {
	image := flashtest.UBIImage(testPEB, testVolume())
	d := newRootfsDigest(testPEB, 0x6000000)
	for rest := image; len(rest) > 0; {
		n := min(len(rest), 4093)
		_, _ = d.Write(rest[:n])
		rest = rest[n:]
	}
	back := flashtest.UBIVolume(image, testPEB)
	if err := d.verify(func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(back)), nil }, "7"); err != nil {
		t.Fatal(err)
	}
}
