// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Icon } from '../../atoms/Icon';
import { StationLogo } from '../../atoms/StationLogo';
import { AuxLabel } from '../../atoms/AuxLabel';
import type { Artwork } from '../../../lib/playback';
import { modelIcon } from '../../../lib/format';
import { Square, type ArtworkVariant } from './styled';

export type { ArtworkVariant } from './styled';

// Glyph and lettermark sizes per variant: a room card, the mini player, full screen.
const GLYPH = { card: 22, player: 24, full: 64 } as const;
const AUX = { card: 10.5, player: 10.5, full: 40 } as const;
const LETTERS = { card: 0.9, player: 1, full: 2.4 } as const;

interface Props {
  artwork: Artwork;
  variant: ArtworkVariant;
  // Tints a square without a station logo, for an input that is playing.
  highlighted?: boolean;
  // The speaker's model: an idle square shows its own drawing, as settings does.
  model?: string;
}

// The square for what plays: the station's logo on its colour, the Bluetooth glyph,
// "AUX", or the speaker.
export function PlaybackArtwork({ artwork, variant, highlighted, model }: Props) {
  const Speaker = Icon[modelIcon(model)];
  if (artwork.kind === 'station')
    return (
      <Square $variant={variant} $color={artwork.color}>
        <StationLogo src={artwork.src} initials={artwork.initials} scale={LETTERS[variant]} />
      </Square>
    );
  return (
    <Square $variant={variant} $highlighted={highlighted}>
      {artwork.kind === 'bluetooth' && (
        <Icon.bluetooth width={GLYPH[variant]} height={GLYPH[variant]} />
      )}
      {artwork.kind === 'aux' && <AuxLabel size={AUX[variant]} />}
      {artwork.kind === 'tv' && <Icon.tv width={GLYPH[variant]} height={GLYPH[variant]} />}
      {artwork.kind === 'speaker' && <Speaker width={GLYPH[variant]} height={GLYPH[variant]} />}
    </Square>
  );
}
