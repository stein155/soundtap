// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Limits the speaker enforces, mirrored so the UI stops input where the speaker would
// refuse it.

// A SoundTouch has six preset keys, and the API addresses them as slots 1..6.
export const PRESET_COUNT = 6;

// settings.NormalizeName accepts at most 63 bytes (one DNS-SD label). maxLength counts
// UTF-16 units, so a name with accents or emoji can still be refused by the speaker.
export const NAME_MAX_LENGTH = 63;
