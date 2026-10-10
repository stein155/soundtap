// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useRef, useState } from 'react';
import { addFavorite, getFavorites, removeFavorite } from '../lib/api';
import { useI18n } from '../lib/i18n';
import { recentItems, type RecentItem } from '../lib/recent';
import type { FavoriteEntry } from '../lib/types';

// After a failed read the list is read again, as useRecent does.
const RETRY_MS = 15000;

// The selected speaker's favorites, kept with the speaker they belong to as useRecent does.
export function useFavorites(
  speaker: string,
  notify: (message: string) => void,
  failed: () => void
) {
  const { t } = useI18n();
  const [read, setRead] = useState<{ speaker: string; items: RecentItem[] }>({
    speaker,
    items: [],
  });
  // Only the newest read may land: an older one holds a list from before a later change,
  // or the list of the speaker selected before this one.
  const newest = useRef(0);
  const load = useCallback(async () => {
    const read = ++newest.current;
    const list = await getFavorites(speaker);
    if (list && read === newest.current) setRead({ speaker, items: recentItems(list) });
  }, [speaker]);
  useEffect(() => {
    newest.current++;
    let live = true;
    let id: ReturnType<typeof setTimeout> | undefined;
    const fetchList = () =>
      void getFavorites(speaker).then((list) => {
        if (!live) return;
        if (list) setRead({ speaker, items: recentItems(list) });
        else id = setTimeout(fetchList, RETRY_MS);
      });
    fetchList();
    return () => {
      live = false;
      clearTimeout(id);
    };
  }, [speaker]);
  const add = async (entry: FavoriteEntry) => {
    await addFavorite(entry, speaker).catch((err: { status?: number }) =>
      err.status === 409 ? notify(t('favoritesFull')) : failed()
    );
    await load();
  };
  const remove = async (item: RecentItem) => {
    await removeFavorite(item.source, item.locations[0], speaker).catch(failed);
    await load();
  };
  return { items: read.speaker === speaker ? read.items : [], add, remove };
}
