// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Field = styled.div`
  display: grid;
  gap: 8px;
`;

// The design's field label: small capitals in grey.
export const Label = styled.label`
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.5px;
  text-transform: uppercase;
  color: var(--ink-3);
`;
