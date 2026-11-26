// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { useRadioKeys } from '../../../hooks/useRadioKeys';
import { Icon } from '../../atoms/Icon';
import { SetMenu } from '../SetMenu';
import { Glyph, IconBadge, Label, Text } from '../SetItem/styled';
import { Chosen, Choice, Mark, Tick, Trail } from './styled';

export interface ChoiceOption<V extends string> {
  value: V;
  label: string;
  // A picture before the name (a speaker's drawing), or a plain glyph (a signal).
  art?: ReactNode;
  glyph?: ReactNode;
  // What goes with the name on the right (a lock).
  trail?: ReactNode;
}

interface Props<V extends string> {
  readonly label: string;
  readonly value: V;
  readonly options: ChoiceOption<V>[];
  readonly onChange: (value: V) => void;
  // The grey bar above the rows.
  readonly title?: string | undefined;
  // The chosen row marked as a pick of things (a partner, a network): a bar at its edge
  // and a filled tick; without it a plain tick, as a setting's value (a language).
  readonly marked?: boolean;
  readonly disabled?: boolean;
  // Rows a little taller than a settings row (the first-run page's networks).
  readonly tall?: boolean;
}

// A pick-one list as a page of rows, the chosen one ticked (Sonos and iOS style), in place
// of a dropdown.
export function ChoiceList<V extends string>({
  label,
  value,
  options,
  onChange,
  title,
  marked = false,
  disabled,
  tall,
}: Props<V>): ReactElement {
  const keys = useRadioKeys(
    options.length,
    options.findIndex((o) => o.value === value),
    (i) => {
      const picked = options[i];
      if (picked) onChange(picked.value);
    }
  );
  return (
    // The focus is on the checked radio inside (roving tabindex), not on the group, so the
    // radios take the keys.
    <div role="radiogroup" aria-label={label}>
      <SetMenu title={title}>
        {options.map((o, i) => {
          const on = o.value === value;
          return (
            <Choice
              key={o.value}
              type="button"
              role="radio"
              aria-checked={on}
              tabIndex={keys.tabIndex(i)}
              disabled={disabled}
              $marked={marked && on}
              $tall={tall}
              onClick={() => onChange(o.value)}
              onKeyDown={keys.onKeyDown}
            >
              {Boolean(o.art) && <Glyph aria-hidden="true">{o.art}</Glyph>}
              {Boolean(o.glyph) && <IconBadge aria-hidden="true">{o.glyph}</IconBadge>}
              <Text>{on ? <Chosen>{o.label}</Chosen> : <Label>{o.label}</Label>}</Text>
              {Boolean(o.trail) && <Trail>{o.trail}</Trail>}
              {on && marked && (
                <Mark aria-hidden="true">
                  <Icon.check width="12" height="12" />
                </Mark>
              )}
              {on && !marked && (
                <Tick aria-hidden="true">
                  <Icon.check width="18" height="18" />
                </Tick>
              )}
            </Choice>
          );
        })}
      </SetMenu>
    </div>
  );
}
