// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"testing"
	"time"
)

func TestPositionStopsWhereThePauseFoundIt(t *testing.T) {
	s := New()
	if err := s.Select(t.Context(), "AIRPLAY", "", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	s.SetPosition("AIRPLAY", 1000, 60000)
	s.lock()
	s.positionAt = s.positionAt.Add(-5 * time.Second)
	s.unlock()
	s.SetPaused("AIRPLAY", true)
	_, pos, dur := s.Position()
	if pos < 6000 || pos > 7000 || dur != 60000 {
		t.Fatalf("paused at %d of %d, want about 6000", pos, dur)
	}
	s.SetPaused("AIRPLAY", false)
	if _, again, _ := s.Position(); again < pos || again > pos+1000 {
		t.Fatalf("resumed at %d after pausing at %d", again, pos)
	}
}
