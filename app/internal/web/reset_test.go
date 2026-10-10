// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"testing"

	"github.com/stein155/soundtap/app/internal/web"
)

func TestFactoryResetClearsPresetsAndSettings(t *testing.T) {
	h, _ := newServer(t)
	do(t, h, "PUT", "/api/presets/1", `{"stationId":"s111","name":"One"}`)
	do(t, h, "PUT", "/api/settings", `{"name":"Keuken","language":"nl"}`)

	if rec := do(t, h, "POST", "/api/factory-reset", `{"confirm":true}`); rec.Code != 200 {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	var after exported
	decodeBody(t, do(t, h, "GET", "/api/backup", ""), &after)
	if len(after.Presets) != 0 {
		t.Errorf("presets survived the reset: %+v", after.Presets)
	}
	if after.Settings.Language != "en" {
		t.Errorf("language = %q, want the default", after.Settings.Language)
	}
	// The name goes too. A box that still answers to "Keuken" after a factory reset
	// has not been reset; soundtapd derives the out-of-box name again on restart.
	if after.Settings.Name != "" {
		t.Errorf("name = %q, want it cleared", after.Settings.Name)
	}
}

func TestFactoryResetAlwaysClearsSettings(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	hardwareReset := false
	srv.ReconfigureForTest(func(o *web.Options) { o.Setup.FactoryReset = func() error { hardwareReset = true; return nil } })
	h := srv.Handler()
	do(t, h, "PUT", "/api/presets/1", `{"stationId":"s111","name":"One"}`)
	do(t, h, "PUT", "/api/settings", `{"language":"nl"}`)

	if rec := do(t, h, "POST", "/api/factory-reset", `{"confirm":true}`); rec.Code != 200 {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	var after exported
	decodeBody(t, do(t, h, "GET", "/api/backup", ""), &after)
	if len(after.Presets) != 0 {
		t.Errorf("presets survived: %+v", after.Presets)
	}
	if after.Settings.Language != "en" {
		t.Errorf("language = %q, want the default", after.Settings.Language)
	}
	if !hardwareReset {
		t.Error("factory reset did not run the on-speaker reset")
	}
}

// A POST that never said "yes" must not wipe anything.
func TestFactoryResetNeedsConfirmation(t *testing.T) {
	h, _ := newServer(t)
	do(t, h, "PUT", "/api/presets/1", `{"stationId":"s111","name":"One"}`)

	for _, body := range []string{`{}`, `{"confirm":false}`, `{"keepSettings":true}`} {
		if rec := do(t, h, "POST", "/api/factory-reset", body); rec.Code != 400 {
			t.Fatalf("body %s gave %d, want 400", body, rec.Code)
		}
	}
	var after exported
	decodeBody(t, do(t, h, "GET", "/api/backup", ""), &after)
	if len(after.Presets) != 1 {
		t.Fatalf("an unconfirmed reset cleared presets: %+v", after.Presets)
	}
}
