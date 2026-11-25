// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/web"
)

func TestSavedWifiListAndForget(t *testing.T) {
	t.Parallel()

	srv, _, _ := newServerSrv(t)

	handler := srv.Handler()

	rec := do(t, handler, "GET", "/api/wifi/saved", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"networks":[]`) {
		t.Fatalf("unwired list: %d %s", rec.Code, rec.Body)
	}

	var forgot string

	srv.ReconfigureForTest(func(o *web.Options) {
		o.Setup.WifiProfiles = func() ([]web.WifiProfile, error) {
			return []web.WifiProfile{{ID: "aG9tZQ==", SSID: "home"}}, nil
		}
		o.Setup.ForgetWifi = func(id string) error {
			if id == "cur" {
				return web.ErrWifiProfileCurrent
			}

			forgot = id

			return nil
		}
	})

	rec = do(t, handler, "GET", "/api/wifi/saved", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ssid":"home"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	if rec := do(t, handler, "POST", "/api/wifi/forget", `{"id":"aG9tZQ=="}`); rec.Code != 200 || forgot != "aG9tZQ==" {
		t.Fatalf("forget: %d %s", rec.Code, rec.Body)
	}

	if rec := do(t, handler, "POST", "/api/wifi/forget", `{"id":"cur"}`); rec.Code != http.StatusConflict {
		t.Fatalf("forget current = %d", rec.Code)
	}

	if rec := do(t, handler, "POST", "/api/wifi/forget", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("forget without id = %d", rec.Code)
	}
}

//nolint:paralleltest // points the package-level join-failure path at a temp file
func TestSavedWifiSaysWhyTheJoinFailed(t *testing.T) {
	file := filepath.Join(t.TempDir(), "wifi-join-failed")
	t.Cleanup(web.SetWifiJoinFailedFileForTest(file))
	srv, _, _ := newServerSrv(t)

	handler := srv.Handler()
	if rec := do(t, handler, "GET", "/api/wifi/saved", ""); strings.Contains(rec.Body.String(), "joinFailed") {
		t.Fatalf("no failure yet: %s", rec.Body)
	}

	err := os.WriteFile(file, []byte("wrong_password\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	rec := do(t, handler, "GET", "/api/wifi/saved", "")
	if !strings.Contains(rec.Body.String(), `"joinFailed":"wrong_password"`) {
		t.Fatalf("failure: %s", rec.Body)
	}
}

//nolint:paralleltest // points the package-level fallback-notice path at a temp file
func TestSavedWifiSaysWhyNewSettingsFellBackUntilDismissed(t *testing.T) {
	file := filepath.Join(t.TempDir(), "wifi-fallback-reason")
	t.Cleanup(web.SetWifiFallbackFileForTest(file))
	srv, _, _ := newServerSrv(t)

	handler := srv.Handler()
	if rec := do(t, handler, "GET", "/api/wifi/saved", ""); strings.Contains(rec.Body.String(), "fellBack") {
		t.Fatalf("no fallback yet: %s", rec.Body)
	}

	err := os.WriteFile(file, []byte("wrong_password\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	rec := do(t, handler, "GET", "/api/wifi/saved", "")
	if !strings.Contains(rec.Body.String(), `"fellBack":"wrong_password"`) {
		t.Fatalf("fallback: %s", rec.Body)
	}

	for range 2 { // dismissing twice is fine
		if rec := do(t, handler, "POST", "/api/wifi/fallback/dismiss", ""); rec.Code != 200 {
			t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
		}
	}

	if rec := do(t, handler, "GET", "/api/wifi/saved", ""); strings.Contains(rec.Body.String(), "fellBack") {
		t.Fatalf("still shown: %s", rec.Body)
	}
}
