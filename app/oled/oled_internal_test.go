// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package oled

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFramebufferBackupRestore(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "fb0")

	orig := bytes.Repeat([]byte{7}, Width*Height)

	err := os.WriteFile(path, orig, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	framebuffer := NewFramebuffer(path)

	err = framebuffer.Draw(make([]byte, 10))
	if err == nil {
		t.Fatal("short frame accepted")
	}

	frame := bytes.Repeat([]byte{200}, Width*Height)

	err = framebuffer.Draw(frame)
	if err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(path); !bytes.Equal(b, frame) {
		t.Fatal("frame not written")
	}
	// second draw must not overwrite the backup with our own frame
	err = framebuffer.Draw(frame)
	if err != nil {
		t.Fatal(err)
	}

	err = framebuffer.Restore()
	if err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(path); !bytes.Equal(b, orig) {
		t.Fatal("original contents not restored")
	}
	// restore without draw is a no-op
	err = framebuffer.Restore()
	if err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(path); !bytes.Equal(b, orig) {
		t.Fatal("no-op restore changed contents")
	}
}

func TestParseSPIPath(t *testing.T) {
	t.Parallel()

	if got, ok := parseSPIPath("spidev:/dev/spidev1.0"); !ok || got != "/dev/spidev1.0" {
		t.Fatalf("got %q, %v", got, ok)
	}

	for _, path := range []string{"/dev/fb0", "spidev:"} {
		if _, ok := parseSPIPath(path); ok {
			t.Fatalf("accepted %q", path)
		}
	}
}

func TestPackFrame(t *testing.T) {
	t.Parallel()

	got := packFrame([]byte{0x12, 0x34, 0xab, 0xcd})

	want := []byte{0x31, 0xca}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestParseSPIPins(t *testing.T) {
	t.Parallel()

	pins, err := parseSPIPins("spidev:/dev/spidev1.0")
	if err != nil || pins != st20Pins() {
		t.Fatalf("no pins named = %+v, %v; want the ST20's", pins, err)
	}

	path := "spidev:/dev/spidev1.0?ce=gpio0_12&reset=gpio2_9&dc=gpio2_12"

	want := spiPins{ce: gpioLine{0, 12}, reset: gpioLine{2, 9}, dc: gpioLine{2, 12}}

	pins, err = parseSPIPins(path)
	if err != nil || pins != want {
		t.Fatalf("parseSPIPins(%q) = %+v, %v; want %+v", path, pins, err, want)
	}

	if dev, ok := parseSPIPath(path); !ok || dev != "/dev/spidev1.0" {
		t.Fatalf("parseSPIPath(%q) = %q, %v", path, dev, ok)
	}

	for _, bad := range []string{
		"spidev:/d?ce=gpio0_12&reset=gpio2_9",
		"spidev:/d?ce=gpio4_1&reset=gpio2_9&dc=gpio2_12",
		"spidev:/d?ce=x&reset=gpio2_9&dc=gpio2_12",
	} {
		_, err := parseSPIPins(bad)
		if err == nil {
			t.Errorf("parseSPIPins(%q) accepted", bad)
		}
	}
}
