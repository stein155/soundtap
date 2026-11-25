// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { Bar } from '../../../../molecules/SaveBar/styled';

// One line of the page's cards: a glyph, the text and what goes with it on the right, as
// the settings rows draw it.
export const Row = styled.div`
  min-height: 52px;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 4px var(--page-gutter, 14px);
  font-size: 15px;
  color: var(--ink);
`;

export const Glyph = styled.span`
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  color: var(--ink);
`;

export const Name = styled.span`
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
`;

export const Value = styled.span`
  font-weight: 600;
  color: var(--ink);
`;

// "Connected", with the dot a lamp would show.
export const Connected = styled.span`
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--ink);

  &::before {
    content: '';
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: currentColor;
  }
`;

export const Forget = styled.span`
  flex-shrink: 0;

  button {
    font-size: 13px;
    font-weight: 600;
  }
`;

// While the speaker can be found: a pulsing dot and what to do on the phone.
export const Findable = styled.p`
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0 0 12px;
  font-size: 13px;
  line-height: 1.45;
  color: var(--ink-2);

  &::before {
    content: '';
    width: 10px;
    height: 10px;
    flex-shrink: 0;
    border-radius: 50%;
    background: var(--ink);
    box-shadow: 0 0 0 4px var(--border-strong);
  }
`;

// The pair button and what it started, in the white bar at the bottom of the page.
export const Footer = styled(Bar)``;
