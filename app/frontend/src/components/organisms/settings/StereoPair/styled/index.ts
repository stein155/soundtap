// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { Bar } from '../../../../molecules/SaveBar/styled';

// The side this speaker plays, under its bar: two halves in a grey track, the chosen one
// filled.
export const SideBox = styled.div`
  padding: 14px var(--page-gutter, 16px) 16px;
`;

export const Sides = styled.div`
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 3px;
  padding: 3px;
  border-radius: var(--radius);
  background: var(--surface-2);
`;

export const SideButton = styled.button<{ $on: boolean }>`
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 44px;
  border-radius: 5px;
  background: ${({ $on }) => ($on ? 'var(--accent-fill)' : 'transparent')};
  color: ${({ $on }) => ($on ? 'var(--on-accent-fill)' : 'var(--ink-2)')};
  font-size: 14px;
  font-weight: 600;
`;

// The side's letter in a small white disc.
export const Letter = styled.span<{ $on: boolean }>`
  width: 22px;
  height: 22px;
  display: inline-grid;
  place-items: center;
  border-radius: 50%;
  background: var(--surface);
  box-shadow: ${({ $on }) => ($on ? 'none' : 'inset 0 0 0 1px var(--border-strong)')};
  color: ${({ $on }) => ($on ? 'var(--ink)' : 'var(--ink-2)')};
  font-size: 11px;
  font-weight: 700;
`;

export const Then = styled.p`
  margin: 12px 0 0;
  font-size: 13px;
  line-height: 1.45;
  color: var(--ink-3);
`;

// The pair button sits in the white bar at the bottom of the page, as a Save does.
export const Footer = styled(Bar)`
  display: grid;
  gap: 12px;
`;
