// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import 'react';

// A style may set a CSS custom property (style={{ '--wash': wash }}): the sheets read them.
declare module 'react' {
  interface CSSProperties extends Partial<Record<`--${string}`, string | number>> {}
}
