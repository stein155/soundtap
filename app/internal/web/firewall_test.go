// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/web"
)

// A production speaker whose filter did not load says so in /api/health and in the
// support bundle (ADR 0113): the boot goes on unfiltered, and nothing else shows it.

// errNftFailed is what a failed nft run returns.
var errNftFailed = errors.New("exit status 1")

func TestHealthAndBundleReportMissingFirewall(t *testing.T) {
	t.Parallel()

	profile := filepath.Join(t.TempDir(), "profile")

	err := os.WriteFile(profile, []byte("production\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	failing := func(context.Context, string, ...string) ([]byte, error) {
		return nil, errNftFailed
	}
	srv, _, _ := newServerSrv(t)
	srv.ReconfigureForTest(func(o *web.Options) {
		o.Health.Firewall = web.NewFirewallCheck(profile, failing, slog.New(slog.DiscardHandler))
	})

	var got struct {
		Firewall string   `json:"firewall"`
		Warnings []string `json:"warnings"`
	}
	decodeBody(t, do(t, srv.Handler(), "GET", "/api/health", ""), &got)

	if got.Firewall != "missing" {
		t.Errorf("firewall = %q, want missing", got.Firewall)
	}

	if !slices.ContainsFunc(
		got.Warnings,
		func(w string) bool { return strings.Contains(w, "firewall is not loaded") },
	) {
		t.Errorf("no firewall warning in %q", got.Warnings)
	}

	body := do(t, srv.Handler(), "GET", "/api/debug", "").Body.String()
	if !strings.Contains(body, "firewall   : missing (nft: exit status 1)") {
		t.Errorf("the support bundle does not report the missing filter:\n%s", body)
	}
}

// Off-device nothing is wired: the field is there and says n/a, with no warning.
func TestHealthFirewallNotApplicableOffDevice(t *testing.T) {
	t.Parallel()

	handler, _ := newServer(t)

	var got struct {
		Firewall string   `json:"firewall"`
		Warnings []string `json:"warnings"`
	}
	decodeBody(t, do(t, handler, "GET", "/api/health", ""), &got)

	if got.Firewall != "n/a" {
		t.Errorf("firewall = %q, want n/a", got.Firewall)
	}

	for _, w := range got.Warnings {
		if strings.Contains(w, "firewall") {
			t.Errorf("unexpected firewall warning %q", w)
		}
	}
}
