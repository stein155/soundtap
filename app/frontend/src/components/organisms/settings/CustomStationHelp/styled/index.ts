// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// A row across the whole width that folds open: the question, and the reasons under it.
export const Details = styled.details`
  margin: 16px calc(-1 * var(--page-gutter, 0px)) 0;
  background: var(--surface);
  border-block: 1px solid var(--border-strong);
`;

export const Summary = styled.summary`
  display: flex;
  align-items: center;
  gap: 14px;
  min-height: 52px;
  padding: 0 var(--page-gutter, 16px);
  list-style: none;
  cursor: pointer;
  font-size: 15px;
  color: var(--ink);

  &::-webkit-details-marker {
    display: none;
  }

  & > span {
    flex: 1;
  }

  & > svg {
    flex: none;
  }

  & > svg:last-child {
    color: var(--ink-3);
    transition: transform var(--dur-fast) var(--ease);
  }

  details[open] > & > svg:last-child {
    transform: rotate(180deg);
  }
`;

export const Reasons = styled.ul`
  margin: 0;
  padding: 0;
  list-style: none;
  border-top: 1px solid var(--border);
`;

export const Reason = styled.li`
  display: flex;
  gap: 14px;
  padding: 12px var(--page-gutter, 16px);
  color: var(--ink);

  & + & {
    border-top: 1px solid var(--border);
  }

  & > svg {
    flex: none;
    margin-top: 1px;
  }
`;

export const ReasonText = styled.span`
  & strong {
    display: block;
    font-size: 14px;
    font-weight: 600;
  }

  & span {
    display: block;
    margin-top: 2px;
    font-size: 12px;
    line-height: 1.45;
    color: var(--ink-3);
  }
`;
