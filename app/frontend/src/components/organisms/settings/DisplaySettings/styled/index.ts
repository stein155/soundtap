// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// The standby screen as the speaker shows it: the time alone, light on black.
export const Preview = styled.div`
  height: 92px;
  margin: 0 0 4px;
  display: grid;
  place-items: center;
  border-radius: 7px;
  background: #111;
  /* Its edge, for the dark theme's page that is nearly as black. */
  box-shadow: inset 0 0 0 1px var(--border);
  font-size: 34px;
  font-weight: 500;
  letter-spacing: 0.04em;
  color: rgba(255, 255, 255, 0.8);
`;

// A card's inside with room around its controls (the clock format).
export const Pad = styled.div`
  padding: 14px var(--page-gutter, 16px) 16px;
`;
