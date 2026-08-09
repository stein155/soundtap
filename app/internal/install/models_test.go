// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package install

import "testing"

func TestModelFromVariants(t *testing.T) {
	for _, tc := range []struct {
		variants []string
		want     string
	}{
		{[]string{"lisa", "spotty"}, "st20"},
		{[]string{" LISA\n"}, "st20"},
		{[]string{"rhino"}, "st10"},
		{[]string{"binky"}, "wla"},
		{[]string{"ginger"}, "st300"},
		{[]string{"mojo"}, "st20"}, // SoundTouch 30 Series I: the ST20's board
	} {
		if got, err := ModelFromVariants(tc.variants...); err != nil || got != tc.want {
			t.Errorf("ModelFromVariants(%q) = %q, %v; want %q", tc.variants, got, err, tc.want)
		}
	}
	for _, bad := range [][]string{{"rhino", "lisa"}, {"bardeen", "rhino"}, {"lisax"}, {}} {
		if got, err := ModelFromVariants(bad...); err == nil {
			t.Errorf("ModelFromVariants(%q) = %q, want an error", bad, got)
		}
	}
}
