// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { css } from 'styled-components';

// One row of a FieldCard, inside the page's side gutter; every row after the first gets
// a full-width line above it, as Stockholm's settings rows.
export const rowLayout = css`
  position: relative;
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 59px;
  padding: 8px var(--page-gutter, 16px);

  & + &::before {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 1px;
    background: var(--border);
  }
`;

export const Row = styled.div<{ $value?: boolean | undefined }>`
  ${rowLayout}
  ${({ $value }) =>
    $value === true &&
    css`
      justify-content: space-between;
      font-size: 14px;
    `}
`;

export const Label = styled.label<{ $wide?: boolean | undefined }>`
  flex: ${({ $wide }) => ($wide === true ? 1 : '0 0 96px')};
  font-size: 14px;
  color: var(--ink-3);
`;

export const Value = styled.span`
  min-width: 0;
  text-align: right;
  color: var(--ink);
  overflow-wrap: anywhere;
`;

const bare = css`
  min-width: 0;
  border: 0;
  outline: none;
  background: transparent;
  color: var(--ink);
  /* 16px: iOS zooms the page into any smaller input on focus and stays zoomed. */
  font-size: 16px;
  font-weight: 500;
`;

// Row inputs hold network names, passwords, hosts and user names: case and spelling are
// exact, so the phone keyboard must not "fix" them. A caller can still opt in.
export const RowInput = styled.input.attrs<{ $align?: 'left' | 'right' }>((p) => ({
  autoCapitalize: p.autoCapitalize ?? 'none',
  autoCorrect: p.autoCorrect ?? 'off',
  spellCheck: p.spellCheck ?? false,
}))<{ $align?: 'left' | 'right' }>`
  ${bare}
  flex: 1;
  text-align: ${({ $align = 'right' }) => $align};

  &::placeholder {
    color: var(--ink-3);
    font-weight: 400;
  }

  /* The caret already shows where typing goes; the global keyboard ring would draw a
     square box inside the row (the station search had the same one). */
  &:focus-visible {
    outline: none;
  }
`;

export const RowSelect = styled.select`
  ${bare}
  max-width: 60%;
  text-align: right;
`;

export const RowTime = styled.input`
  ${bare}
  max-width: 110px;
  text-align: right;
  font-variant-numeric: tabular-nums;

  &:focus-visible {
    outline: none;
  }
`;
