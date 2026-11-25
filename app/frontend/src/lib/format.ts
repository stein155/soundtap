// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Display formats shared across the settings pages.

// Binary units, the same ones the support bundle prints, so a screenshot and a pasted
// bundle agree. Decimals follow the language of the page: 1,5 MiB in Dutch.
export function formatBytes(bytes: number, lang?: string): string {
  const one = (n: number) =>
    new Intl.NumberFormat(lang, { minimumFractionDigits: 1, maximumFractionDigits: 1 }).format(n);
  if (bytes >= 1 << 30) return `${one(bytes / (1 << 30))} GiB`;
  if (bytes >= 1 << 20) return `${one(bytes / (1 << 20))} MiB`;
  if (bytes >= 1 << 10) return `${one(bytes / (1 << 10))} KiB`;
  return `${bytes} B`;
}

// Bass with its sign: "+2", "0", "−3" (a minus sign, not a hyphen).
export function formatBass(level: number): string {
  if (level > 0) return `+${level}`;
  if (level < 0) return `−${-level}`;
  return '0';
}

// "Bose SoundTouch 10" from the model the speaker reports; the brand is not translated.
export const modelLabel = (model?: string): string => (model ? `Bose ${model}` : '');

// The line drawing of a speaker's model, for lists of speakers; any other model gets the
// generic speaker.
export const modelIcon = (model?: string): 'st10' | 'st20' | 'st300' | 'wla' | 'speaker' => {
  if (/Wireless Link/i.test(model || '')) return 'wla';
  if (/\b300\b/.test(model || '')) return 'st300';
  // The SoundTouch 30 is the ST20's board in a bigger box, and looks like it.
  if (/\b(20|30)\b/.test(model || '')) return 'st20';
  if (/\b10\b/.test(model || '')) return 'st10';
  return 'speaker';
};
