// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stein155/soundtap/app/internal/btctl"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/web"
)

// Forgetting one paired device asks BlueZ for that address alone; a connected one that was
// streaming leaves the speaker BLUETOOTH but no longer playing, as a clear does.

// errBluetoothdGone is a Bluetooth daemon that stopped answering.
var errBluetoothdGone = errors.New("bluetoothd is gone")

func TestBluetoothForgetOneDevice(t *testing.T) {
	t.Parallel()

	srv, _, _ := newServerSrv(t)

	var asked []string

	srv.ReconfigureForTest(func(o *web.Options) {
		o.Bluetooth.Forget = func(_ context.Context, address string) (bool, error) {
			asked = append(asked, address)
			if address != "60:F4:45:11:22:33" {
				return false, btctl.ErrNotPaired
			}

			return true, nil
		}
	})
	handler := srv.Handler()
	doLoopbackCT(t, handler, "POST", "/api/source", "application/json", `{"source":"BLUETOOTH"}`)
	doLoopbackCT(t, handler, "POST", "/api/source", "application/json", `{"source":"BLUETOOTH","event":"play"}`)

	for body, want := range map[string]int{
		`{"address":"60:F4:45:11:22:33"}`:            http.StatusOK,
		`{"address":"AA:BB:CC:DD:EE:FF"}`:            http.StatusNotFound,
		`{"address":"phone"}`:                        http.StatusBadRequest,
		`{"address":"60:F4:45:11:22:33","all":true}`: http.StatusBadRequest,
		``: http.StatusBadRequest,
	} {
		if rec := do(t, handler, "POST", "/api/bluetooth/forget", body); rec.Code != want {
			t.Errorf("forget %s = %d (%s), want %d", body, rec.Code, rec.Body, want)
		}
	}

	if len(asked) != 2 {
		t.Errorf("BlueZ was asked for %v, want only the two well-formed addresses", asked)
	}

	var np speaker.NowPlaying
	decodeBody(t, do(t, handler, "GET", "/api/now", ""), &np)

	if np.Source != "BLUETOOTH" || np.PlayStatus == "PLAY_STATE" {
		t.Fatalf("after forgetting the connected phone = %+v, want BLUETOOTH and not playing", np)
	}
}

// The body and the pairing are checked before the press is asked for: a bad or stale address
// is refused at once, without a walk to the speaker, and only a paired one waits for the
// press. A pairing that goes while the speaker waits is still a 404.
func TestBluetoothForgetChecksBeforeThePress(t *testing.T) {
	t.Parallel()

	srv, _, _ := newServerSrv(t)
	paired := map[string]bool{"60:F4:45:11:22:33": true}

	var presses, forgets int

	press := error(nil)

	srv.ReconfigureForTest(func(opts *web.Options) {
		opts.Bluetooth.Paired = func(_ context.Context, address string) (bool, error) {
			if address == "11:11:11:11:11:11" {
				return false, errBluetoothdGone
			}

			return paired[address], nil
		}
		opts.Bluetooth.Forget = func(_ context.Context, address string) (bool, error) {
			forgets++

			if !paired[address] {
				return false, btctl.ErrNotPaired
			}

			return false, nil
		}
		opts.Panel.Confirm = func(context.Context, string, string) error {
			presses++

			return press
		}
	})
	handler := srv.Handler()

	for _, testCase := range []struct {
		body           string
		want, wantAsks int
	}{
		{`{"address":"phone"}`, http.StatusBadRequest, 0},
		{`{"address":"60:F4:45:11:22:33","all":true}`, http.StatusBadRequest, 0},
		{`{"address":"AA:BB:CC:DD:EE:FF"}`, http.StatusNotFound, 0},
		{`{"address":"11:11:11:11:11:11"}`, http.StatusInternalServerError, 0},
		{`{"address":"60:F4:45:11:22:33"}`, http.StatusOK, 1},
	} {
		presses, forgets = 0, 0
		if rec := do(t, handler, "POST", "/api/bluetooth/forget", testCase.body); rec.Code != testCase.want ||
			presses != testCase.wantAsks ||
			forgets != testCase.wantAsks {
			t.Errorf(
				"forget %s = %d after %d presses and %d forgets, want %d after %d",
				testCase.body,
				rec.Code,
				presses,
				forgets,
				testCase.want,
				testCase.wantAsks,
			)
		}
	}

	// No press: nothing is forgotten.
	press, presses, forgets = web.ErrNotConfirmed, 0, 0

	rec := do(t, handler, "POST", "/api/bluetooth/forget", `{"address":"60:F4:45:11:22:33"}`)
	if rec.Code != http.StatusForbidden ||
		forgets != 0 {
		t.Errorf("forget without the press = %d after %d forgets, want 403 and none", rec.Code, forgets)
	}

	// Paired when asked, gone by the press.
	press = nil

	srv.ReconfigureForTest(func(o *web.Options) {
		o.Panel.Confirm = func(context.Context, string, string) error {
			delete(paired, "60:F4:45:11:22:33")

			return nil
		}
	})

	rec = do(t, handler, "POST", "/api/bluetooth/forget", `{"address":"60:F4:45:11:22:33"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("forget of a pairing gone during the press = %d, want 404", rec.Code)
	}
}

// A speaker without Bluetooth has nothing to forget.
func TestBluetoothForgetWithoutBluetooth(t *testing.T) {
	t.Parallel()

	h, _ := newServer(t)

	rec := do(t, h, "POST", "/api/bluetooth/forget", `{"address":"60:F4:45:11:22:33"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("forget without Bluetooth = %d, want 400", rec.Code)
	}
}
