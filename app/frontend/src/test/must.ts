// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// A value the test needs present: returns it, or fails the test with what was missing.
// The narrowing a `!` would only assert, checked.
export function must<T>(value: T | null | undefined, what = 'value'): T {
  if (value === null || value === undefined)
    throw new Error(`test needs ${what}, got ${String(value)}`);
  return value;
}
