// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useRadioKeys } from '../../../hooks/useRadioKeys';
import { Segment, Track } from './styled';
import type { ReactElement } from 'react';

interface Props<V> {
  readonly label: string;
  readonly value: V;
  readonly options: { value: V; label: string }[];
  readonly onChange: (value: V) => void;
}

// A small inline choice between a few values (12h / 24h).
export function SegmentedControl<V>({ label, value, options, onChange }: Props<V>): ReactElement {
  const keys = useRadioKeys(
    options.length,
    options.findIndex((o) => o.value === value),
    (i) => {
      const picked = options[i];
      if (picked) onChange(picked.value);
    }
  );
  return (
    <Track role="radiogroup" aria-label={label}>
      {options.map((o, i) => (
        <Segment
          key={o.label}
          type="button"
          role="radio"
          aria-checked={o.value === value}
          tabIndex={keys.tabIndex(i)}
          $selected={o.value === value}
          onClick={() => onChange(o.value)}
          onKeyDown={keys.onKeyDown}
        >
          {o.label}
        </Segment>
      ))}
    </Track>
  );
}
