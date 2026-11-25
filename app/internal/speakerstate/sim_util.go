// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"slices"
	"strings"
)

func sortedSlots(m map[int]Preset) []int {
	slots := make([]int, 0, len(m))
	for k := range m {
		slots = append(slots, k)
	}

	slices.Sort(slots)

	return slots
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}

	if value > high {
		return high
	}

	return value
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}

	return v
}
