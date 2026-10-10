// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// The row's glyph, a plain ink line drawing.
export const IconBadge = styled.span`
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  color: var(--ink-2);
`;

// The label on the left, its current value on the right of the same line.
export const Text = styled.span`
  flex: 1;
  min-width: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 2px 12px;
`;

export const Label = styled.span`
  flex-shrink: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 14px;
  font-weight: 400;
  letter-spacing: -0.25px;
  color: var(--ink-2);

  &:not(:only-child) {
    max-width: 70%;
  }

  /* Alone on its row a long translation still has to stay on the screen. */
  &:only-child {
    flex-shrink: 1;
  }
`;

// Too long for the label's line it drops to its own line, and wraps there: a value cut off
// could not be read at all.
export const Meta = styled.span`
  flex: 1 1 auto;
  min-width: 0;
  overflow-wrap: anywhere;
  text-align: right;
  font-size: 13px;
  letter-spacing: -0.2px;
  color: var(--ink-3);
`;

export const Row = styled.button`
  width: 100%;
  min-height: 59px;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 8px var(--page-gutter, 14px);
  text-align: left;

  @media (hover: hover) {
    transition: background 140ms ease;

    &:hover {
      background: var(--surface-2);
    }
  }
`;

// A speaker's drawing, in the space a tile would take.
export const Glyph = styled.span`
  /* A near-black speaker drawing needs lifting on the dark theme. */
  [data-theme='dark'] & svg {
    filter: brightness(1.8);
  }
  width: 36px;
  height: 36px;
  flex-shrink: 0;
  display: grid;
  place-items: center;

  svg {
    width: 100%;
    height: 100%;
  }
`;

export const Switch = styled.span<{ $on: boolean }>`
  position: relative;
  flex-shrink: 0;
  width: 51px;
  height: 31px;
  border-radius: 999px;
  background: ${({ $on }) => ($on ? 'var(--accent-fill)' : 'var(--border-strong)')};
  transition: background 180ms ease;

  &::after {
    content: '';
    position: absolute;
    top: 2px;
    left: 2px;
    width: 27px;
    height: 27px;
    border-radius: 50%;
    background: ${({ $on }) => ($on ? 'var(--on-accent-fill)' : 'var(--thumb)')};
    box-shadow: 0 2px 4px rgba(0, 0, 0, 0.2);
    transform: translateX(${({ $on }) => ($on ? '20px' : '0')});
    transition: transform 180ms ease;
  }
`;
