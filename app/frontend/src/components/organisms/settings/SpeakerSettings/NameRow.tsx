// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { RowInput } from '../../../molecules/FieldRow';
import { SetMenu } from '../../../molecules/SetMenu';
import { NAME_MAX_LENGTH } from '../../../../lib/limits';

const Row = styled.label`
  min-height: 59px;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 8px var(--page-gutter, 14px);
  font-size: 14px;
  letter-spacing: -0.25px;
  color: var(--ink-2);
`;

const Gap = styled.div`
  margin-bottom: 12px;
`;

// Stockholm's Speaker Name: a plain row on the page, typed into in place.
export function NameRow({
  label,
  value,
  placeholder,
  onChange,
}: {
  label: string;
  value: string;
  placeholder: string;
  onChange: (name: string) => void;
}) {
  return (
    <Gap>
      <SetMenu>
        <Row>
          {label}
          <RowInput
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder={placeholder}
            maxLength={NAME_MAX_LENGTH}
            autoComplete="off"
          />
        </Row>
      </SetMenu>
    </Gap>
  );
}
