// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useSyncExternalStore } from 'react';
import { speakerArg } from './rooms/household';

export type UpdatePhase = 'idle' | 'busy' | 'done' | 'updated' | 'error';

// Where an update the speaker took on is, as far as this page can tell from the outside: the
// speaker downloads and checks the release while it still answers with the old version, is
// away while it restarts, and answers with the new one once it is back. null until the
// speaker has taken the update on.
export type UpdateStage = 'download' | 'restart' | 'check';

// One speaker's update as this page follows it.
export interface UpdateRun {
  phase: UpdatePhase;
  stage: UpdateStage | null;
  // How far the download is, as the speaker last said; see shownProgress.
  progress: number | null;
  text: string;
}

const IDLE: UpdateRun = { phase: 'idle', stage: null, progress: null, text: '' };

// The updates under way, held once for the whole page by speaker (as speakerArg names it),
// so following one goes on while the owner leaves the Updates page, the update screen or
// Settings, and controls another speaker meanwhile. The timers that follow a speaker live
// here too, not in a component that may unmount. Which speaker's update screen is on show,
// if any, is kept here as well.
let runs: ReadonlyMap<string, UpdateRun> = new Map();
let screen: string | null = null;
const listeners = new Set<() => void>();
const timers = new Map<string, ReturnType<typeof setTimeout>>();
const generations = new Map<string, number>();

const notify = () => listeners.forEach((l) => l());

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};

export const runOf = (speaker: string): UpdateRun => runs.get(speaker) ?? IDLE;

export function setRun(speaker: string, patch: Partial<UpdateRun>): void {
  const next = new Map(runs);
  next.set(speaker, { ...runOf(speaker), ...patch });
  runs = next;
  notify();
}

// Whether the page follows the speaker through an update: under way, or just finished.
export const following = (run: UpdateRun): boolean =>
  run.phase === 'busy' || run.phase === 'updated';

// The percentage to show: only while the speaker downloads and says how far it is.
export const shownProgress = (run: UpdateRun): number | null =>
  run.phase === 'busy' && run.stage === 'download' ? run.progress : null;

// A new attempt for this speaker: answers and timers of an earlier one no longer land.
export function nextRun(speaker: string): number {
  const n = (generations.get(speaker) ?? 0) + 1;
  generations.set(speaker, n);
  clearTimeout(timers.get(speaker));
  timers.delete(speaker);
  return n;
}

export const isCurrentRun = (speaker: string, run: number): boolean =>
  (generations.get(speaker) ?? 0) === run;

// The next poll of this speaker's update, in place of any still pending.
export function schedule(speaker: string, poll: () => Promise<void>, ms: number): void {
  clearTimeout(timers.get(speaker));
  timers.set(
    speaker,
    setTimeout(() => {
      void poll();
    }, ms)
  );
}

export function openUpdateScreen(speaker: string): void {
  screen = speaker;
  notify();
}

export function closeUpdateScreen(): void {
  screen = null;
  notify();
}

// For tests, between them: nothing under way, nothing on show. Nobody is told: what is
// still on screen is about to be taken down.
export function resetUpdates(): void {
  timers.forEach((id) => clearTimeout(id));
  timers.clear();
  generations.clear();
  runs = new Map();
  screen = null;
}

// This speaker's update, live.
export function useUpdateRun(speaker: string): UpdateRun {
  return useSyncExternalStore(subscribe, () => runOf(speaker));
}

// Every speaker's update, live, by speaker.
export function useUpdateRuns(): ReadonlyMap<string, UpdateRun> {
  return useSyncExternalStore(subscribe, () => runs);
}

// The speaker whose update screen is on show, or null.
export function useUpdateScreen(): string | null {
  return useSyncExternalStore(subscribe, () => screen);
}

// A room's update while the page follows it (under way or just finished), else null: its
// controls wait meanwhile, and losing it while it restarts is no reason for alarm.
export function useRoomUpdate(
  room: { id: string; self: boolean } | null | undefined
): UpdateRun | null {
  const run = useSyncExternalStore(subscribe, () => (room ? runOf(speakerArg(room)) : IDLE));
  return following(run) ? run : null;
}
