// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Icon } from '../../atoms/Icon';
import type { Transport } from '../../../lib/playback';
import type { ReactElement } from 'react';

// The play, pause or stop glyph for what a transport button will do.
export function TransportGlyph({ action }: { readonly action: Transport }): ReactElement {
  if (action === 'pause') return <Icon.pause />;
  if (action === 'stop') return <Icon.stop />;
  return <Icon.play />;
}
