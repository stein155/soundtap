// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { css } from 'styled-components';

export type ButtonVariant =
  'primary' | 'stockholm' | 'ghost' | 'danger' | 'delete' | 'inline' | 'dashed' | 'spotify';

// Stockholm has one button: flat, 54 px, radius 7, small capitals, no lift or shadow. Solid
// dims when pressed; outlined thickens its border, drawn inset so nothing moves.
const box = css`
  min-height: 54px;
  padding: 11px;
  border-radius: var(--radius);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.2px;
  text-transform: uppercase;
  &:disabled {
    opacity: 0.4;
    cursor: default;
  }
`;

const solid = css`
  ${box}
  background: var(--accent);
  color: var(--on-accent);
  &:active:not(:disabled) {
    opacity: 0.7;
  }
`;

const outline = (colour: string) => css`
  ${box}
  background: transparent;
  color: ${colour};
  box-shadow: inset 0 0 0 1.5px ${colour};
  &:active:not(:disabled) {
    box-shadow: inset 0 0 0 3px ${colour};
  }
`;

const variants: Record<ButtonVariant, ReturnType<typeof css>> = {
  primary: css`
    flex: 1;
    ${solid}
  `,
  stockholm: css`
    width: 100%;
    ${solid}
  `,
  danger: css`
    flex: 1;
    ${outline('var(--danger)')}
  `,
  ghost: outline('var(--ink)'),
  delete: css`
    width: 100%;
    margin-top: 14px;
    ${outline('var(--danger)')}
  `,
  inline: css`
    flex-shrink: 0;
    ${outline('var(--ink)')}
    min-height: 32px;
    padding: 7px 12px;
  `,
  dashed: css`
    width: 100%;
    margin-top: 10px;
    ${outline('var(--ink)')}
  `,
  // Spotify's brand rule: its green, with dark text, which white would fail on.
  spotify: css`
    margin-top: 10px;
    min-height: 40px;
    padding: 7px 26px;
    border-radius: 999px;
    background: #1db954;
    color: #121212;
    font-size: 12px;
    font-weight: 700;
    letter-spacing: 1px;
    text-transform: uppercase;
    &:active {
      opacity: 0.7;
    }
  `,
};

type Look = { $variant: ButtonVariant; $wide?: boolean; $small?: boolean };

const base = css<Look>`
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  text-decoration: none;
  ${({ $variant }) => variants[$variant]}
  ${({ $wide }) =>
    $wide &&
    css`
      width: 100%;
      margin-top: 12px;
    `}
  ${({ $small }) =>
    $small &&
    css`
      align-self: flex-start;
      margin-top: 6px;
      min-height: 40px;
      padding: 7px 18px;
    `}
`;

export const ButtonEl = styled.button<Look>`
  ${base}
`;

export const LinkEl = styled.a<Look>`
  ${base}
`;
