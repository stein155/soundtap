// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// Sticks to the bottom of the sheet's scrolling body, fading the form out beneath it.
export const Bar = styled.div`
  position: sticky;
  bottom: -32px;
  z-index: 2;
  margin: 18px -20px -32px;
  padding: 14px 20px max(20px, env(safe-area-inset-bottom));
  background: linear-gradient(180deg, transparent, var(--bg) 30%);
`;
