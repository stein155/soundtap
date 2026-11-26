// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package safehttp_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/safehttp"
)

func TestDownloadAndGetJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bin":
			_, _ = w.Write([]byte("BINARY"))
		case "/json":
			_, _ = w.Write([]byte(`{"tag_name":"v9"}`))
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer server.Close()

	dst := filepath.Join(t.TempDir(), "out")

	err := safehttp.Download(t.Context(), server.Client(), "test", server.URL+"/bin", dst, 0o755)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	if b, _ := os.ReadFile(dst); string(b) != "BINARY" {
		t.Fatalf("downloaded %q, want BINARY", b)
	}

	err = safehttp.Download(t.Context(), server.Client(), "test", server.URL+"/nope", dst, 0o755)
	if err == nil {
		t.Fatal("Download of a 404 should fail")
	}

	var rel struct {
		TagName string `json:"tag_name"`
	}

	err = safehttp.GetJSON(t.Context(), server.Client(), "test", server.URL+"/json", &rel)
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}

	if rel.TagName != "v9" {
		t.Fatalf("GetJSON tag = %q, want v9", rel.TagName)
	}
}

// DownloadWatched reports what is in the file so far against the announced size: once
// before the first byte, then after every write, ending at the whole.
func TestDownloadWatchedReportsBytes(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "6")
		_, _ = w.Write([]byte("BINARY"))
	}))
	defer server.Close()

	var seen [][2]int64

	dst := filepath.Join(t.TempDir(), "out")

	err := safehttp.DownloadWatched(t.Context(), server.Client(), "test", server.URL, dst, 0o644,
		func(done, total int64) { seen = append(seen, [2]int64{done, total}) })
	if err != nil {
		t.Fatal(err)
	}

	if len(seen) < 2 || seen[0] != [2]int64{0, 6} || seen[len(seen)-1] != [2]int64{6, 6} {
		t.Fatalf("seen %v, want from 0 of 6 to 6 of 6", seen)
	}
}

func TestDownloadRemovesPartialFile(t *testing.T) {
	t.Parallel()

	// An upstream that dies mid-body (Content-Length says 10, sends 5) must not
	// leave a truncated file behind.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write([]byte("short"))
	}))
	defer server.Close()

	dst := filepath.Join(t.TempDir(), "out")

	err := safehttp.Download(t.Context(), server.Client(), "test", server.URL, dst, 0o755)
	if err == nil {
		t.Fatal("Download succeeded, want error")
	}

	_, err = os.Stat(dst)
	if !os.IsNotExist(err) {
		t.Fatalf("partial file exists after failed download: %v", err)
	}
}

func TestPublicIP(t *testing.T) {
	t.Parallel()

	for addr, want := range map[string]bool{
		"1.1.1.1": true, "8.8.8.8": true, "2606:4700:4700::1111": true, "192.0.0.9": true,
		"2001:4:112::1": true, "100.63.255.255": true, "100.128.0.0": true,
		"100.64.0.1": false, "100.100.100.200": false, "100.127.255.254": false,
		"203.0.113.1": false, "203.0.113.2": false, "::ffff:203.0.113.2": false,
		"192.0.2.1": false, "198.51.100.7": false, "198.18.0.1": false, "240.0.0.1": false,
		"255.255.255.255": false, "0.1.2.3": false, "10.0.0.1": false, "127.0.0.1": false,
		"169.254.1.1": false, "::1": false, "fd00::1": false, "fe80::1": false,
		"2001:db8::1": false, "3fff::1": false, "64:ff9b:1::1": false, "100::1": false,
	} {
		if got := safehttp.PublicIP(net.ParseIP(addr)); got != want {
			t.Errorf("PublicIP(%s) = %v, want %v", addr, got, want)
		}
	}
}
