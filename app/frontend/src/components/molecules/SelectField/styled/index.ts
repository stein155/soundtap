// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Wrap = styled.div`
  position: relative;
`;

export const Select = styled.select<{ $shadow: string; $trail: boolean }>`
  width: 100%;
  appearance: none;
  padding: 15px ${({ $trail }) => ($trail ? 96 : 44)}px 15px 16px;
  border: 1.5px solid transparent;
  border-radius: var(--radius);
  outline: none;
  background: var(--surface);
  color: var(--ink);
  font-size: 16px; /* no iOS focus zoom */
  font-weight: 500;
  box-shadow: ${({ $shadow }) => $shadow};
  cursor: pointer;
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
  right: 14px;
  display: grid;
  color: var(--ink-3);
  pointer-events: none;
  transform: translateY(-50%) rotate(90deg);
`;
