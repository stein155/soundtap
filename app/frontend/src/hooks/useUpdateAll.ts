// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import { getReleases, getVersion, startUpdate } from '../lib/api';
import { updateAvailable } from '../lib/version';
import { speakerArg, type Room } from './useRooms';
import { UPDATE_MAX_POLLS, UPDATE_POLL_MS } from './useSelfUpdate';

export type UpdateAllState = 'queued' | 'busy' | 'current' | 'done' | 'failed';

// The same wait as useSelfUpdate: long enough for the speaker's own download budget.
const POLL_MS = UPDATE_POLL_MS;
const MAX_POLLS = UPDATE_MAX_POLLS;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// One speaker's stable update, start to finish: 'current' when there is nothing newer.
// A lookup that failed says nothing about the speaker, so it is 'failed', never 'current'.
async function updateOne(speaker: string): Promise<UpdateAllState> {
  const [version, releases] = await Promise.all([getVersion(speaker), getReleases(speaker)]);
  if (!version) return 'failed';
  if (version.updateMode !== 'firmware' || !version.updatable) return 'current';
  if (!releases || releases.unavailable) return 'failed';
  if (!updateAvailable(releases.current, releases.stable?.tag)) return 'current';
  const res = await startUpdate(undefined, speaker);
  if (res.status === 200 && res.body.status === 'current') return 'current';
  if (res.body.status !== 'updating' && res.status !== 202) return 'failed';
  return waitForVersion(speaker, res.body.to || releases.stable?.tag);
}

// Done only on the version asked for, as in useSelfUpdate: a speaker that comes back on
// another one is not updated.
async function waitForVersion(speaker: string, target?: string): Promise<UpdateAllState> {
  for (let i = 0; i < MAX_POLLS; i++) {
    await sleep(POLL_MS);
    const v = await getVersion(speaker);
    if (v?.version && v.version === target) return 'done';
  }
  return 'failed';
}

// Every online speaker to the latest stable, one after the other. Each asks for
// its own button press, a failure does not stop the rest, and the speaker serving this
// page goes last: its restart reloads the page.
export function useUpdateAll(rooms: Room[]) {
  const [states, setStates] = useState<Partial<Record<string, UpdateAllState>>>({});
  const [running, setRunning] = useState(false);
  const order = rooms.filter((r) => r.online).sort((a, b) => Number(a.self) - Number(b.self));

  const run = async () => {
    if (running) return;
    setRunning(true);
    setStates(Object.fromEntries(order.map((r) => [r.id, 'queued'])));
    let selfUpdated = false;
    for (const room of order) {
      setStates((s) => ({ ...s, [room.id]: 'busy' }));
      const state = await updateOne(speakerArg(room)).catch((): UpdateAllState => 'failed');
      setStates((s) => ({ ...s, [room.id]: state }));
      if (room.self && state === 'done') selfUpdated = true;
    }
    setRunning(false);
    if (selfUpdated) window.location.reload();
  };

  return { order, states, running, run };
}
