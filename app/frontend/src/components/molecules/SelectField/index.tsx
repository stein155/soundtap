// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Icon } from '../../atoms/Icon';
import { cardShadow, useSurface } from '../../../hooks/useSurface';
import { Chevron, Select, Trail, Wrap } from './styled';

interface Props<V extends string> {
  readonly label: string;
  readonly value: V;
  readonly options: { value: V; label: string }[];
  readonly onChange: (value: V) => void;
  readonly disabled?: boolean;
  // Ties the select to a visible label, which then replaces aria-label.
  readonly id?: string;
  // What a native option cannot show about the chosen one: Wi-Fi's lock and signal.
  readonly trail?: ReactNode;
}

// A full-width native select in the design's field style.
export function SelectField<V extends string>({
  label,
  value,
  options,
  onChange,
  disabled,
  id,
  trail,
}: Props<V>): ReactElement {
  return (
    <Wrap>
      <Select
        id={id}
        $shadow={cardShadow(useSurface())}
        $trail={Boolean(trail)}
        value={value}
        onChange={(e) => {
          // The select only offers these options: the one picked is always among them.
          const picked = options.find((o) => o.value === e.target.value);
          if (picked) onChange(picked.value);
        }}
        aria-label={id ? undefined : label}
        disabled={disabled}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </Select>
      {Boolean(trail) && <Trail>{trail}</Trail>}
      <Chevron aria-hidden="true">
        <Icon.chevron width="18" height="18" />
      </Chevron>
    </Wrap>
  );
}
