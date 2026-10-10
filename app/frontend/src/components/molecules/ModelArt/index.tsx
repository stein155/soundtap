// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useId } from 'react';
import { Defs, St10, St20, St300, Wla } from './drawings';

type ModelKey = 'st10' | 'st20' | 'st300' | 'wla';

// Which drawing a speaker gets, from the model it reports. Unknown models get the
// ST10's.
const modelKey = (model?: string): ModelKey =>
  /Wireless Link/i.test(model || '')
    ? 'wla'
    : /300/.test(model || '')
      ? 'st300'
      : /20/.test(model || '')
        ? 'st20'
        : 'st10';

// Our own drawings of the speakers seen from the front, for the speaker bar and card.
// They fill the box they are put in.
export function ModelArt({ model }: { model?: string }) {
  const k = modelKey(model);
  const id = useId().replace(/:/g, '');
  const fill = (name: string) => `url(#${id}${name})`;
  return (
    <svg viewBox="0 0 100 100" aria-hidden="true">
      <Defs id={id} />
      {k === 'st10' && <St10 fill={fill} />}
      {k === 'st20' && <St20 fill={fill} />}
      {k === 'st300' && <St300 fill={fill} />}
      {k === 'wla' && <Wla fill={fill} />}
    </svg>
  );
}
