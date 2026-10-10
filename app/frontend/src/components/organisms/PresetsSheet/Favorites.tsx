// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Art } from '../../molecules/Art';
import { Icon } from '../../atoms/Icon';
import { useI18n } from '../../../lib/i18n';
import { logoURL } from '../../../lib/artwork';
import type { RecentItem } from '../../../lib/recent';

export interface FavoriteActions {
  items: RecentItem[];
  // Saves what is held, or else what plays.
  onAdd: () => void;
  onPlay: (item: RecentItem) => void;
  onRemove: (item: RecentItem) => void;
}

interface Props extends FavoriteActions {
  speaker: string;
  canAdd: boolean;
  saving: boolean;
  close: () => void;
}

// The heart under the presets: it saves what plays, or what a browse list handed over, and
// says what it does, or that there is nothing to save yet. A long press ends in the same
// click, so stock's gesture works, and so does a keyboard.
function AddFavorite({
  canAdd,
  saving,
  onAdd,
  empty,
}: Pick<Props, 'canAdd' | 'saving' | 'onAdd'> & { empty: boolean }) {
  const { t } = useI18n();
  const ready = canAdd || saving;
  const hint = ready ? t('favoriteHold') : empty ? t('favoriteNone') : null;
  return (
    <button
      type="button"
      className="prow-main fav-add"
      onClick={() => {
        if (ready) onAdd();
      }}
    >
      <Icon.heart width="24" height="24" />
      <span className="ptext">
        <span className="pname">{t('favorites')}</span>
        {hint && <span className="psrc">{hint}</span>}
      </span>
    </button>
  );
}

// Stock's favorites: what the six presets have no room for.
export function Favorites({
  items,
  speaker,
  canAdd,
  saving,
  close,
  onAdd,
  onPlay,
  onRemove,
}: Props) {
  const { t } = useI18n();
  return (
    <section className="favs" aria-label={t('favorites')}>
      <AddFavorite
        canAdd={canAdd}
        saving={saving}
        onAdd={() => {
          onAdd();
          if (saving) close();
        }}
        empty={!items.length}
      />
      <ul className="prows">
        {items.map((item) => (
          <li key={item.key} className="prow">
            <button
              type="button"
              className="prow-main"
              disabled={saving}
              onClick={() => {
                onPlay(item);
                close();
              }}
            >
              <Art
                src={logoURL({ logo: item.art, tuneInId: item.tuneInId }, speaker)}
                name={item.name}
                className="part"
              />
              <span className="ptext">
                <span className="pname">{item.name}</span>
                <span className="psrc">{item.spotifyUri ? 'Spotify' : 'TuneIn'}</span>
              </span>
            </button>
            <button
              type="button"
              className="prow-more"
              aria-label={`${t('remove')} ${item.name}`}
              onClick={() => onRemove(item)}
            >
              <Icon.close />
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
