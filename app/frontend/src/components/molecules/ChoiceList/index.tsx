// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useRadioKeys } from '../../../hooks/useRadioKeys';
import { Icon } from '../../atoms/Icon';
import { SetMenu } from '../SetMenu';
import { Label, Row, Text } from '../SetItem/styled';
import { Tick } from './styled';

interface Props<V extends string> {
  label: string;
  value: V;
  options: { value: V; label: string }[];
  onChange: (value: V) => void;
}

// A pick-one list as a page of rows, the chosen one ticked (Sonos and iOS style), in place
// of a dropdown.
export function ChoiceList<V extends string>({ label, value, options, onChange }: Props<V>) {
  const keys = useRadioKeys(
    options.length,
    options.findIndex((o) => o.value === value),
    (i) => onChange(options[i].value)
  );
  return (
    // The focus is on the checked radio inside (roving tabindex), not on the group, so the
    // radios take the keys.
    <div role="radiogroup" aria-label={label}>
      <SetMenu>
        {options.map((o, i) => (
          <Row
            key={o.value}
            type="button"
            role="radio"
            aria-checked={o.value === value}
            tabIndex={keys.tabIndex(i)}
            onClick={() => onChange(o.value)}
            onKeyDown={keys.onKeyDown}
          >
            <Text>
              <Label>{o.label}</Label>
            </Text>
            {o.value === value && (
              <Tick aria-hidden="true">
                <Icon.check width="18" height="18" />
              </Tick>
            )}
          </Row>
        ))}
      </SetMenu>
    </div>
  );
}
