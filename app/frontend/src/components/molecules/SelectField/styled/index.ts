// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Wrap = styled.div`
  position: relative;
`;

export const Select = styled.select<{ $shadow: string; $trail: boolean }>`
  width: 100%;
  appearance: none;
  min-height: 48px;
  padding: 0 ${({ $trail }) => ($trail ? 96 : 40)}px 0 12px;
  border: 0;
  border-radius: var(--radius-sm);
  outline: none;
  background: var(--surface-2);
  color: var(--ink);
  font-size: 16px; /* no iOS focus zoom */
  font-weight: 400;
  box-shadow: inset 0 0 0 1px var(--border-strong);
  cursor: pointer;

  &:focus-visible {
    outline: none;
    box-shadow: inset 0 0 0 2px var(--ink);
  }
`;

export const Trail = styled.span`
  position: absolute;
  top: 50%;
  right: 42px;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: var(--ink-3);
  pointer-events: none;
  transform: translateY(-50%);
`;

export const Chevron = styled.span`
  position: absolute;
  top: 50%;
  right: 12px;
  display: grid;
  color: var(--ink-3);
  pointer-events: none;
  transform: translateY(-50%) rotate(90deg);
`;
