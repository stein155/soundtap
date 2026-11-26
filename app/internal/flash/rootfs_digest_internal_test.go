// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
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
	flasher := &Installer{
		Target: Target{
			Alternate: Slot{
				Name: "PONG", KernelMTD: 7, RootfsMTD: 9, RootfsName: "",
				KernelSize: 0, RootfsSize: 0x6000000, RootfsEraseSize: testPEB,
			},
			Running: zeroSlot(), BootloaderSizes: nil, BootloaderEraseSizes: nil, EnvMTD: 0, EnvSize: 0,
			EnvEraseSize: 0, PersistentName: "", BootCount: false, EnvRedundant: false,
		},
		written: nil,
		Run: func(stdin io.Reader, name string, _ ...string) error {
			*cmds = append(*cmds, name)

			if stdin != nil {
				_, err := io.Copy(io.Discard, stdin)
				if err != nil {
					return fmt.Errorf("ubiformat's stdin: %w", err)
				}
			}

			return nil
		},
		Open: func(name string) (io.ReadCloser, error) {
			*cmds = append(*cmds, "open:"+name)

			return open(name)
		},
	}

	writer, wait := flasher.StartRootfs(int64(len(image)))

	_, err := writer.Write(image)
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = wait()
	if err != nil {
		t.Fatal(err)
	}

	return flasher, cmds
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

func (*failingReader) Close() error { return nil }

// The whole volume is read back and held to what was streamed into ubiformat. A slot that
// reads back short, with an I/O error, or with one bit different is refused, and refused
// before it is mounted: VerifyRootfs is what stands between the write and the selection.
func TestVerifyRootfsReadsTheWholeVolumeBack(t *testing.T) {
	t.Parallel()

	image := flashtest.UBIImage(testPEB, testVolume())
	volume := flashtest.UBIVolume(image, testPEB)
	flipped := bytes.Clone(volume)

	flipped[len(flipped)/2] ^= 0x10
	for _, test := range []struct {
		name string
		back io.ReadCloser
		want string
	}{
		{"clean", io.NopCloser(bytes.NewReader(volume)), ""},
		{"short read", io.NopCloser(bytes.NewReader(volume[:len(volume)-1])), "reads back"},
		{"I/O error", &failingReader{data: volume[:testPEB], err: errIO}, "input/output error"},
		{"digest mismatch", io.NopCloser(bytes.NewReader(flipped)), "does not read back as written"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			in, cmds := writtenSlot(t, image, func(string) (io.ReadCloser, error) { return test.back, nil })

			err := in.VerifyRootfs(bootFiles, "rhino")
			if test.want == "" {
				if err != nil {
					t.Fatalf("clean slot refused: %v", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}

			if got := strings.Join(*cmds, " "); got != "ubiformat ubiattach open:/dev/ubi7_0 ubidetach" {
				t.Fatalf("commands %q: a slot that does not read back went on to be mounted", got)
			}
		})
	}
}

// A volume extends past what the image fills: the read covers the image's LEBs, no more.
func TestVerifyRootfsIgnoresTheVolumeBeyondTheImage(t *testing.T) {
	t.Parallel()

	image := flashtest.UBIImage(testPEB, testVolume())
	back := append(flashtest.UBIVolume(image, testPEB), bytes.Repeat([]byte{0xff}, 3*testPEB)...)

	in, _ := writtenSlot(t, image, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(back)), nil
	})

	err := in.VerifyRootfs(bootFiles, "rhino")
	if err != nil {
		t.Fatalf("autoresized volume refused: %v", err)
	}
}

// ubiformat writes no eraseblock the image leaves out, and UBI reads an unmapped LEB as
// erased: the digest has to agree.
func TestRootfsDigestReadsASkippedLEBAsErased(t *testing.T) {
	t.Parallel()

	image := flashtest.UBIImage(testPEB, testVolume())
	volume := flashtest.UBIVolume(image, testPEB)
	// Drop LEB 1 (the fourth eraseblock) from the image; on flash it reads as 0xFF.
	sparse := append(bytes.Clone(image[:3*testPEB]), image[4*testPEB:]...)
	leb := testPEB - 4096
	back := bytes.Clone(volume)
	copy(back[leb:2*leb], bytes.Repeat([]byte{0xff}, leb))

	d := newRootfsDigest(testPEB, 0x6000000)

	_, _ = d.Write(sparse)

	err := d.verify(func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(back)), nil })
	if err != nil {
		t.Fatalf("sparse image: %v", err)
	}
}

// An image the digest cannot follow is refused rather than waved through unverified.
func TestRootfsDigestRefusesWhatItCannotFollow(t *testing.T) {
	t.Parallel()

	image := flashtest.UBIImage(testPEB, testVolume())
	outOfOrder := append(bytes.Clone(image[:2*testPEB]), image[3*testPEB:4*testPEB]...)
	outOfOrder = append(outOfOrder, image[2*testPEB:3*testPEB]...)
	farLEB := bytes.Clone(image)
	binary.BigEndian.PutUint32(farLEB[2*testPEB+2048+12:], 0xfffffff0)

	never := func(string) (io.ReadCloser, error) { return nil, errMustNotRead }

	for _, test := range []struct {
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
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			d := newRootfsDigest(test.peb, test.size)

			_, _ = d.Write(test.stream)

			err := d.verify(never)
			if err == nil || !strings.Contains(err.Error(), "cannot verify") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// Header fields from 2^31 up are refused, not followed: on the speaker an int is 32 bits,
// and taken as one they turned negative, passed every check and sliced out of range.
func TestRootfsDigestRefusesHeaderFieldsPastAnInt32(t *testing.T) {
	t.Parallel()

	image := flashtest.UBIImage(testPEB, testVolume())
	data := 2 * testPEB // the first eraseblock of the volume, after the two layout copies
	vid := data + 2048  // ubiformat -s 2048

	patch := func(off int, value uint32, typ byte) []byte {
		out := bytes.Clone(image)
		binary.BigEndian.PutUint32(out[off:], value)

		if typ != 0 {
			out[vid+5] = typ
		}

		return out
	}

	for _, test := range []struct {
		name   string
		stream []byte
	}{
		{"VID header offset", patch(data+ubiECVIDOff, 0x7ffffff0, 0)},
		{"data offset", patch(data+ubiECDataOff, 0xfffff000, 0)},
		{"data pad", patch(vid+28, 0xfffffff0, 0)},
		{"static data size", patch(vid+20, 0xfffffff0, ubiStatic)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			d := newRootfsDigest(testPEB, 0x6000000)

			_, _ = d.Write(test.stream)

			err := d.verify(func(string) (io.ReadCloser, error) { return nil, errMustNotRead })
			if err == nil || !strings.Contains(err.Error(), "cannot verify") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// The failures the fakes report.
var (
	errIO          = errors.New("input/output error")
	errMustNotRead = errors.New("must not read")
)

// zeroSlot and zeroTarget are the empty values, spelled out.
func zeroSlot() Slot {
	return Slot{Name: "", KernelMTD: 0, RootfsMTD: 0, RootfsName: "", KernelSize: 0, RootfsSize: 0, RootfsEraseSize: 0}
}

func zeroTarget() Target {
	return Target{
		Running: zeroSlot(), Alternate: zeroSlot(), BootloaderSizes: nil, BootloaderEraseSizes: nil,
		EnvMTD: 0, EnvSize: 0, EnvEraseSize: 0, PersistentName: "", BootCount: false, EnvRedundant: false,
	}
}

// VerifyRootfs with nothing streamed has nothing to hold the slot to.
func TestVerifyRootfsRefusesWithoutAWrite(t *testing.T) {
	t.Parallel()

	in := &Installer{
		Target: zeroTarget(), Open: nil, written: nil,
		Run: func(io.Reader, string, ...string) error { return nil },
	}

	err := in.VerifyRootfs(bootFiles, "rhino")
	if err == nil {
		t.Fatal("verified a slot nothing was written to")
	}
}

// The digest follows what ubiformat took, in pieces of any size.
func TestRootfsDigestFollowsAStreamInPieces(t *testing.T) {
	t.Parallel()

	image := flashtest.UBIImage(testPEB, testVolume())
	digest := newRootfsDigest(testPEB, 0x6000000)

	for rest := image; len(rest) > 0; {
		n := min(len(rest), 4093)
		_, _ = digest.Write(rest[:n])
		rest = rest[n:]
	}

	back := flashtest.UBIVolume(image, testPEB)

	err := digest.verify(func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(back)), nil })
	if err != nil {
		t.Fatal(err)
	}
}
