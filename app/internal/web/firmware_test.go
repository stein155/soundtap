// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"net/http"
	"strings"
	"testing"
)

// Anything that is not a package is refused with a reason, not swallowed.
func TestFirmwareUploadRejectsRubbish(t *testing.T) {
	t.Parallel()

	h, _ := newServer(t)

	rec := do(t, h, "POST", "/api/firmware", strings.Repeat("<html>404 not found</html>", 40))
	if rec.Code != http.StatusUnsupportedMediaType && rec.Code != http.StatusBadRequest {
		t.Fatalf("rubbish upload = %d %s", rec.Code, rec.Body)
	}

	if strings.TrimSpace(rec.Body.String()) == "" {
		t.Error("refused without saying why")
	}
}
