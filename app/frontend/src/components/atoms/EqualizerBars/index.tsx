// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Bars } from './styled';

// Four animated bars: "this is playing right now". `size` is their height in px.
export function EqualizerBars({ size = 10 }: { size?: number }) {
  return (
    <Bars $height={size} aria-hidden="true">
      <i />
      <i />
      <i />
      <i />
    </Bars>
  );
}
