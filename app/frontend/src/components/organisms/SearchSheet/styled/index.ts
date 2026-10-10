// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { hitArea } from '../../../../theme/hitArea';

export const SearchBox = styled.label`
  display: flex;
  align-items: center;
  gap: 10px;
  height: 48px;
  padding: 0 16px;
  background: var(--surface);
  border: 1.5px solid var(--border-strong);
  border-radius: var(--radius-sm);
  color: var(--ink-3);
  transition: border-color 200ms ease;

  /* The focus line belongs to the whole field, not to the bare input inside it. */
  &:focus-within {
    border-color: var(--ink);
  }
`;

export const SearchInput = styled.input`
  flex: 1;
  min-width: 0;
  border: 0;
  outline: none;
  background: transparent;
  color: var(--ink);
  /* 16px: iOS zooms the page into any input set smaller, and stays zoomed. */
  font-size: 16px;
  appearance: none;

  &:focus-visible {
    outline: none;
  }

  /* The sheet has its own clear button. */
  &::-webkit-search-cancel-button {
    display: none;
  }
  font-weight: 500;
  letter-spacing: 0.04em;

  &::placeholder {
    color: var(--ink-3);
    font-weight: 400;
  }
`;

export const ClearButton = styled.button`
  ${hitArea()}
  width: 24px;
  height: 24px;
  display: grid;
  place-items: center;
  border-radius: var(--radius-sm);
  background: var(--surface-3);
  color: var(--ink-2);

  &:hover {
    background: var(--ink);
    color: var(--on-ink);
  }
`;
