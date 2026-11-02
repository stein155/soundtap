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
	var out bytes.Buffer
	now := time.Unix(1000, 0)
	c := newCapWriter(&out, "ffmpeg", 10)
	c.now = func() time.Time { return now }
	for range 3 {
		if n, err := c.Write([]byte("abcdef")); n != 6 || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if out.String() != "abcdefabcd" {
		t.Fatalf("first minute = %q", out.String())
	}
	now = now.Add(time.Minute)
	_, _ = c.Write([]byte("xy"))
	if !strings.HasSuffix(out.String(), "ffmpeg: 8 bytes of output dropped\nxy") {
		t.Fatalf("next minute = %q", out.String())
	}
}
