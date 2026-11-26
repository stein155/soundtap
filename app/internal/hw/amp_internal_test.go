// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/hw/model"
)

//nolint:paralleltest // it points the package-wide gpioRoot at a fake sysfs tree
func TestResolveST20MuteGPIO(t *testing.T) {
	root := t.TempDir()

	device := filepath.Join(root, "devices", "4804c000.gpio")

	err := os.MkdirAll(device, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(device, filepath.Join(root, "gpiochip512"))
	if err != nil {
		t.Fatal(err)
	}

	oldRoot := gpioRoot

	gpioRoot = root
	defer func() { gpioRoot = oldRoot }()

	st20 := model.Named(model.ST20)

	amp := NewAmp(st20.Name, st20.MuteGPIO, st20.AmpPowerGPIO)
	if got, want := amp.MuteLine(), filepath.Join(root, "gpio529", "value"); got != want {
		t.Fatalf("MuteLine = %q, want %q", got, want)
	}

	if got, want := amp.PowerLine(), filepath.Join(root, "gpio535", "value"); got != want {
		t.Fatalf("PowerLine = %q, want %q", got, want)
	}
}

func TestAmpPowerWriteRetriesWhenGPIOAppears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gpio55", "value")
	amp := NewAmp(model.ST20, "", path)
	t.Setenv("AMP_POWER_GPIO", path)
	t.Setenv("AUDIO_NOINIT", "")

	amp.SetPowered(true, discardLog())

	if got := amp.powerState.Load(); got != 0 {
		t.Fatalf("missing GPIO cached state %d", got)
	}

	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, []byte("0"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	amp.SetPowered(true, discardLog())

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "1" {
		t.Fatalf("amp power value = %q, err %v", got, err)
	}
}
