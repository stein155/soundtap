// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { css } from 'styled-components';

// The design's field: grey, a fine edge that turns into a 2 px ink ring on focus. $shadow is
// kept for the callers that pass it; the edge is drawn the same on every surface.
const field = css<{ $shadow: string }>`
  width: 100%;
  min-height: 48px;
  padding: 0 12px;
  border: 0;
  border-radius: var(--radius-sm);
  outline: none;
  background: var(--surface-2);
  color: var(--ink);
  font-size: 16px; /* no iOS focus zoom */
  font-weight: 400;
  box-shadow: inset 0 0 0 1px var(--border-strong);
  transition: box-shadow var(--dur-fast) var(--ease);

  &::placeholder {
    color: var(--ink-3);
    font-weight: 400;
  }

  &:focus {
    box-shadow: inset 0 0 0 2px var(--ink);
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
  padding: 12px;
  resize: none;
  line-height: 1.45;
`;
