// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Icon } from '../../atoms/Icon';
import { useI18n, type I18nKey } from '../../../lib/i18n';
import type { Transport } from '../../../lib/playback';
import { Round, type TransportVariant } from './styled';

const LABEL: Record<Transport, I18nKey> = {
  play: 'play',
  pause: 'pause',
  resume: 'resume',
  stop: 'stop',
};
const GLYPH_SIZE: Record<TransportVariant, number> = { card: 14, player: 18, full: 22 };

interface Props {
  action: Transport;
  onClick: () => void;
  variant: TransportVariant;
  // Something plays (a room card shows that in its colour).
  on?: boolean;
  // The room or group it acts on, so a screen reader can tell the room cards apart.
  target?: string;
}

// The one transport control for what plays: play, pause/resume (Spotify) or stop.
export function TransportButton({ action, onClick, variant, on = false, target }: Props) {
  const { t } = useI18n();
  const Glyph = action === 'pause' ? Icon.pause : action === 'stop' ? Icon.stop : Icon.play;
  const size = GLYPH_SIZE[variant];
  return (
    <Round
      $variant={variant}
      $on={on}
      onClick={onClick}
      aria-label={target ? `${t(LABEL[action])} · ${target}` : t(LABEL[action])}
    >
      <Glyph width={size} height={size} />
    </Round>
  );
}
