// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/hw/model"
)

// ampFaultHarness points a monitor at a temp sysfs tree and starts it, returning it and
// the gpio value path a test drives edges on. Stops the monitor on cleanup.
//
// The mute decision itself is covered by TestShouldMute in soundtapd rather than by
// inspecting the GPIO file.
func ampFaultHarness(t *testing.T, initial string) (*AmpFault, string) {
	t.Helper()
	dir := t.TempDir()

	gpioDir := filepath.Join(dir, "gpio56")

	err := os.MkdirAll(gpioDir, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	valuePath := filepath.Join(gpioDir, "value")

	err = os.WriteFile(valuePath, []byte(initial), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	origRoot := gpioRoot
	gpioRoot = dir
	f := &AmpFault{gpio: 56, every: time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	t.Cleanup(func() {
		cancel()
		// Wait for the monitor to exit before restoring the globals it reads,
		// otherwise the restore races its next poll.
		<-done

		gpioRoot = origRoot
	})

	go func() { defer close(done); f.Run(ctx, model.ST20, func() {}, slog.Default()) }()

	return f, valuePath
}

// waitFault polls until f.Active matches want, or fails the test.
func waitFault(t *testing.T, f *AmpFault, want bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.Active() == want {
			return
		}

		time.Sleep(time.Millisecond)
	}

	t.Fatalf("Active() = %v, want %v", f.Active(), want)
}

func TestAmpFaultDetect(t *testing.T) {
	f, _ := ampFaultHarness(t, "0\n") // asserted (low) from the start
	waitFault(t, f, true)
}

func TestAmpFaultStaysClearWhenDeasserted(t *testing.T) {
	f, _ := ampFaultHarness(t, "1\n") // a healthy amp holds the line high
	// Give the monitor several polls; the latch must never set.
	time.Sleep(20 * time.Millisecond)

	if f.Active() {
		t.Error("want no amp fault, but the latch was set")
	}
}

// TestAmpFaultEdges is the behaviour trackOwnership depends on: the line going low
// mutes the amp immediately and sets the latch, and going high again clears the latch
// so a recovered amp is not left muted forever. Active low: a healthy amp reads 1.
func TestAmpFaultEdges(t *testing.T) {
	f, valuePath := ampFaultHarness(t, "1\n")

	err := os.WriteFile(valuePath, []byte("0\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	waitFault(t, f, true)

	err = os.WriteFile(valuePath, []byte("1\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	waitFault(t, f, false)
}

// A line that flickers healthy for fewer than ampFaultClearReads polls must keep the amp
// muted; each unmute of a still-faulting amp is an audible pop.
func TestAmpFaultLatchNeedsSustainedClear(t *testing.T) {
	latched, healthy := ampFaultLatch(false, true, 0)
	for i := 1; i < ampFaultClearReads; i++ {
		latched, healthy = ampFaultLatch(latched, false, healthy)
		if !latched {
			t.Fatalf("released after %d healthy reads, want %d", i, ampFaultClearReads)
		}
	}

	if latched, _ = ampFaultLatch(latched, true, healthy); !latched {
		t.Fatal("fault during the hold must keep the latch")
	}

	latched, healthy = true, 0
	for range ampFaultClearReads {
		latched, healthy = ampFaultLatch(latched, false, healthy)
	}

	if latched {
		t.Fatalf("still latched after %d healthy reads", ampFaultClearReads)
	}
}

func TestAmpFaultExportsMissingGPIO(t *testing.T) {
	dir := t.TempDir()

	exportPath := filepath.Join(dir, "export")
	if err := os.WriteFile(exportPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AUDIO_NOINIT", "1")

	origRoot := gpioRoot
	gpioRoot = dir

	t.Cleanup(func() { gpioRoot = origRoot })

	f := &AmpFault{gpio: 56, every: time.Millisecond}

	// gpio56 does not exist, so the monitor exports it, then finds no value file. It
	// must give up after ampFaultReadFailLimit reads instead of warning forever.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); f.Run(ctx, model.ST20, func() {}, slog.Default()) }()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("Run kept polling a missing GPIO instead of giving up")
	}

	b, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}

	if string(b) != "56" {
		t.Errorf("export file = %q, want %q", string(b), "56")
	}
}

// TestAmpFaultDisabledWithoutEvidence covers the ST10 case: no fault GPIO is
// documented for it, so the monitor must return at once rather than poll a guessed
// pin. A wrong pin reading high would latch a permanent mute.
func TestAmpFaultDisabledWithoutEvidence(t *testing.T) {
	f := &AmpFault{every: time.Millisecond}

	done := make(chan struct{})
	go func() { defer close(done); f.Run(context.Background(), model.ST10, func() {}, slog.Default()) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run polled despite no fault GPIO for the model")
	}

	if f.Active() {
		t.Error("latch set with the monitor disabled")
	}
}

// TestModelAmpFaultGPIO pins the per-model numbers against the recorded evidence, so
// a future model cannot silently inherit the ST20's pin the way BTUart once did.
func TestModelAmpFaultGPIO(t *testing.T) {
	if got := model.Named(model.ST10).AmpFaultGPIO; got != 0 {
		t.Errorf("st10 AmpFaultGPIO = %d, want 0 (no documented fault pin)", got)
	}

	if got := model.Named(model.ST20).AmpFaultGPIO; got != 56 {
		t.Errorf("st20 AmpFaultGPIO = %d, want 56 (hardware-facts.md)", got)
	}
}

// Mainline numbers GPIO banks dynamically, so the init script hands over the resolved
// line; the flat stock number is only the fallback.
func TestAmpFaultGPIOFromEnvironment(t *testing.T) {
	t.Setenv("AMP_FAULT_GPIO", "536")

	if got := NewAmpFault(56).gpio; got != 536 {
		t.Fatalf("NewAmpFault(56) watches %d, want 536", got)
	}
}
