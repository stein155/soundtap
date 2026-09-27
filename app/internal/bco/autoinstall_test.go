// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAutoInstallOncePerImage(t *testing.T) {
	dir := t.TempDir()
	image, marker := filepath.Join(dir, "app.bcd"), filepath.Join(dir, "nv", "bco-image")
	if err := os.WriteFile(image, []byte("own image"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls, runsOwn := 0, true
	own := func() bool { return runsOwn }
	fail := func(ctx context.Context, img []byte) (string, error) {
		calls++
		if _, err := CheckAppImage(img); err != nil {
			t.Fatalf("flash got an image CheckAppImage refuses: %v", err)
		}
		return "", errors.New("no BCO")
	}
	if _, err := AutoInstall(context.Background(), image, marker, own, fail, io.Discard); err == nil {
		t.Fatal("flash error not reported")
	}
	// Failed once: the next boot leaves the BCO alone.
	if _, err := AutoInstall(context.Background(), image, marker, own, fail, io.Discard); err != nil || calls != 1 {
		t.Fatalf("retried a failed image: calls=%d err=%v", calls, err)
	}
	// A new OS image is tried again.
	if err := os.WriteFile(image, []byte("next image"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := func(ctx context.Context, img []byte) (string, error) { calls++; return "link-2", nil }
	if flashed, err := AutoInstall(context.Background(), image, marker, own, ok, io.Discard); err != nil || calls != 2 || !flashed {
		t.Fatalf("new image: calls=%d flashed=%v err=%v", calls, flashed, err)
	}
	// Installed and running: left alone.
	if flashed, err := AutoInstall(context.Background(), image, marker, own, ok, io.Discard); err != nil || calls != 2 || flashed {
		t.Fatalf("reflashed a BCO that runs the image: calls=%d flashed=%v err=%v", calls, flashed, err)
	}
	// Stock flashed back over it: installed again.
	runsOwn = false
	if _, err := AutoInstall(context.Background(), image, marker, own, ok, io.Discard); err != nil || calls != 3 {
		t.Fatalf("BCO back on stock: calls=%d err=%v", calls, err)
	}
}

// A flash cut off after the erase leaves the BCO without an app, and the speaker without a
// network: it is tried again on every call, also on later boots, with no limit.
func TestAutoInstallRetriesWhileNoOwnApp(t *testing.T) {
	dir := t.TempDir()
	image, marker := filepath.Join(dir, "app.bcd"), filepath.Join(dir, "nv", "bco-image")
	if err := os.WriteFile(image, []byte("own image"), 0o644); err != nil {
		t.Fatal(err)
	}
	none := func() bool { return false }
	calls := 0
	fail := func(context.Context, []byte) (string, error) { calls++; return "", errors.New("cut off") }

	// One attempt per call, and every failure asks for another while no own app runs.
	for want := 1; want <= 5; want++ {
		_, err := AutoInstall(context.Background(), image, marker, none, fail, io.Discard)
		if err == nil || calls != want || !RetryInstall(err, none) {
			t.Fatalf("attempt %d: calls=%d err=%v retry=%v", want, calls, err, RetryInstall(err, none))
		}
	}
	if b, _ := os.ReadFile(marker); string(b) != markerSum(t, image)+" 5\n" {
		t.Fatalf("marker after five failures: %q", b)
	}
	// Our own app came back (an older image): the failure is not retried.
	if RetryInstall(errors.New("cut off"), func() bool { return true }) {
		t.Fatal("retry while the own app carries the LAN")
	}

	// A marker from before the count, BCO in its loader: tried again, and a success sticks.
	if err := os.WriteFile(marker, []byte(markerSum(t, image)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := func(context.Context, []byte) (string, error) { calls++; return "link-2", nil }
	if flashed, err := AutoInstall(context.Background(), image, marker, none, ok, io.Discard); err != nil || !flashed || calls != 6 {
		t.Fatalf("old marker: calls=%d flashed=%v err=%v", calls, flashed, err)
	}
	if b, _ := os.ReadFile(marker); string(b) != markerSum(t, image)+" ok\n" {
		t.Fatalf("marker after the success: %q", b)
	}
}

func TestRetryDelayBacksOffToAnHour(t *testing.T) {
	for n, want := range map[int]time.Duration{1: 5 * time.Minute, 2: 10 * time.Minute, 3: 20 * time.Minute,
		4: 40 * time.Minute, 5: time.Hour, 50: time.Hour} {
		if got := RetryDelay(n); got != want {
			t.Errorf("RetryDelay(%d) = %s, want %s", n, got, want)
		}
	}
}

func markerSum(t *testing.T, image string) string {
	t.Helper()
	b, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
