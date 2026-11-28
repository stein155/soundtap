// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, type ReactElement } from 'react';
import { Spinner } from '../../../atoms/Spinner';
import { Icon } from '../../../atoms/Icon';
import {
  closeUpdateScreen,
  following,
  openUpdateScreen,
  shownProgress,
  useUpdateRun,
  useUpdateRuns,
  useUpdateScreen,
  type UpdateRun,
} from '../../../../hooks/updates';
import { speakerArg, type Room } from '../../../../hooks/useRooms';
import { useI18n } from '../../../../lib/i18n';
import { Updating } from './Updating';
import { Pill } from './styled/updating';

// After a finished update this page still runs the old bundle; reload into the new one.
// Only while the update screen is on show: an owner busy with another speaker is not
// pulled away by it, and the screen they come back to reloads.
const RELOAD_MS = 2200;

const roomOf = (rooms: readonly Room[], speaker: string) =>
  rooms.find((r) => speakerArg(r) === speaker);

// The speakers the page follows through an update, in the household's order.
function followed(rooms: readonly Room[], runs: ReadonlyMap<string, UpdateRun>) {
  return rooms
    .map((room) => ({ room, run: runs.get(speakerArg(room)) }))
    .filter((f): f is { room: Room; run: UpdateRun } => !!f.run && following(f.run));
}

// Whether the update screen is on show: open for a speaker the page follows, once that
// speaker has taken the update on.
const onShow = (screen: string | null, run: UpdateRun | undefined): run is UpdateRun =>
  screen !== null && !!run && following(run) && run.stage !== null;

// The update screen of the speaker it is open for, over the app, while the page follows that
// speaker's update. Close leaves it for the app; the update goes on, and the notice on the
// dock, the speaker's row or its Updates page bring it back.
export function UpdateScreen({
  rooms,
  onOther,
}: {
  readonly rooms: readonly Room[];
  // The speakers, to control another one meanwhile.
  readonly onOther?: () => void;
}): ReactElement | null {
  const speaker = useUpdateScreen();
  const run = useUpdateRun(speaker ?? '');
  const shown = onShow(speaker, run);
  const done = shown && run.phase === 'updated';
  useEffect(() => {
    if (!done) return undefined;
    const id = setTimeout(() => window.location.reload(), RELOAD_MS);
    return () => clearTimeout(id);
  }, [done]);
  if (speaker === null || !shown || !run.stage) return null;
  const room = roomOf(rooms, speaker);
  return (
    <Updating
      name={room?.name || 'SoundTap'}
      model={room?.model ?? ''}
      stage={run.stage}
      done={done}
      progress={shownProgress(run)}
      onClose={closeUpdateScreen}
      onOther={
        onOther &&
        (() => {
          closeUpdateScreen();
          onOther();
        })
      }
    />
  );
}

// "Update · Kitchen · 42% done": how far a speaker's update is, or that it is done.
export function useUpdateLine(): (name: string, run: UpdateRun) => string {
  const { t } = useI18n();
  return (name, run) => {
    const progress = shownProgress(run);
    let how = '';
    if (run.phase === 'updated') how = run.text;
    else if (progress !== null) how = t('updatingProgress', { percent: progress });
    return [t('updatingEyebrow', { name }), how].filter(Boolean).join(' · ');
  };
}

// The way back to the update screen while it is closed: a small notice over the dock with
// the speaker and how far it is, on every page. A tap opens the screen again.
export function UpdateNotice({ rooms }: { readonly rooms: readonly Room[] }): ReactElement | null {
  const runs = useUpdateRuns();
  const screen = useUpdateScreen();
  const line = useUpdateLine();
  const first = followed(rooms, runs).at(0);
  if (!first || onShow(screen, screen === null ? undefined : runs.get(screen))) return null;
  const { room, run } = first;
  return (
    <Pill type="button" onClick={() => openUpdateScreen(speakerArg(room))}>
      {run.phase === 'updated' ? (
        <Icon.check width="14" height="14" aria-hidden="true" />
      ) : (
        <Spinner small />
      )}
      <span>{line(room.name, run)}</span>
    </Pill>
  );
}
