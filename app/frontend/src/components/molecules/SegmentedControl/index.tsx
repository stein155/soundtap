// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useRadioKeys } from '../../../hooks/useRadioKeys';
import { Segment, Track } from './styled';

interface Props<V> {
  label: string;
  value: V;
  options: { value: V; label: string }[];
  onChange: (value: V) => void;
}

// A small inline choice between a few values (12h / 24h).
export function SegmentedControl<V>({ label, value, options, onChange }: Props<V>) {
  const keys = useRadioKeys(
    options.length,
    options.findIndex((o) => o.value === value),
    (i) => onChange(options[i].value)
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
