// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Label } from './styled';

// "AUX", the design's glyph for the line-in input (there is no icon for it). `size` is
// its font size in px.
export function AuxLabel({ size = 10.5 }: { size?: number }) {
  return <Label $size={size}>AUX</Label>;
}
