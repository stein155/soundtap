// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLargestEntriesSumsDirectories(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write := func(name string, size int) {
		path := filepath.Join(dir, name)

		err := os.MkdirAll(filepath.Dir(path), 0o755)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(path, make([]byte, size), 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}
	write("small", 10)
	write("big/a", 300)
	write("big/sub/b", 400)
	write("mid", 500)

	got := largestEntries(dir, 2)
	if len(got) != 2 || filepath.Base(got[0].Path) != "big" || got[0].Bytes != 700 ||
		filepath.Base(got[1].Path) != "mid" || got[1].Bytes != 500 {
		t.Fatalf("largestEntries = %+v", got)
	}

	if largestEntries(filepath.Join(dir, "missing"), 3) != nil {
		t.Fatal("a missing directory must give nothing")
	}
}

func TestFailReportsFullStorage(t *testing.T) {
	t.Parallel()

	s := new(Server)
	s.log = quiet()
	rec := httptest.NewRecorder()
	s.fail(rec, "storing the new soundtapd failed", &os.PathError{Op: "write", Path: "x", Err: syscall.ENOSPC})

	if rec.Code != http.StatusInsufficientStorage || !strings.Contains(rec.Body.String(), "storage is full") {
		t.Fatalf("fail = %d %s", rec.Code, rec.Body.String())
	}
}

func TestDiskLowFitsTheSpeakerVolume(t *testing.T) {
	t.Parallel()

	const volume = 31_500 << 10 // the speakers' /mnt/nv
	if (diskUsage{FreeBytes: 23 << 20, TotalBytes: volume, Path: ""}).low() {
		t.Error("23 MiB free of the 31.5 MiB volume counted as low")
	}

	if !(diskUsage{FreeBytes: 2 << 20, TotalBytes: volume, Path: ""}).low() {
		t.Error("2 MiB free not counted as low")
	}

	if (diskUsage{Path: "", FreeBytes: 0, TotalBytes: 0}).low() {
		t.Error("an unmeasured volume counted as low")
	}
}
