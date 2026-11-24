// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// [id, screen, time ms, state]: every screen of the design once, plus the states the
// Go side has to get right at the edges (volume 0 and full, the clock's drift).
const CASES = [
  ["radio", "radio", 0, {}],
  ["spotify", "spotify", 60500, {}],
  ["volume", "volume", 0, { vol: 32 }],
  ["volume-0", "volume", 0, { vol: 0 }],
  ["volume-80", "volume", 0, { vol: 80 }],
  ["mute", "mute", 0, {}],
  ["preset", "preset", 0, { preset: 3, station: "Qmusic" }],
  ["bluetooth", "bluetooth", 0, {}],
  ["holding", "holding", 0, { holdN: 3, holdP: 0.6 }],
  ["saved", "saved", 0, {}],
  ["buffering", "buffering", 0, {}],
  ["aux", "aux", 0, {}],
  ["pairing", "pairing", 0, {}],
  ["standby", "standby", 0, {}],
  ["standby-drift", "standby", 180000, {}],
  ["standby-12h", "standby", 0, { h12: true }],
  ["offline", "offline", 0, {}],
];
