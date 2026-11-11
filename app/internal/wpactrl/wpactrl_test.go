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
	c, err := wpactrl.Dial(srv.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, srv, dir
}

func TestRequestsGetTheirReplies(t *testing.T) {
	ids := 0
	c, srv, _ := dial(t, func(cmd string) string {
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
	st, err := c.Status(ctx)
	if err != nil || st["ssid"] != " my net " || st["wpa_state"] != "COMPLETED" || st["id"] != "1" {
		t.Fatalf("status = %q, %v", st, err)
	}
	if id, err := c.AddNetwork(ctx); err != nil || id != 0 {
		t.Fatalf("add = %d, %v", id, err)
	}
	if err := c.SetNetwork(ctx, 0, "ssid", "686f6d65"); err != nil {
		t.Fatal(err)
	}
	err = c.SetNetwork(ctx, 0, "psk", "secret-key-value")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("a refused SET_NETWORK = %v; want an error without the value", err)
	}
	for _, f := range []func(context.Context) error{
		func(ctx context.Context) error { return c.EnableNetwork(ctx, 0) },
		func(ctx context.Context) error { return c.SelectNetwork(ctx, 0) },
		c.RemoveAllNetworks, c.Reconfigure, c.Scan,
	} {
		if err := f(ctx); err != nil {
			t.Fatal(err)
		}
	}
	bss, err := c.ScanResults(ctx)
	if err != nil || len(bss) != 1 || bss[0].SSID != "home net " || bss[0].Signal != -50 || bss[0].Freq != 2437 {
		t.Fatalf("scan results = %+v, %v", bss, err)
	}
	sig, err := c.SignalPoll(ctx)
	if err != nil || sig["RSSI"] != "-61" {
		t.Fatalf("signal = %q, %v", sig, err)
	}
	if !c.Ping(ctx) {
		t.Fatal("no PONG")
	}
	if !srv.Has("REMOVE_NETWORK all") {
		t.Fatal("server missed a command")
	}
}

func TestEventsArriveAfterAttach(t *testing.T) {
	c, srv, _ := dial(t, nil)
	ctx := context.Background()
	if err := c.Attach(ctx); err != nil {
		t.Fatal(err)
	}
	srv.Event(`CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="home" auth_failures=1 duration=10 reason=WRONG_KEY`)
	srv.Event("CTRL-EVENT-CONNECTED - Connection to aa:bb:cc:dd:ee:ff completed [id=2 id_str=]")
	var got []wpactrl.Event
	for len(got) < 2 {
		select {
		case ev := <-c.Events():
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
	if !c.Ping(ctx) {
		t.Fatal("no PONG after events")
	}
}

func TestParseEventRejectsReplies(t *testing.T) {
	for _, s := range []string{"OK\n", "FAIL", "<x>nope", "bssid / frequency"} {
		if _, ok := wpactrl.ParseEvent(s); ok {
			t.Errorf("%q read as an event", s)
		}
	}
}

func TestAGoneSupplicantTimesOutAndCloseCleansUp(t *testing.T) {
	c, srv, dir := dial(t, nil)
	srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if c.Ping(ctx) {
		t.Fatal("a closed server answered")
	}
	c.Close()
	if _, err := c.Request(context.Background(), "PING"); !errors.Is(err, wpactrl.ErrClosed) {
		t.Fatalf("request after close = %v", err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "wpa_ctrl_*"))
	if len(left) != 0 {
		t.Fatalf("client sockets left behind: %v", left)
	}
}

func TestDialWithoutSupplicantFails(t *testing.T) {
	dir := wpactrltest.ShortDir(t)
	if _, err := wpactrl.Dial(filepath.Join(dir, "wlan0")); err == nil {
		t.Fatal("dial to nothing succeeded")
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("a failed dial left %v", left)
	}
}

func TestMultiLineCommandIsRefused(t *testing.T) {
	c, _, _ := dial(t, nil)
	if _, err := c.Request(context.Background(), "SET_NETWORK 0 ssid x\nREMOVE_NETWORK all"); err == nil {
		t.Fatal("a command with a newline went out")
	}
}
