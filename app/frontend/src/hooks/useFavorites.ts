// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useRef, useState } from 'react';
import { addFavorite, getFavorites, removeFavorite } from '../lib/api';
import { recentItems, type RecentItem } from '../lib/recent';
import type { FavoriteEntry } from '../lib/types';

// After a failed read the list is read again, as useRecent does.
const RETRY_MS = 15000;

export interface Favorites {
  items: RecentItem[];
  add: (entry: FavoriteEntry) => Promise<void>;
  remove: (item: RecentItem) => Promise<void>;
}

// The selected speaker's favorites, kept with the speaker they belong to as useRecent does.
export function useFavorites(speaker: string, failed: () => void): Favorites {
  const [read, setRead] = useState<{ speaker: string; items: RecentItem[] }>({
    speaker,
    items: [],
  });
  // Only the newest read may land: an older one holds a list from before a later change,
  // or the list of the speaker selected before this one.
  const newest = useRef(0);
  const load = useCallback(async () => {
    newest.current += 1;
    const ticket = newest.current;
    const list = await getFavorites(speaker);
    if (list && ticket === newest.current) setRead({ speaker, items: recentItems(list) });
  }, [speaker]);
  useEffect(() => {
    let live = true;
    let id: ReturnType<typeof setTimeout> | undefined;
    const fetchList = () => {
      // A read of its own: a change made meanwhile (add, remove) reads the list after it.
      newest.current += 1;
      const ticket = newest.current;
      void getFavorites(speaker).then((list) => {
        if (!live || ticket !== newest.current) return;
        if (list) setRead({ speaker, items: recentItems(list) });
        else id = setTimeout(fetchList, RETRY_MS);
      });
    };
    fetchList();
    return () => {
      live = false;
      clearTimeout(id);
    };
  }, [speaker]);
  const add = async (entry: FavoriteEntry) => {
    await addFavorite(entry, speaker).catch(failed);
    await load();
  };
  const remove = async (item: RecentItem) => {
    const location = item.locations.at(0);
    if (location === undefined) return; // nothing on the speaker to point at
    await removeFavorite(item.source, location, speaker).catch(failed);
    await load();
  };
  return { items: read.speaker === speaker ? read.items : [], add, remove };
}
