// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { hitArea } from '../../../../theme/hitArea';

// The field as the design draws it: a grey rounded well with the magnifier in front.
export const SearchBox = styled.label`
  display: flex;
  align-items: center;
  gap: 8px;
  height: 40px;
  padding: 0 4px 0 12px;
  background: var(--surface-2);
  border-radius: var(--radius);
  color: var(--ink-3);

  /* The focus line belongs to the whole field, not to the bare input inside it. */
  &:focus-within {
    box-shadow: inset 0 0 0 1.5px var(--ink);
  }

  & > svg {
    flex: none;
    width: 18px;
    height: 18px;
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
  font-weight: 500;
  appearance: none;

  &:focus-visible {
    outline: none;
  }

  /* The sheet has its own clear button. */
  &::-webkit-search-cancel-button {
    display: none;
  }

  &::placeholder {
    color: var(--ink-3);
    font-weight: 400;
  }
`;

// A small grey disc with a white cross, in a target big enough for a finger.
export const ClearButton = styled.button`
  ${hitArea()}
  flex: none;
  width: 32px;
  height: 32px;
  display: grid;
  place-items: center;
  background: transparent;

  & > span {
    width: 18px;
    height: 18px;
    display: grid;
    place-items: center;
    border-radius: 50%;
    background: var(--ink-3);
    color: var(--surface);
  }

  &:hover > span {
    background: var(--ink);
  }
`;

// The way to a station TuneIn does not list: a row of its own under the results.
export const LinkRow = styled.button`
  display: flex;
  align-items: center;
  gap: 14px;
  width: calc(100% + 2 * var(--page-gutter, 0px));
  min-height: 60px;
  margin: 12px calc(-1 * var(--page-gutter, 0px)) 0;
  padding: 8px var(--page-gutter, 16px);
  background: var(--surface);
  box-shadow:
    0 -1px 0 var(--border),
    0 1px 0 var(--border);
  text-align: left;
  color: var(--ink);

  & > svg {
    flex: none;
    width: 22px;
    height: 22px;
  }

  & > svg:last-child {
    width: 18px;
    height: 18px;
    color: var(--ink-3);
  }

  &:active {
    background: var(--surface-2);
  }
`;

export const LinkText = styled.span`
  flex: 1;
  min-width: 0;
  font-size: 15px;
  font-weight: 600;

  & small {
    display: block;
    margin-top: 2px;
    font-size: 12px;
    font-weight: 400;
    color: var(--ink-3);
  }
`;
