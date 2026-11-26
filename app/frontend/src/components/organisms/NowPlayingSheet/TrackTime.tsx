// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useRef, useState, type ReactElement } from 'react';
import { Slider, SliderThumb, SliderTrack } from 'react-aria-components';
import { useI18n } from '../../../lib/i18n';
import { seekTrack, trackPosition, type TrackPosition } from '../../../lib/api/playback';
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

interface Props {
  readonly speaker?: string | undefined;
  readonly playing: boolean;
  // Changes with the track and the play status, to read the position again at once.
  readonly nowKey: string;
}

// How far the track is, for what has an end: a stream shows nothing. The knob only shows
// where the speaker can seek, as in Stockholm.
export function TrackTime({ speaker, playing, nowKey }: Props): ReactElement | null {
  const { t } = useI18n();
  const [time, setTime] = useState<TrackPosition | null>(null);
  const [dragged, setDragged] = useState<number | null>(null);
  // Another track: the last one's length and place no longer hold, so show nothing until
  // the speaker tells the new ones (a pause or play keeps them).
  const track = nowKey.split('|')[0];
  const [shownTrack, setShownTrack] = useState(track);
  if (track !== shownTrack) {
    setShownTrack(track);
    setTime(null);
    setDragged(null);
  }
  // A seek moves this on: an answer that left before it must not put the old place back.
  const seeks = useRef(0);
  // A hidden page does not ask; shown again, it reads the place at once.
  const visible = usePageVisible();
  const load = useCallback(() => {
    const seeksThen = seeks.current;
    trackPosition(speaker).then(
      (p) => {
        if (seeksThen === seeks.current) setTime(p);
      },
      () => undefined
    );
  }, [speaker]);
  useEffect(() => {
    if (!visible) return undefined;
    seeks.current += 1; // an answer for the speaker or track before does not count either
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
  // No track length (or none yet): nothing to show a position on.
  if (time === null || time.durationMs === 0 || Number.isNaN(time.durationMs)) return null;
  const seek = (positionMs: number) => {
    seeks.current += 1;
    setDragged(null);
    setTime({ ...time, positionMs });
    seekTrack(positionMs, speaker).then(load, load);
  };
  const shown = dragged ?? time.positionMs;
  return (
    <div className="np-time">
      <span className="np-clock">{clock(shown)}</span>
      <Slider
        className="vol on-dark"
        value={shown}
        minValue={0}
        maxValue={time.durationMs}
        step={1000}
        aria-label={t('trackPosition')}
        isDisabled={!time.seekable}
        onChange={setDragged}
        onChangeEnd={seek}
      >
        <SliderTrack className="vtrack">
          {({ state }) => (
            <>
              <div className="vfill" style={{ width: `${state.getThumbPercent(0) * 100}%` }} />
              {time.seekable && <SliderThumb className="vthumb" />}
            </>
          )}
        </SliderTrack>
      </Slider>
      <span className="np-clock np-total">{clock(time.durationMs)}</span>
    </div>
  );
}
