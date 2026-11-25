// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { css } from 'styled-components';

// A room is a plain full-width row: the page's gutter is its padding, a 1px line closes it.
export const Card = styled.div<{ $active?: boolean | undefined; $offline?: boolean | undefined }>`
  display: flex;
  flex-direction: column;
  justify-content: center;
  min-height: 76px;
  margin-inline: calc(-1 * var(--page-gutter, 0px));
  padding: 8px var(--page-gutter, 0px);
  border-bottom: 1px solid var(--border-strong);

  &:first-child {
    border-top: 1px solid var(--border-strong);
  }
  ${({ $offline }) =>
    $offline === true &&
    css`
      opacity: 0.7;
    `}
`;

export const Row = styled.div`
  display: flex;
  align-items: center;
  gap: 6px;
`;

// The speaker's own drawing, with no tile around it.
export const Drawing = styled.span`
  width: 44px;
  height: 52px;
  flex-shrink: 0;
  display: grid;
  place-items: center;

  & > svg {
    width: 100%;
    height: 100%;
  }
`;

export const Main = styled.button`
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 16px;
  text-align: left;

  &:disabled {
    cursor: default;
  }
`;

export const Text = styled.span`
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
`;

export const Name = styled.span`
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 15px;
  font-weight: 600;
  letter-spacing: 0.1em;
  text-transform: uppercase;
`;

export const Status = styled.span<{ $on?: boolean | undefined }>`
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 12.5px;
  font-weight: 500;
  color: ${({ $on }) => ($on === true ? 'var(--ink-2)' : 'var(--ink-3)')};
`;

export const StatusText = styled.span`
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
`;

export const Check = styled.span`
  display: grid;
  flex-shrink: 0;
  color: var(--accent);
`;

export const RetryButton = styled.button`
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  border-radius: 999px;
  background: var(--surface);
  color: var(--ink);
  box-shadow: 0 0 0 1px var(--border-strong);
  font-size: 12.5px;
  font-weight: 600;
`;
