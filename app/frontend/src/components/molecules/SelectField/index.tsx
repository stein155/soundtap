// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Icon } from '../../atoms/Icon';
import { cardShadow, useSurface } from '../../../hooks/useSurface';
import { Chevron, Select, Trail, Wrap } from './styled';

interface Props<V extends string> {
  label: string;
  value: V;
  options: { value: V; label: string }[];
  onChange: (value: V) => void;
  disabled?: boolean;
  // Ties the select to a visible label, which then replaces aria-label.
  id?: string;
  // What a native option cannot show about the chosen one: Wi-Fi's lock and signal.
  trail?: ReactNode;
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
}: Props<V>) {
  return (
    <Wrap>
      <Select
        id={id}
        $shadow={cardShadow(useSurface())}
        $trail={!!trail}
        value={value}
        onChange={(e) => onChange(e.target.value as V)}
        aria-label={id ? undefined : label}
        disabled={disabled}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </Select>
      {trail && <Trail>{trail}</Trail>}
      <Chevron aria-hidden="true">
        <Icon.chevron width="18" height="18" />
      </Chevron>
    </Wrap>
  );
}
