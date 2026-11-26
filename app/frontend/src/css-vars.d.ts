// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// eslint-disable-next-line react/no-typos -- a bare import makes this file a module, so the block below augments React's types
import 'react';

// A style may set a CSS custom property (style={{ '--wash': wash }}): the sheets read them.
declare module 'react' {
  interface CSSProperties extends Partial<Record<`--${string}`, string | number>> {}
}
