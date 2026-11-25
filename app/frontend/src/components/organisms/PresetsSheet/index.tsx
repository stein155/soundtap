// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactElement } from 'react';
import { Button, Heading } from 'react-aria-components';
import { EqualizerBars } from '../../atoms/EqualizerBars';
import { Spinner } from '../../atoms/Spinner';
import { Icon } from '../../atoms/Icon';
import { Favorites, type FavoriteActions } from './Favorites';
import { Art } from '../../molecules/Art';
import { SheetModal } from '../../molecules/SheetModal';
import { useLongPress } from '../../../hooks/useLongPress';
import { useI18n } from '../../../lib/i18n';
import { logoURL } from '../../../lib/artwork';
import { activePresetIndex } from '../../../lib/station';
import type { Player, Preset } from '../../../lib/types';

interface Props {
  open: boolean;
  onClose: () => void;
  presets: (Preset | null)[];
  player: Player;
  loading: boolean;
  speaker: string;
  canStore: boolean;
  onPlay: (slot: number) => void;
  // What a long press in a browse list holds: a tap on a slot stores it there.
  saving: string | null;
  onSave: (slot: number) => void;
  onStore: (slot: number) => void;
  favorites: FavoriteActions;
}

interface RowProps {
  slot: number;
  preset: Preset | null;
  speaker: string;
  state: 'idle' | 'starting' | 'playing';
  canStore: boolean;
  onPlay: () => void;
  onEmpty: () => void;
  onStore: () => void;
}

// One preset: tap plays it (an empty one says how to fill it), a long press stores what
// plays, as in Stockholm.
function PresetRow({ slot, preset, speaker, state, canStore, onPlay, onEmpty, onStore }: RowProps) {
  const { t } = useI18n();
  const press = useLongPress(canStore ? onStore : null, preset ? onPlay : onEmpty);
  const name = preset?.name.trim() || '?';
  return (
    <li className={`prow${preset ? '' : ' empty'}`} data-active={state !== 'idle' || undefined}>
      <button type="button" className="prow-main" {...press}>
        <span className="pnum">{slot}</span>
        {preset ? (
          <Art src={logoURL(preset, speaker)} name={name} className="part" />
        ) : (
          <span className="part pempty" aria-hidden="true" />
        )}
        <span className="ptext">
          <span className="pname">{preset ? name : t('presetOpen')}</span>
          {preset && (
            <span className="psrc">{preset.source === 'SPOTIFY' ? 'Spotify' : 'TuneIn'}</span>
          )}
        </span>
        {state === 'playing' && <EqualizerBars />}
        {state === 'starting' && <Spinner small />}
      </button>
    </li>
  );
}

// What a tap on an empty preset says, on a page of its own as in Stockholm.
function EmptyPreset({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useI18n();
  return (
    <SheetModal open={open} onClose={onClose} label={t('presetEmptyTitle')} variant="page">
      <div className="pblank">
        <Icon.info width="26" height="26" />
        <Heading slot="title" className="pblank-title">
          {t('presetEmptyTitle')}
        </Heading>
        <p>{t('presetEmptyHint')}</p>
        <Button className="pblank-ok" onPress={onClose}>
          OK
        </Button>
      </div>
    </SheetModal>
  );
}

// The sheet's grip and title, with its X.
function PageHead({ close }: { close: () => void }) {
  const { t } = useI18n();
  return (
    <>
      <span className="ract-grab" aria-hidden="true" />
      <div className="phead">
        <Heading slot="title" className="phead-title">
          {t('presetsAndFavorites')}
        </Heading>
        <Button className="top-btn" aria-label={t('close')} onPress={close}>
          <Icon.close />
        </Button>
      </div>
    </>
  );
}

const playingState = (status: Player['status']): 'playing' | 'starting' | 'idle' => {
  if (status === 'playing') return 'playing';
  return status === 'starting' || status === 'buffering' ? 'starting' : 'idle';
};

// The selected room's six presets and its favorite, in a sheet from the bottom: tap one to
// play it.
export function PresetsSheet({
  open,
  onClose,
  presets,
  player,
  loading,
  speaker,
  canStore,
  onPlay,
  saving,
  onSave,
  onStore,
  favorites,
}: Props): ReactElement {
  const { t } = useI18n();
  const [emptyTapped, setEmptyTapped] = useState(false);
  // One active slot, resolved once: a loose name match must never light up several.
  const active = activePresetIndex(presets, player.station);
  const hint = saving ? `${t('spotifySaveTo')}: ${saving}` : null;
  const activeState = playingState(player.status);
  return (
    <SheetModal open={open} onClose={onClose} label={t('presetsAndFavorites')} className="psheet">
      {(close) => (
        <>
          <PageHead close={close} />
          <div className="plist">
            {hint && (
              <p className="phint" role="status">
                {hint}
              </p>
            )}
            <h2 className="pbar" id="pbar-presets">
              {t('yourStations')}
            </h2>
            <ol className="prows" aria-labelledby="pbar-presets" aria-busy={loading || undefined}>
              {presets.map((preset, i) => {
                const slot = i + 1;
                if (loading) return <li key={slot} className="prow skeleton" aria-hidden="true" />;
                return (
                  <PresetRow
                    key={slot}
                    slot={slot}
                    preset={preset}
                    speaker={speaker}
                    state={i === active ? activeState : 'idle'}
                    canStore={canStore && !saving}
                    onPlay={() => {
                      if (saving) onSave(slot);
                      else onPlay(slot);
                      close();
                    }}
                    onEmpty={() => {
                      if (!saving) {
                        setEmptyTapped(true);
                        return;
                      }
                      onSave(slot);
                      close();
                    }}
                    onStore={() => onStore(slot)}
                  />
                );
              })}
            </ol>
            <Favorites
              {...favorites}
              speaker={speaker}
              canAdd={canStore}
              saving={!!saving}
              close={close}
            />
          </div>
          <EmptyPreset open={emptyTapped} onClose={() => setEmptyTapped(false)} />
        </>
      )}
    </SheetModal>
  );
}
