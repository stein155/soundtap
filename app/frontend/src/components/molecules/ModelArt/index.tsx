// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useId, type ReactElement } from 'react';
import { Defs, St10, St20, St300, Wla } from './drawings';

type ModelKey = 'st10' | 'st20' | 'st300' | 'wla';

// Which drawing a speaker gets, from the model it reports: the speaker's own type
// ("SoundTouch Wireless Link Adapter") or its short name ("WLA", "ST300"). Unknown models
// get the ST10's.
export function modelKey(model = ''): ModelKey {
  if (/Wireless Link|^WLA$/iu.test(model)) return 'wla';
  if (model.includes('300')) return 'st300';
  if (model.includes('20')) return 'st20';
  return 'st10';
}

// Our own drawings of the speakers seen from the front, for the speaker bar and card.
// They fill the box they are put in.
export function ModelArt({ model }: { readonly model?: string | undefined }): ReactElement {
  const k = modelKey(model);
  const id = useId().replace(/:/gu, '');
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
