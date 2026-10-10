// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useSyncExternalStore } from 'react';
import { snapshot, subscribeResource, type Held, type ResourceKind } from './store';

// One of a speaker's reads as the page holds it (see store.ts). Reading it does not ask the
// speaker: the caller says when, with ensureResource or refreshResource.
export function useSpeakerResource<K extends ResourceKind>(kind: K, speaker: string): Held<K> {
  const subscribe = useCallback(
    (listener: () => void) => subscribeResource(kind, speaker, listener),
    [kind, speaker]
  );
  const get = () => snapshot(kind, speaker);
  return useSyncExternalStore(subscribe, get, get);
}
