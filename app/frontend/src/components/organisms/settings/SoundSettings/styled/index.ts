// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// One control of the Sound group, as a row of its group's card.
export const Card = styled.div`
  padding: 14px var(--page-gutter, 16px) 16px;
`;

export const Head = styled.div`
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 9px;
  font-size: 15px;
`;

export const Name = styled.span`
  font-weight: 600;
`;

// The slider inset, so its knob stays inside the card at either end.
export const Inset = styled.div`
  display: flex;
  margin: 0 10px;
`;

// The current level; in the accent once it differs from the speaker's neutral value.
// The current level; where given, grey until it differs from the neutral value.
export const Level = styled.span<{ $changed?: boolean | undefined }>`
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  color: ${({ $changed }) => ($changed === false ? 'var(--ink-3)' : 'var(--ink)')};
`;

export const Scale = styled.div`
  display: flex;
  justify-content: space-between;
  margin-top: 1px;
  font-size: 12px;
  color: var(--ink-3);
  font-variant-numeric: tabular-nums;
`;
