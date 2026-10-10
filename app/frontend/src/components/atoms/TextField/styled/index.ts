// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { css } from 'styled-components';

const field = css<{ $shadow: string }>`
  width: 100%;
  padding: 15px 16px;
  border: 1.5px solid transparent;
  border-radius: var(--radius);
  outline: none;
  background: var(--surface);
  color: var(--ink);
  font-size: 16px; /* no iOS focus zoom */
  font-weight: 500;
  box-shadow: ${({ $shadow }) => $shadow};
  transition:
    box-shadow 200ms ease,
    border-color 160ms ease;

  &::placeholder {
    color: var(--ink-3);
    font-weight: 400;
  }

  &:focus {
    box-shadow: var(--shadow);
    border-color: color-mix(in oklab, var(--accent) 55%, transparent);
  }

  /* The border above is the focus ring; the global outline would add a second one. */
  &:focus-visible {
    outline: none;
  }
`;

export const Input = styled.input<{ $shadow: string }>`
  ${field}
`;

export const Area = styled.textarea<{ $shadow: string }>`
  ${field}
  min-height: 86px;
  resize: none;
  line-height: 1.45;
`;
