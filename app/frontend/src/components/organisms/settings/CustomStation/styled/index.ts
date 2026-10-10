// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// Tall enough to fill the sheet's body, so the footer sits at the bottom as in Stockholm.
export const Form = styled.form`
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-height: calc(100dvh - 108px - env(safe-area-inset-top) - env(safe-area-inset-bottom));

  @media (min-width: 720px) and (min-height: 600px) {
    min-height: calc(min(852px, 100dvh - 64px) - 108px);
  }
`;

export const Footer = styled.div`
  display: grid;
  gap: 8px;
  margin-top: auto;
  padding-top: 16px;
  text-align: center;
`;
