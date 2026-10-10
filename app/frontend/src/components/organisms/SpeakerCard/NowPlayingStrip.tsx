// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Button } from 'react-aria-components';
import { PlayArt } from '../../molecules/Art';
import { TransportGlyph } from '../../molecules/TransportGlyph';
import { useI18n } from '../../../lib/i18n';
import { sourceLine, type PlaybackView } from '../../../lib/playback';

interface Props {
  view: PlaybackView;
  // The stream is still connecting or buffering.
  starting: boolean;
  // What the transport button is for, read out with its action.
  groupLabel: string;
  onTransport: () => void;
}

// The small "now playing" line at the top of the card, with play/stop for a station.
export function NowPlayingStrip({ view, starting, groupLabel, onTransport }: Props) {
  const { t } = useI18n();
  return (
    <div className="spk-now">
      <PlayArt artwork={view.artwork} name={view.title} className="mini-art" />
      <span className="mini-text">
        <span className="mini-title">{view.title}</span>
        <span className="mini-line">{starting ? t('connecting') : sourceLine(view, t)}</span>
      </span>
      {view.artwork.kind === 'station' && (
        <Button
          className="mini-ctl"
          onPress={onTransport}
          aria-label={`${t(view.transport)} · ${groupLabel}`}
        >
          <TransportGlyph action={view.transport} />
        </Button>
      )}
    </div>
  );
}
