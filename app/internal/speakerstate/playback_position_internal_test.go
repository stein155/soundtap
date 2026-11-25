// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"testing"
	"time"
)

func TestPositionStopsWhereThePauseFoundIt(t *testing.T) {
	t.Parallel()

	spk := New()

	err := spk.Select(t.Context(), "AIRPLAY", "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	spk.SetPosition("AIRPLAY", 1000, 60000)
	spk.lock()
	spk.positionAt = spk.positionAt.Add(-5 * time.Second)
	spk.unlock()
	spk.SetPaused("AIRPLAY", true)

	_, pos, dur := spk.Position()
	if pos < 6000 || pos > 7000 || dur != 60000 {
		t.Fatalf("paused at %d of %d, want about 6000", pos, dur)
	}

	spk.SetPaused("AIRPLAY", false)

	if _, again, _ := spk.Position(); again < pos || again > pos+1000 {
		t.Fatalf("resumed at %d after pausing at %d", again, pos)
	}
}
