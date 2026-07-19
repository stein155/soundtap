// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"testing"
)

func TestChangedFiresOnStateChangeOnly(t *testing.T) {
	ctx := context.Background()
	s := New()
	ch := s.Changed()
	s.StatusSnapshot() // a read is not a change
	select {
	case <-ch:
		t.Fatal("Changed fired on a read")
	default:
	}
	if err := s.SetSource(ctx, "SPOTIFY"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	default:
		t.Fatal("Changed did not fire on a source change")
	}
	ch = s.Changed()
	if err := s.SetVolume(ctx, 12); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	default:
		t.Fatal("Changed did not fire on a volume change")
	}
}
