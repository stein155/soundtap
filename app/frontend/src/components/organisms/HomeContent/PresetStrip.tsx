// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Art } from '../../molecules/Art';
import { useLongPress } from '../../../hooks/useLongPress';
import { useI18n } from '../../../lib/i18n';
import { logoURL } from '../../../lib/artwork';
import { activePresetIndex } from '../../../lib/station';
import type { Player, Preset } from '../../../lib/types';

export interface PresetStripProps {
  presets: (Preset | null)[];
  player: Player;
  speaker: string;
  loading: boolean;
  // Whether what plays can be stored on a slot, as on the presets page.
  canStore: boolean;
  // A tap on a filled slot plays it; an open slot opens the presets page, where a slot is
  // filled (the top bar's button opens it too). A hold stores what plays, as on the speaker.
  onPlay: (slot: number) => void;
  onOpen: () => void;
  onStore: (slot: number) => void;
}

interface SlotProps {
  slot: number;
  preset: Preset | null;
  speaker: string;
  active: boolean;
  canStore: boolean;
  onPlay: () => void;
  onOpen: () => void;
  onStore: () => void;
}

// One slot: tap plays it (an open one opens the presets page), a long press stores what
// plays there, as the presets page and the speaker's own buttons do.
function Slot({ slot, preset, speaker, active, canStore, onPlay, onOpen, onStore }: SlotProps) {
  const { t } = useI18n();
  const press = useLongPress(canStore ? onStore : null, preset ? onPlay : onOpen);
  const name = preset?.name.trim() || t('presetOpen');
  return (
    <li>
      <button
        type="button"
        className="pstrip-slot"
        data-empty={!preset || undefined}
        data-active={active || undefined}
        aria-current={active || undefined}
        aria-label={t('presetSlot', { slot, name })}
        {...press}
      >
        {preset && <Art src={logoURL(preset, speaker)} name={name} className="pstrip-art" />}
        <span className="pstrip-num" aria-hidden="true">
          {slot}
        </span>
      </button>
    </li>
  );
}

// The six presets at the top of the start page, as the speaker's own buttons: one tap
// away instead of behind the top bar's button. The one playing is ringed.
export function PresetStrip({
  presets,
  player,
  speaker,
  loading,
  canStore,
  onPlay,
  onOpen,
  onStore,
}: PresetStripProps) {
  const { t } = useI18n();
  const active = activePresetIndex(presets, player.station);
  return (
    <section className="pstrip" aria-labelledby="home-presets">
      <h2 className="h-row" id="home-presets">
        {t('presets')}
      </h2>
      <ol className="pstrip-row" aria-busy={loading || undefined}>
        {presets.map((preset, i) => (
          <Slot
            key={i + 1}
            slot={i + 1}
            preset={preset}
            speaker={speaker}
            active={i === active}
            canStore={canStore}
            onPlay={() => onPlay(i + 1)}
            onOpen={onOpen}
            onStore={() => onStore(i + 1)}
          />
        ))}
      </ol>
    </section>
  );
}
