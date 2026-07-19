// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"sort"
	"strings"
)

func sortedSlots(m map[int]Preset) []int {
	slots := make([]int, 0, len(m))
	for k := range m {
		slots = append(slots, k)
	}
	sort.Ints(slots)
	return slots
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
