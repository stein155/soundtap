// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// slowScan counts surveys and makes each one last long enough to overlap.
type slowScan struct {
	Backend

	n atomic.Int32
}

func (b *slowScan) ScanWifi(context.Context) ([]speaker.WifiNetwork, error) {
	b.n.Add(1)
	time.Sleep(50 * time.Millisecond)

	return []speaker.WifiNetwork{{SSID: "home", Signal: "", Secure: false, WEP: false}}, nil
}

// A survey takes the speaker's radio, so a scan is a POST behind the origin guard and runs once
// for any number of callers at the same time.
func TestWifiScanIsGuardedAndSingleFlight(t *testing.T) {
	t.Parallel()

	scan := new(slowScan)
	srv, _, _ := radioServerWith(t, func(sp *countingBackend) Backend {
		scan.Backend = sp

		return scan
	})

	handler := srv.Handler()
	if rec := radioDo(t, handler, http.MethodGet, "/api/wifi/scan", ""); rec.Code == http.StatusOK {
		t.Fatal("GET still scans; a survey must be a guarded POST")
	}

	rec := radioDo(t, handler, http.MethodPost, "/api/wifi/scan", "",
		"Origin", "http://evil.example", "Sec-Fetch-Site", "cross-site")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site scan = %d, want 403", rec.Code)
	}

	if n := scan.n.Load(); n != 0 {
		t.Fatalf("refused scans still surveyed %d times", n)
	}

	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			rec := radioDo(t, handler, http.MethodPost, "/api/wifi/scan", "")
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "home") {
				t.Errorf("scan = %d %s", rec.Code, rec.Body)
			}
		})
	}

	wg.Wait()

	if n := scan.n.Load(); n != 1 {
		t.Fatalf("three concurrent scans surveyed %d times, want 1", n)
	}
}

// joinDeadline records how long setWifi lets the backend take.
type joinDeadline struct {
	Backend

	left time.Duration
}

func (b *joinDeadline) SetWifi(ctx context.Context, _, _, _ string) error {
	d, _ := ctx.Deadline()
	b.left = time.Until(d)

	return nil
}

// The stock-BCO join polls up to 45 s for the association; the old 6 s budget turned a
// correct password into "set wifi failed" and a speaker without Wi-Fi.
func TestSetWifiOutlastsTheBCOAssociation(t *testing.T) {
	t.Parallel()

	join := new(joinDeadline)
	srv, _, _ := radioServerWith(t, func(sp *countingBackend) Backend {
		join.Backend = sp

		return join
	})

	const body = `{"ssid":"home","security":"wpa_or_wpa2","password":"password1"}`

	rec := radioDo(t, srv.Handler(), http.MethodPost, "/api/wifi", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("set wifi = %d %s", rec.Code, rec.Body)
	}

	if join.left < 50*time.Second {
		t.Fatalf("join budget %v, want more than the 45 s association window", join.left)
	}
}
