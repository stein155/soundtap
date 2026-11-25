// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Art } from '../../molecules/Art';
import { Icon } from '../../atoms/Icon';
import { useLongPress } from '../../../hooks/useLongPress';
import { useI18n } from '../../../lib/i18n';
import { logoURL } from '../../../lib/artwork';
import type { RecentItem } from '../../../lib/recent';

export interface FavoriteActions {
  // The speaker's list; the first is the favorite, anything after it is from before the list
  // offered one and stays out of sight.
  items: RecentItem[];
  // Saves what is held, or else what plays, in place of the favorite.
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

// The one favorite beside the six presets. A tap plays it; a long press saves what plays in
// its place, as on a preset; while a browse list holds something, a tap saves that.
export function Favorites({
  items,
  speaker,
  canAdd,
  saving,
  close,
  onAdd,
  onPlay,
  onRemove,
}: Props): ReactElement {
  const { t } = useI18n();
  const item = items.at(0);
  const save = () => {
    onAdd();
    if (saving) close();
  };
  const press = useLongPress(canAdd && !saving ? save : null, () => {
    if (saving) {
      save();
      return;
    }
    if (!item) return;
    onPlay(item);
    close();
  });
  return (
    <section className="pfav" aria-labelledby="pfav-title">
      <h2 className="pbar" id="pfav-title">
        {t('favorite')}
      </h2>
      <ul className="pfav-list" aria-labelledby="pfav-title">
        <li className={`pfav-row${item ? '' : ' empty'}`}>
          <button type="button" className="pfav-main" {...press}>
            {item ? (
              <Art
                src={logoURL({ logo: item.art, tuneInId: item.tuneInId }, speaker)}
                name={item.name}
                className="pfav-art"
              />
            ) : (
              <span className="pfav-art pempty" aria-hidden="true" />
            )}
            <span className="ptext">
              <span className="pname">{item ? item.name : t('presetOpen')}</span>
              {item && <span className="psrc">{item.spotifyUri ? 'Spotify' : 'TuneIn'}</span>}
            </span>
          </button>
          {item && !saving && (
            <button
              type="button"
              className="pfav-remove"
              aria-label={`${t('remove')} ${item.name}`}
              onClick={() => onRemove(item)}
            >
              <Icon.close />
            </button>
          )}
        </li>
      </ul>
      <p className="pnote">{t('favoriteNote')}</p>
    </section>
  );
}
