// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Trim a value that should be a string, and treat anything else as empty. Speaker
// responses are JSON from firmware we do not fully control.
export const clean = (value: unknown): string => (typeof value === 'string' ? value.trim() : '');
