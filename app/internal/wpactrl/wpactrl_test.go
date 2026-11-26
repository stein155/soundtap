// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wpactrl_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/wpactrl"
	"github.com/stein155/soundtap/app/internal/wpactrl/wpactrltest"
)

func dial(t *testing.T, handle func(string) string) (*wpactrl.Conn, *wpactrltest.Server, string) {
	t.Helper()
	dir := wpactrltest.ShortDir(t)
	srv := wpactrltest.Listen(t, dir, "wlan0", handle)

	conn, err := wpactrl.Dial(srv.Path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { conn.Close() })

	return conn, srv, dir
}

//nolint:paralleltest // wpactrltest.ShortDir points wpactrl.ClientDir, a package variable, at this test's directory
func TestRequestsGetTheirReplies(t *testing.T) {
	ids := 0
	conn, srv, _ := dial(t, func(cmd string) string {
		switch {
		case cmd == "STATUS":
			return "bssid=aa:bb:cc:dd:ee:ff\nssid= my net \nid=1\nmode=station\nwpa_state=COMPLETED\n"
		case cmd == "ADD_NETWORK":
			ids++

			return string(rune('0'+ids-1)) + "\n"
		case strings.HasPrefix(cmd, "SET_NETWORK 0 ssid "), cmd == "ENABLE_NETWORK 0", cmd == "SELECT_NETWORK 0",
			cmd == "REMOVE_NETWORK all", cmd == "RECONFIGURE":
			return "OK\n"
		case strings.HasPrefix(cmd, "SET_NETWORK"):
			return "FAIL\n"
		case cmd == "SCAN":
			return "FAIL-BUSY\n"
		case cmd == "SCAN_RESULTS":
			return "bssid / frequency / signal level / flags / ssid\n" +
				"aa:bb:cc:dd:ee:01\t2437\t-50\t[WPA2-PSK-CCMP][ESS]\thome net \n" +
				"aa:bb:cc:dd:ee:02\t2412\tx\t[ESS]\tbroken\n"
		case cmd == "SIGNAL_POLL":
			return "RSSI=-61\nLINKSPEED=65\nNOISE=9999\nFREQUENCY=2437\n"
		}

		return ""
	})
	ctx := context.Background()

	st, err := conn.Status(ctx)
	if err != nil || st["ssid"] != " my net " || st["wpa_state"] != "COMPLETED" || st["id"] != "1" {
		t.Fatalf("status = %q, %v", st, err)
	}

	id, err := conn.AddNetwork(ctx)
	if err != nil || id != 0 {
		t.Fatalf("add = %d, %v", id, err)
	}

	err = conn.SetNetwork(ctx, 0, "ssid", "686f6d65")
	if err != nil {
		t.Fatal(err)
	}

	err = conn.SetNetwork(ctx, 0, "psk", "secret-key-value")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("a refused SET_NETWORK = %v; want an error without the value", err)
	}

	for _, f := range []func(context.Context) error{
		func(ctx context.Context) error { return conn.EnableNetwork(ctx, 0) },
		func(ctx context.Context) error { return conn.SelectNetwork(ctx, 0) },
		conn.RemoveAllNetworks, conn.Reconfigure, conn.Scan,
	} {
		err = f(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}

	bss, err := conn.ScanResults(ctx)
	if err != nil || len(bss) != 1 || bss[0].SSID != "home net " || bss[0].Signal != -50 || bss[0].Freq != 2437 {
		t.Fatalf("scan results = %+v, %v", bss, err)
	}

	sig, err := conn.SignalPoll(ctx)
	if err != nil || sig["RSSI"] != "-61" {
		t.Fatalf("signal = %q, %v", sig, err)
	}

	if !conn.Ping(ctx) {
		t.Fatal("no PONG")
	}

	if !srv.Has("REMOVE_NETWORK all") {
		t.Fatal("server missed a command")
	}
}

//nolint:paralleltest // wpactrltest.ShortDir points wpactrl.ClientDir, a package variable, at this test's directory
func TestEventsArriveAfterAttach(t *testing.T) {
	conn, srv, _ := dial(t, nil)

	ctx := context.Background()

	err := conn.Attach(ctx)
	if err != nil {
		t.Fatal(err)
	}

	srv.Event(`CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="home" auth_failures=1 duration=10 reason=WRONG_KEY`)
	srv.Event("CTRL-EVENT-CONNECTED - Connection to aa:bb:cc:dd:ee:ff completed [id=2 id_str=]")

	var got []wpactrl.Event
	for len(got) < 2 {
		select {
		case ev := <-conn.Events():
			got = append(got, ev)
		case <-time.After(2 * time.Second):
			t.Fatalf("events = %+v", got)
		}
	}

	if got[0].Name != "CTRL-EVENT-SSID-TEMP-DISABLED" || got[0].Level != 3 {
		t.Fatalf("first event = %+v", got[0])
	}

	if r, _ := got[0].Field("reason"); r != "WRONG_KEY" {
		t.Fatalf("reason = %q", r)
	}

	if s, _ := got[0].Field("ssid"); s != "home" {
		t.Fatalf("ssid = %q", s)
	}

	if id, ok := got[1].Field("id"); !ok || id != "2" {
		t.Fatalf("id = %q, %v", id, ok)
	}
	// A request still gets its reply while events flow.
	if !conn.Ping(ctx) {
		t.Fatal("no PONG after events")
	}
}

func TestParseEventRejectsReplies(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"OK\n", "FAIL", "<x>nope", "bssid / frequency"} {
		if _, ok := wpactrl.ParseEvent(s); ok {
			t.Errorf("%q read as an event", s)
		}
	}
}

//nolint:paralleltest // wpactrltest.ShortDir points wpactrl.ClientDir, a package variable, at this test's directory
func TestAGoneSupplicantTimesOutAndCloseCleansUp(t *testing.T) {
	conn, srv, dir := dial(t, nil)
	srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if conn.Ping(ctx) {
		t.Fatal("a closed server answered")
	}

	conn.Close()

	_, err := conn.Request(context.Background(), "PING")
	if !errors.Is(err, wpactrl.ErrClosed) {
		t.Fatalf("request after close = %v", err)
	}

	left, _ := filepath.Glob(filepath.Join(dir, "wpa_ctrl_*"))
	if len(left) != 0 {
		t.Fatalf("client sockets left behind: %v", left)
	}
}

//nolint:paralleltest // wpactrltest.ShortDir points wpactrl.ClientDir, a package variable, at this test's directory
func TestDialWithoutSupplicantFails(t *testing.T) {
	dir := wpactrltest.ShortDir(t)

	_, err := wpactrl.Dial(filepath.Join(dir, "wlan0"))
	if err == nil {
		t.Fatal("dial to nothing succeeded")
	}

	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("a failed dial left %v", left)
	}
}

//nolint:paralleltest // wpactrltest.ShortDir points wpactrl.ClientDir, a package variable, at this test's directory
func TestMultiLineCommandIsRefused(t *testing.T) {
	conn, _, _ := dial(t, nil)

	_, err := conn.Request(context.Background(), "SET_NETWORK 0 ssid x\nREMOVE_NETWORK all")
	if err == nil {
		t.Fatal("a command with a newline went out")
	}
}

// A reply that comes after its request gave up is not taken as the next request's answer.
//
//nolint:paralleltest // wpactrltest.ShortDir points wpactrl.ClientDir, a package variable, at this test's directory
func TestALateReplyIsNotTheNextOnes(t *testing.T) {
	conn, _, _ := dial(t, func(cmd string) string {
		if cmd == "STATUS" {
			time.Sleep(300 * time.Millisecond) // slow, as during a scan

			return "wpa_state=COMPLETED\n"
		}

		return ""
	})

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := conn.Status(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("STATUS err = %v, want a timeout", err)
	}

	pctx, pcancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer pcancel()

	if !conn.Ping(pctx) {
		t.Fatal("PING took STATUS's late reply for its own")
	}
}
