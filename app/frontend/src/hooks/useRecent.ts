// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState } from 'react';
import { getRecent, removeRecent } from '../lib/api';
import { recentItems, type RecentItem } from '../lib/recent';

// The speaker records an entry when a station stops or is replaced; give it a moment.
const RECORD_DELAY_MS = 800;
// After a failed read, the list shown stays and the read is tried again.
const RETRY_MS = 15000;

// A removal belongs to one speaker: the same station stays in another room's history.
const removalOf = (speaker: string, item: RecentItem) => `${speaker} ${item.key}`;

export interface Recent {
  items: RecentItem[];
  remove: (item: RecentItem) => Promise<void>;
}

// What the selected speaker played recently, re-read whenever what plays changes. onFail
// hears about a removal the speaker did not take; the read-back then shows the entry again.
export function useRecent(
  speaker: string,
  playing: string,
  onFail?: (err: unknown) => void
): Recent {
  // Kept with the speaker it belongs to: after a switch the previous room's history is not
  // shown, or played, under the new one while its own is still loading.
  const [read, setRead] = useState<{ speaker: string; items: RecentItem[] }>({
    speaker,
    items: [],
  });
  const [generation, setGeneration] = useState(0);
  // Entries being removed: a read that started before the DELETE must not bring them back.
  const removing = useRef(new Set<string>());
  useEffect(() => {
    let live = true;
    let id: ReturnType<typeof setTimeout>;
    const load = (after: number) => {
      id = setTimeout(
        () =>
          void getRecent(speaker).then((list) => {
            if (!live) return;
            if (list)
              setRead({
                speaker,
                items: recentItems(list).filter(
                  (i) => !removing.current.has(removalOf(speaker, i))
                ),
              });
            else load(RETRY_MS);
          }),
        after
      );
    };
    load(RECORD_DELAY_MS);
    return () => {
      live = false;
      clearTimeout(id);
    };
  }, [speaker, playing, generation]);
  // Gone from the list at once; the speaker's list is read back afterwards either way.
  const remove = async (item: RecentItem) => {
    const removal = removalOf(speaker, item);
    if (removing.current.has(removal)) return; // already on its way out
    removing.current.add(removal);
    setRead((r) => ({ ...r, items: r.items.filter((i) => i.key !== item.key) }));
    const results = await Promise.allSettled(
      item.locations.map(async (loc) => removeRecent(item.source, loc, speaker))
    );
    removing.current.delete(removal);
    const failure = results.find((r) => r.status === 'rejected');
    if (failure) onFail?.(failure.reason);
    setGeneration((g) => g + 1);
  };
  return { items: read.speaker === speaker ? read.items : [], remove };
}
