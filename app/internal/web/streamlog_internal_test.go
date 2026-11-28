// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// lockedBuffer is a log sink the handlers may write from more than one goroutine.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("log buffer: %w", err)
	}

	return n, nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// A stream of the user's own may carry its login in the URL. Playing it or saving it to
// a preset logs the station, and credentials never reach the log.
func TestOwnStreamCredentialsStayOutOfTheLog(t *testing.T) {
	t.Parallel()

	const stream = "http://listener:s3cret@10.0.0.5:8000/live?token=t0ken"

	for _, req := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/play", `{"stationId":"` + stream + `","name":"Mine"}`},
		{
			http.MethodPut, "/api/presets/1",
			`{"stationId":"` + stream + `","name":"Mine","logo":"http://10.0.0.5/logo.png"}`,
		},
	} {
		srv, _, _ := radioServer(t)

		var logged lockedBuffer

		srv.log = slog.New(slog.NewTextHandler(&logged, nil))

		rec := radioDo(t, srv.Handler(), req.method, req.path, req.body, "Content-Type", "application/json")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s = %d %s", req.method, req.path, rec.Code, rec.Body)
		}

		out := logged.String()
		if !strings.Contains(out, "10.0.0.5:8000/live") {
			t.Errorf("%s %s: the station is not logged at all: %q", req.method, req.path, out)
		}

		for _, secret := range []string{"s3cret", "listener", "t0ken"} {
			if strings.Contains(out, secret) {
				t.Errorf("%s %s logged %q: %q", req.method, req.path, secret, out)
			}
		}
	}
}
