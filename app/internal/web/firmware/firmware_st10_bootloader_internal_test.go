// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/install"
	"github.com/stein155/soundtap/app/internal/stu"
)

// The ST10 is the Wireless Link's sm2 module, so it takes the sm2 boot chain the universal
// package carries as soundtap-spl.binky and soundtap-uboot.binky -- behind the same RAM-boot
// proof and the same two acknowledgements.
func TestST10ReplacesItsBootChainWithTheSM2One(t *testing.T) {
	t.Parallel()

	data := universalPackage(t, stu.ExpectedSoundTapDuo)

	mlo, _ := scanBootChain(t, data, "soundtap-spl.binky", "soundtap-uboot.binky")

	post := func(s *Handler) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, installPath, bytes.NewReader(data))
		req.Header.Set("X-SoundTap-Confirm", "install")

		rec := httptest.NewRecorder()
		s.postFirmware(rec, req)

		return rec
	}
	st10 := func(nand *fakeFlash) *Handler {
		s := wlaInstallServer(t, nand, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
		s.firmwareModel = func() (string, error) { return "st10", nil }

		return s
	}

	nand := newFakeFlash()

	rec := post(st10(nand))
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}

	joined := strings.Join(nand.cmds, "\n")
	for _, mtd := range []string{"/dev/mtd1", "/dev/mtd2", "/dev/mtd3", "/dev/mtd4", "/dev/mtd0"} {
		if !writesTo(joined, mtd) {
			t.Errorf("no nandwrite to %s in:\n%s", mtd, joined)
		}
	}

	if !bytes.Equal(nand.writes["/dev/mtd0"], mlo.Bytes()) {
		t.Error("mtd0 does not hold the sm2 SPL")
	}
	// Our U-Boot cannot run Bose's nboot.e recipes: ours are in the environment,
	// and they went in before the first bootloader write.
	env := envOf(t, nand)

	recipes, _ := flash.MainlineRecipes("rhino")
	for _, entry := range recipes {
		key, value, _ := strings.Cut(entry, "=")
		if got, _ := env.Get(key); got != value {
			t.Errorf("%s = %q, want the mainline recipe", key, got)
		}
	}

	envAt, splAt := strings.Index(joined, "fw_setenv -c"), strings.Index(joined, "nandwrite -p /dev/mtd1")
	if envAt < 0 || envAt > splAt {
		t.Errorf("recipes not written before the bootloaders:\n%s", joined)
	}
}

// The ST20 takes its SPL and the shared U-Boot through the same endpoint as the ST10 and
// the Wireless Link.
func TestST20ReplacesItsBootChainThroughTheAPI(t *testing.T) {
	t.Parallel()

	data := universalPackage(t, stu.ExpectedSoundTapTri)

	mlo, uboot := scanBootChain(t, data, "soundtap-spl.spotty", "soundtap-uboot.binky")

	post := func(nand *fakeFlash) *httptest.ResponseRecorder {
		handler := wlaInstallServer(t, nand, "root=ubi0:rootfs ubi.mtd=9,2048")
		handler.firmwareModel = func() (string, error) { return "st20", nil }
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, installPath, bytes.NewReader(data))
		req.Header.Set("X-SoundTap-Confirm", "install")

		rec := httptest.NewRecorder()
		handler.postFirmware(rec, req)

		return rec
	}
	nand := newFakeFlash()

	rec := post(nand)
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}

	var order []string

	for _, cmd := range nand.cmds {
		fields := strings.Fields(cmd)
		switch {
		case fields[0] == "nandwrite" && fields[len(fields)-2] <= "/dev/mtd4":
			order = append(order, strings.TrimPrefix(fields[len(fields)-2], "/dev/"))
		case fields[0] == "dd" && fields[5] == "skip=0": // a U-Boot copy's first block
			order = append(order, strings.TrimPrefix(fields[2], "of=/dev/"))
		default:
			// Erases, reads and the rest of each copy say nothing about the order.
		}
	}

	if want := "mtd4 mtd1 mtd2 mtd3 mtd0 mtd4"; strings.Join(order, " ") != want {
		t.Errorf("write order %v, want %s", order, want)
	}

	for _, mtd := range []string{"/dev/mtd0", "/dev/mtd1", "/dev/mtd2", "/dev/mtd3"} {
		if !bytes.Equal(nand.writes[mtd], mlo.Bytes()) {
			t.Errorf("%s does not hold the ST20 SPL", mtd)
		}
	}

	if !holdsTwoUBoots(nand.writes["/dev/mtd4"], uboot.Bytes()) {
		t.Error("mtd4 does not hold the shared U-Boot")
	}

	env := envOf(t, nand)

	recipes, _ := flash.MainlineRecipes("spotty")
	for _, entry := range recipes {
		key, value, _ := strings.Cut(entry, "=")
		if got, _ := env.Get(key); got != value {
			t.Errorf("%s = %q, want the mainline recipe", key, got)
		}
	}
}

// A bootloader that cannot fit its partition is refused from its header, before it is
// staged: the staging directory is on /mnt/nv, which a 120 MiB "u-boot.img" would fill.
func TestOversizeBootloaderIsNotStaged(t *testing.T) {
	t.Parallel()

	for _, tcase := range []struct {
		model, module string
		size          int
		want          string
	}{
		{"st10", "soundtap-spl.binky", 0x20000 + 1, "mtd0 holds 131072"},
		{"st10", "soundtap-uboot.binky", 0x1e0000 + 1, "mtd4 holds 1966080"},
		{"st20", "soundtap-spl.spotty", 0x20000 + 1, "mtd0 holds 131072"},
	} {
		t.Run(tcase.module, func(t *testing.T) {
			t.Parallel()

			data := universalPackageSized(t, stu.ExpectedSoundTapTri, map[string]int{tcase.module: tcase.size})
			nand := newFakeFlash()
			handler := wlaInstallServer(t, nand, "root=ubi0:rootfs ubi.mtd=9,2048")
			handler.firmwareModel = func() (string, error) { return tcase.model, nil }

			var log bytes.Buffer

			handler.log = slog.New(slog.NewTextHandler(&log, nil))

			rec := installWLA(t, handler, data)
			if rec.Code == 200 || !strings.Contains(log.String(), tcase.want) {
				t.Fatalf("status %d %s, log:\n%s", rec.Code, rec.Body, log.String())
			}

			for _, cmd := range nand.cmds {
				for _, mtd := range []string{"/dev/mtd0", "/dev/mtd1", "/dev/mtd2", "/dev/mtd3", "/dev/mtd4"} {
					if strings.HasSuffix(cmd, mtd) || strings.Contains(cmd, mtd+" ") {
						t.Fatalf("boot chain touched: %s", cmd)
					}
				}
			}
		})
	}
}

// twoUBoots is mtd4 as an install leaves it: the image, then its spare.
// holdsTwoUBoots reports whether mtd4 holds image twice, as WriteBootloaders leaves it,
// with whatever is in its last block, the redundant environment's, left alone.
func holdsTwoUBoots(mtd4, image []byte) bool {
	want := twoUBoots(image)

	return len(mtd4) >= len(want) && bytes.Equal(mtd4[:len(want)], want)
}

func twoUBoots(image []byte) []byte {
	b := bytes.Repeat([]byte{0xff}, install.SecondUBootOffset)
	copy(b, image)
	// dd writes whole blocks, the last one padded with 0xff.
	return append(b, append(image, bytes.Repeat([]byte{0xff}, (131072-len(image)%131072)%131072)...)...)
}
