// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Card = styled.div<{ $shadow: string }>`
  padding: 16px 18px 14px;
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: ${({ $shadow }) => $shadow};
`;

export const Head = styled.div`
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 14px;
`;

export const Name = styled.span`
  font-size: 15px;
  font-weight: 700;
`;

// The current level; in the accent once it differs from the speaker's neutral value.
export const Level = styled.span<{ $changed: boolean }>`
  font-size: 17px;
  font-weight: 800;
  font-variant-numeric: tabular-nums;
  color: ${({ $changed }) => ($changed ? 'var(--accent)' : 'var(--ink-3)')};
  transition: color 160ms ease;
`;

export const Scale = styled.div`
  display: flex;
  justify-content: space-between;
  margin-top: 9px;
  font-size: 11.5px;
  font-weight: 600;
  color: var(--ink-3);
  font-variant-numeric: tabular-nums;
`;
