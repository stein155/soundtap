// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package supervise

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestCapWriterDropsPastTheBudgetAndSaysSo(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	now := time.Unix(1000, 0)
	writer := newCapWriter(&out, "ffmpeg", 10)

	writer.now = func() time.Time { return now }
	for range 3 {
		n, err := writer.Write([]byte("abcdef"))
		if n != 6 || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}

	if out.String() != "abcdefabcd" {
		t.Fatalf("first minute = %q", out.String())
	}

	now = now.Add(time.Minute)
	_, _ = writer.Write([]byte("xy"))

	if !strings.HasSuffix(out.String(), "ffmpeg: 8 bytes of output dropped\nxy") {
		t.Fatalf("next minute = %q", out.String())
	}
}
