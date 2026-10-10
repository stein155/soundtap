// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

const SIGNAL_BARS: Record<string, number> = { poor: 1, fair: 2, good: 3, excellent: 4 };

// Signal strength as lit and dimmed arcs, so a network reads at a glance.
export function SignalGlyph({ signal }: { signal: string }) {
  const level = SIGNAL_BARS[signal] || 0;
  const opacity = (bar: number) => (level >= bar ? 1 : 0.22);
  return (
    <svg
      width="19"
      height="19"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M2.5 8.5a15 15 0 0 1 19 0" opacity={opacity(4)} />
      <path d="M5.5 12a10.5 10.5 0 0 1 13 0" opacity={opacity(3)} />
      <path d="M8.5 15.5a6 6 0 0 1 7 0" opacity={opacity(2)} />
      <circle cx="12" cy="19" r="1" fill="currentColor" stroke="none" opacity={opacity(1)} />
    </svg>
  );
}
