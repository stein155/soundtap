// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// The row's glyph, a plain ink line drawing; on a grey disc for a route to take.
export const IconBadge = styled.span<{ $disc?: boolean | undefined }>`
  width: ${({ $disc }) => ($disc === true ? '44px' : '22px')};
  height: ${({ $disc }) => ($disc === true ? '44px' : '22px')};
  flex-shrink: 0;
  display: grid;
  place-items: center;
  border-radius: 50%;
  background: ${({ $disc }) => ($disc === true ? 'var(--surface-2)' : 'none')};
  color: var(--ink);

  svg {
    stroke-width: 1.8;
  }
`;

// The label on the left, its current value on the right of the same line.
export const Text = styled.span`
  flex: 1;
  min-width: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0 12px;
`;

export const Label = styled.span<{ $strong?: boolean | undefined }>`
  flex-shrink: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 15px;
  font-weight: 400;
  color: var(--ink);
  ${({ $strong }) => ($strong === true ? 'font-weight: 600; color: var(--ink);' : '')}

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
  color: var(--ink-3);
`;

// A switch row with a line under its label has a little more room around its text, as in
// the design.
export const Row = styled.button<{ $danger?: boolean | undefined; $roomy?: boolean | undefined }>`
  width: 100%;
  min-height: 52px;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: ${({ $roomy }) => ($roomy === true ? 12 : 10)}px var(--page-gutter, 16px);
  text-align: left;

  ${({ $danger }) => ($danger === true ? '& span { color: var(--danger); }' : '')}

  /* The hairline above a row (SetMenu's) comes on top of its height, as in the design. */
  & + & {
    min-height: 53px;
  }

  &:disabled {
    opacity: 0.45;
    cursor: default;
  }

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
  width: 48px;
  height: 48px;
  padding: 6px;
  border-radius: 50%;
  background: var(--surface-2);
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
  background: ${({ $on }) => ($on ? 'var(--switch-on)' : 'var(--border-strong)')};
  transition: background 180ms ease;

  &::after {
    content: '';
    position: absolute;
    top: 2px;
    left: 2px;
    width: 27px;
    height: 27px;
    border-radius: 50%;
    background: #ffffff;
    box-shadow: 0 2px 4px rgba(0, 0, 0, 0.2);
    transform: translateX(${({ $on }) => ($on ? '20px' : '0')});
    transition: transform 180ms ease;
  }
`;

// The row's "opens a page" sign.
export const Chevron = styled.span`
  flex-shrink: 0;
  display: grid;
  place-items: center;
  color: var(--ink-3);

  svg {
    stroke-width: 2;
  }
`;

// A line under the label that says what the row does.
// Under a name (a speaker's model) it sits close, as one block with the name.
export const Desc = styled.span<{ $close?: boolean | undefined }>`
  flex-basis: 100%;
  margin-top: ${({ $close }) => ($close === true ? '0' : '2px')};
  font-size: 12px;
  line-height: ${({ $close }) => ($close === true ? 'normal' : '1.45')};
  color: var(--ink-3);
`;

// What waits on the row (an update), in the amber of a notice; in ink on the speaker's own
// Updates row.
export const Badge = styled.span<{ $ink?: boolean | undefined }>`
  flex-shrink: 0;
  padding: 3px 7px;
  border-radius: var(--radius-sm);
  background: ${({ $ink }) => ($ink === true ? 'var(--accent-fill)' : 'var(--warn-bg)')};
  box-shadow: ${({ $ink }) => ($ink === true ? 'none' : 'inset 0 0 0 1px var(--warn-line)')};
  color: ${({ $ink }) => ($ink === true ? 'var(--on-accent-fill)' : 'var(--warn-ink)')};
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.5px;
  text-transform: uppercase;
`;
