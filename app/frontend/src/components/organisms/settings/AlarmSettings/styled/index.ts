// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// The card with the time and the preset: labelled fields with room around them.
export const Fields = styled.div`
  display: grid;
  gap: 18px;
  padding: 18px var(--page-gutter, 16px);

  input[type='time'] {
    font-size: 20px;
    font-weight: 600;
  }
`;
