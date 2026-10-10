// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { css } from 'styled-components';

export type ArtworkVariant = 'card' | 'player' | 'full';

const sizes: Record<ArtworkVariant, ReturnType<typeof css>> = {
  card: css`
    width: 52px;
    height: 52px;
    border-radius: var(--radius-sm);
  `,
  player: css`
    width: 52px;
    height: 52px;
    border-radius: var(--radius-sm);
    box-shadow:
      0 1px 2px rgba(31, 24, 20, 0.06),
      0 4px 10px -4px rgba(31, 24, 20, 0.1),
      inset 0 0 0 1px rgba(31, 24, 20, 0.04);
  `,
  full: css`
    width: 240px;
    height: 240px;
    border-radius: var(--radius);
    margin-top: 28px;
    box-shadow: 0 30px 60px -30px rgba(22, 24, 27, 0.45);
  `,
};

// The colours of a square without a station logo: muted, or (on a room card) tinted
// while its input is playing.
const idle: Record<ArtworkVariant, string> = {
  card: 'background: var(--surface-2); color: var(--ink-2);',
  player: 'background: var(--surface); color: var(--ink);',
  full: 'background: var(--surface-3); color: var(--ink-3);',
};

export const Square = styled.span<{
  $variant: ArtworkVariant;
  $color?: string;
  $highlighted?: boolean;
}>`
  position: relative;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  overflow: hidden;
  padding: 0 4px;
  ${({ $variant }) => sizes[$variant]}
  ${({ $variant, $color, $highlighted }) => {
    if ($color && $variant === 'player') return 'background: var(--surface); color: var(--ink);';
    if ($color) return `background: ${$color}; color: #fff;`;
    if ($highlighted) return 'background: var(--accent-soft); color: var(--accent-fill);';
    return idle[$variant];
  }}
`;
