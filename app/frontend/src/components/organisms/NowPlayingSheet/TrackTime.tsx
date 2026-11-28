// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useRef, useState, type ReactElement, type ReactNode } from 'react';
import { ProgressBar } from 'react-aria-components';
import { useI18n } from '../../../lib/i18n';
import { trackPosition, type TrackPosition } from '../../../lib/api/playback';
import { usePageVisible } from '../../../hooks/usePageVisible';

const POLL_MS = 5000;
const TICK_MS = 1000;

// 1:05, or 1:01:05 for an hour or more.
export function clock(ms: number): string {
  const total = Math.floor(ms / 1000);
  const seconds = String(total % 60).padStart(2, '0');
  const minutes = Math.floor(total / 60);
  if (minutes < 60) return `${minutes}:${seconds}`;
  return `${Math.floor(minutes / 60)}:${String(minutes % 60).padStart(2, '0')}:${seconds}`;
}

// Where the speaker is in the track, read every few seconds and counted on in between while
// it plays; null while it has not said, or for another track it has not said yet.
function useTrackPosition(
  speaker: string | undefined,
  playing: boolean,
  nowKey: string
): TrackPosition | null {
  const [time, setTime] = useState<TrackPosition | null>(null);
  // Another track: the last one's length and place no longer hold, so show nothing until
  // the speaker tells the new ones (a pause or play keeps them).
  const track = nowKey.split('|')[0];
  const [shownTrack, setShownTrack] = useState(track);
  if (track !== shownTrack) {
    setShownTrack(track);
    setTime(null);
  }
  // Moves on with every new read: an answer for the speaker or track before does not count.
  const reads = useRef(0);
  // A hidden page does not ask; shown again, it reads the place at once.
  const visible = usePageVisible();
  const load = useCallback(() => {
    const readsThen = reads.current;
    trackPosition(speaker).then(
      (p) => {
        if (readsThen === reads.current) setTime(p);
      },
      () => undefined
    );
  }, [speaker]);
  useEffect(() => {
    if (!visible) return undefined;
    reads.current += 1;
    load();
    const id = setInterval(load, POLL_MS);
    return () => clearInterval(id);
  }, [load, nowKey, visible]);
  useEffect(() => {
    if (!playing) return undefined;
    const id = setInterval(() => {
      setTime((p) => p && { ...p, positionMs: Math.min(p.positionMs + TICK_MS, p.durationMs) });
    }, TICK_MS);
    return () => clearInterval(id);
  }, [playing]);
  return time;
}

const hasEnd = (time: TrackPosition | null): time is TrackPosition =>
  time !== null && time.durationMs > 0 && !Number.isNaN(time.durationMs);

// The artwork as now playing shows it: a square that takes the height the controls leave
// (home.css), with how far the track is as a thin line inside its bottom edge and the time
// played and left under it. Only shown, not dragged.
export function ArtFrame({
  time,
  children,
}: {
  readonly time?: TrackPosition | null | undefined;
  readonly children: ReactNode;
}): ReactElement {
  const { t } = useI18n();
  const read = time ?? null;
  const timed = hasEnd(read) ? read : null;
  const left = timed ? Math.max(0, timed.durationMs - timed.positionMs) : 0;
  return (
    <div className="np-cover">
      <div className="np-cover-in">
        <div className="np-frame">
          {children}
          {timed && (
            <ProgressBar
              className="np-progress"
              aria-label={t('trackPosition')}
              value={timed.positionMs}
              minValue={0}
              maxValue={timed.durationMs}
              valueLabel={`${clock(timed.positionMs)} / ${clock(timed.durationMs)}`}
            >
              {({ percentage }) => (
                <span className="np-progress-fill" style={{ width: `${percentage ?? 0}%` }} />
              )}
            </ProgressBar>
          )}
        </div>
        {timed && (
          <div className="np-times" aria-hidden="true">
            <span>{clock(timed.positionMs)}</span>
            <span>{`−${clock(left)}`}</span>
          </div>
        )}
      </div>
    </div>
  );
}

interface Props {
  readonly speaker?: string | undefined;
  readonly playing: boolean;
  // Changes with the track and the play status, to read the position again at once.
  readonly nowKey: string;
  // The artwork the line runs under.
  readonly children: ReactNode;
}

// The artwork with how far the track is, for what has an end: a stream shows the artwork
// alone.
export function TrackTime({ speaker, playing, nowKey, children }: Props): ReactElement {
  const time = useTrackPosition(speaker, playing, nowKey);
  return <ArtFrame time={time}>{children}</ArtFrame>;
}
