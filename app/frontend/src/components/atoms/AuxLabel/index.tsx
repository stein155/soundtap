// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Label } from './styled';
import type { ReactElement } from 'react';

// "AUX", the design's glyph for the line-in input (there is no icon for it). `size` is
// its font size in px.
export function AuxLabel({ size = 10.5 }: { size?: number }): ReactElement {
  return <Label $size={size}>AUX</Label>;
}
