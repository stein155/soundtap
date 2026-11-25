// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Button } from 'react-aria-components';
import { Art } from '../../molecules/Art';
import { useI18n } from '../../../lib/i18n';
import { logoURL } from '../../../lib/artwork';
import { activePresetIndex } from '../../../lib/station';
import type { Player, Preset } from '../../../lib/types';

export interface PresetStripProps {
  presets: (Preset | null)[];
  player: Player;
  speaker: string;
  loading: boolean;
  // A tap on a filled slot plays it; an open slot opens the presets page, where a slot is
  // filled (the top bar's button opens it too).
  onPlay: (slot: number) => void;
  onOpen: () => void;
}

// The six presets at the top of the start page, as the speaker's own buttons: one tap
// away instead of behind the top bar's button. The one playing is ringed.
export function PresetStrip({
  presets,
  player,
  speaker,
  loading,
  onPlay,
  onOpen,
}: PresetStripProps) {
  const { t } = useI18n();
  const active = activePresetIndex(presets, player.station);
  return (
    <section className="pstrip" aria-labelledby="home-presets">
      <h2 className="h-row" id="home-presets">
        {t('presets')}
      </h2>
      <ol className="pstrip-row" aria-labelledby="home-presets" aria-busy={loading || undefined}>
        {presets.map((preset, i) => {
          const slot = i + 1;
          const name = preset?.name.trim() || t('presetOpen');
          return (
            <li key={slot}>
              <Button
                className="pstrip-slot"
                data-empty={!preset || undefined}
                data-active={i === active || undefined}
                aria-label={t('presetSlot', { slot, name })}
                onPress={() => (preset ? onPlay(slot) : onOpen())}
              >
                {preset ? (
                  <Art src={logoURL(preset, speaker)} name={name} className="pstrip-art" />
                ) : (
                  <span className="pstrip-num" aria-hidden="true">
                    {slot}
                  </span>
                )}
              </Button>
            </li>
          );
        })}
      </ol>
    </section>
  );
}
