// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package install_test

import (
	"testing"

	"github.com/stein155/soundtap/app/internal/install"
)

func TestModelFromVariants(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
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
		got, err := install.ModelFromVariants(test.variants...)
		if err != nil || got != test.want {
			t.Errorf("install.ModelFromVariants(%q) = %q, %v; want %q", test.variants, got, err, test.want)
		}
	}

	for _, bad := range [][]string{{"rhino", "lisa"}, {"bardeen", "rhino"}, {"lisax"}, {}} {
		got, err := install.ModelFromVariants(bad...)
		if err == nil {
			t.Errorf("install.ModelFromVariants(%q) = %q, want an error", bad, got)
		}
	}
}
