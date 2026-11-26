// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package safehttp

import (
	"crypto/x509"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

//nolint:paralleltest // swaps package variables Do reads: now, clockPollInterval, clockWaitLimit
func TestDoRetriesCertificateErrorAfterClockBecomesSane(t *testing.T) {
	oldNow, oldPoll, oldLimit := now, clockPollInterval, clockWaitLimit

	t.Cleanup(func() {
		now, clockPollInterval, clockWaitLimit = oldNow, oldPoll, oldLimit
	})

	var clockReads atomic.Int32

	now = func() time.Time {
		if clockReads.Add(1) > 1 {
			return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		}

		return time.Unix(0, 0)
	}
	clockPollInterval, clockWaitLimit = time.Millisecond, time.Second

	var requests int

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return nil, x509.CertificateInvalidError{
				Cert:   &x509.Certificate{NotBefore: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
				Reason: x509.Expired,
			}
		}

		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := Do(t.Context(), client, req)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}
