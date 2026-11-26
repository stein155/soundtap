// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type * as React from 'react';
import { Fill, OriginMark, Rail, Thumb, Track } from './styled';
import { clamp, useSliderPointer } from './useSliderPointer';
import type { ReactElement } from 'react';

interface Props {
  readonly value: number;
  readonly onChange: (value: number) => void;
  readonly label: string;
  readonly min?: number;
  readonly max?: number;
  // Where the fill starts. A volume fills from the left; bass fills from the speaker's
  // neutral value, which on a SoundTouch is the top of its range (-9..0).
  readonly origin?: number;
  // Arrow-key step.
  readonly step?: number;
  // How a screen reader says the value ("+3" rather than "3"), when it is not the number.
  readonly valueText?: string;
  // The slightly taller track of a settings card.
  readonly tall?: boolean;
}

// The design's drag slider, as a real slider: a pointer drag anywhere on the track
// (useSliderPointer), arrow keys and Home/End, and role="slider" for screen readers.
export function Slider({
  value,
  onChange,
  label,
  min = 0,
  max = 100,
  origin = min,
  step = 1,
  valueText,
  tall,
}: Props): ReactElement {
  const { track, handlers } = useSliderPointer({ value, min, max, onChange });
  const span = Math.max(1, max - min);

  const onKeyDown = (e: React.KeyboardEvent) => {
    const delta = { ArrowRight: step, ArrowUp: step, ArrowLeft: -step, ArrowDown: -step }[e.key];
    const jump = { Home: min, End: max }[e.key];
    if (delta === undefined && jump === undefined) return;
    e.preventDefault();
    onChange(jump ?? clamp(value + (delta ?? 0), min, max));
  };

  const pct = ((value - min) / span) * 100;
  const originPct = ((origin - min) / span) * 100;
  return (
    <Track
      ref={track}
      $tall={tall}
      role="slider"
      tabIndex={0}
      aria-label={label}
      aria-valuemin={min}
      aria-valuemax={max}
      aria-valuenow={value}
      aria-valuetext={valueText}
      {...handlers}
      onKeyDown={onKeyDown}
    >
      <Rail />
      {origin !== min && <OriginMark style={{ left: `${originPct}%` }} />}
      <Fill
        style={{ left: `${Math.min(originPct, pct)}%`, width: `${Math.abs(pct - originPct)}%` }}
      />
      <Thumb style={{ left: `${pct}%` }} />
    </Track>
  );
}
