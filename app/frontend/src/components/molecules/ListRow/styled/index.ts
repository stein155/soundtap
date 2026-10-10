// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// Stockholm's browse row: tall, a flat square image, no divider, no badge: the row is the
// button. Hover is for a mouse only, since on touch it sticks after the tap.
export const Row = styled.button`
  display: flex;
  align-items: center;
  gap: 20px;
  min-height: 78px;
  padding: 8px var(--page-gutter, 4px);
  margin-inline: calc(-1 * var(--page-gutter, 0px));
  width: calc(100% + 2 * var(--page-gutter, 0px));
  text-align: left;

  @media (hover: hover) {
    transition: background 160ms ease;

    &:hover {
      background: var(--surface-2);
    }
  }
`;

export const Art = styled.span`
  position: relative;
  width: 62px;
  height: 62px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  overflow: hidden;
  background: var(--surface);
  color: var(--ink);
`;

export const Text = styled.span`
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
`;

const line = `
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
`;

export const Title = styled.span`
  ${line}
  font-size: 14px;
  font-weight: 400;
  letter-spacing: -0.25px;
  color: var(--ink-2);
`;

export const Subtitle = styled.span`
  ${line}
  margin-top: 2px;
  font-size: 11px;
  font-weight: 400;
  letter-spacing: -0.4px;
  color: var(--ink-3);
`;
