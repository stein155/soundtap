// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package rootd

import (
	"slices"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/hw/model"
	"github.com/stein155/soundtap/app/internal/modeltable"
	"github.com/stein155/soundtap/app/oled"
)

// Every line soundtapd drives itself is one the helper exports for it: each board's keypad
// matrix and panel, taken from the code that drives them, and the ST20's amplifier fault
// line. A line added there and not here would leave a model without keys or panel.
func TestExportableGPIOCoversWhatSoundtapdDrives(t *testing.T) {
	t.Parallel()

	want := map[string][]gpioLine{"st20 amplifier fault": {{gpio1, 24}}}

	for _, value := range modeltable.Variants {
		module, err := model.ForVariant(value.Name)
		if err != nil {
			continue // a name only stock reports
		}

		if module.Name == model.ST10 || module.Name == model.ST20 {
			for _, l := range hw.KeypadLines(module.Name) {
				want[module.Name+" keypad"] = append(want[module.Name+" keypad"], gpioLine{l.Bank, l.Line})
			}
		}

		if !strings.HasPrefix(module.OLED, "spidev:") {
			continue
		}

		lines, err := oled.PanelLines(module.OLED)
		if err != nil {
			t.Fatalf("%s panel %q: %v", value.Name, module.OLED, err)
		}

		for _, l := range lines {
			want[value.Name+" panel"] = append(want[value.Name+" panel"], gpioLine{gpioBanks()[l[0]], l[1]})
		}
	}

	if len(want) < 5 {
		t.Fatalf("only %d sets of lines found: %v", len(want), want)
	}

	for what, lines := range want {
		for _, l := range lines {
			if !slices.Contains(exportableGPIO(), l) {
				t.Errorf("%s: bank %s line %d is not exportable", what, l.bank, l.line)
			}
		}
	}
}
