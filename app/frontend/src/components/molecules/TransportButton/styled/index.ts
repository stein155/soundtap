// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { css } from 'styled-components';
import { hitArea } from '../../../../theme/hitArea';

export type TransportVariant = 'card' | 'player' | 'full';

type Look = { $variant: TransportVariant; $on: boolean };

const looks: Record<TransportVariant, ReturnType<typeof css<Look>>> = {
  // On a room card: quiet while the room is silent, the accent while it plays.
  card: css<Look>`
    width: 38px;
    height: 38px;
    background: ${({ $on }) => ($on ? 'var(--accent-fill)' : 'var(--surface-2)')};
    color: ${({ $on }) => ($on ? 'var(--on-accent-fill)' : 'var(--ink-2)')};
    transition:
      background 160ms ease,
      color 160ms ease;
  `,
  player: css<Look>`
    width: 42px;
    height: 42px;
    background: var(--accent-fill);
    color: var(--on-accent-fill);
    box-shadow: 0 6px 14px -6px color-mix(in oklab, var(--accent) 70%, transparent);
    transition: transform 180ms ease;
    &:hover {
      transform: scale(1.06);
    }
    &:active {
      transform: scale(0.96);
    }
  `,
  full: css<Look>`
    width: 60px;
    height: 60px;
    margin-top: 18px;
    background: var(--ink);
    color: var(--on-ink);
  `,
};

export const Round = styled.button<Look>`
  ${hitArea()}
  flex-shrink: 0;
  display: grid;
  place-items: center;
  border-radius: 50%;
  ${({ $variant }) => looks[$variant]}
`;
