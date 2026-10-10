// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Icon } from '../../atoms/Icon';
import type { Transport } from '../../../lib/playback';

// The play, pause or stop glyph for what a transport button will do.
export function TransportGlyph({ action }: { action: Transport }) {
  if (action === 'pause') return <Icon.pause />;
  if (action === 'stop') return <Icon.stop />;
  return <Icon.play />;
}
