// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/upnp"
)

// A media fetch follows a redirect only to another MediaURL, and never dials loopback,
// whatever the URL or the DNS answer said.
func TestMediaClientStaysOffLoopback(t *testing.T) {
	t.Parallel()

	client := upnp.MediaClient(time.Second)

	ctx := context.Background()
	for target, allowed := range map[string]bool{
		"http://192.168.1.9/radio.pls": true, "http://127.0.0.1/api/info": false,
		"http://localhost/x": false, "http://169.254.169.254/latest": false, "file:///etc/passwd": false,
	} {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil)

		err := client.CheckRedirect(req, nil)
		if (err == nil) != allowed {
			t.Errorf("redirect to %s: err %v, want allowed=%v", target, err, allowed)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the loopback server was reached")
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)

	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()

		t.Fatal("dialled loopback")
	}
}
