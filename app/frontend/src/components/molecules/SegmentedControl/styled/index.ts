// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// The design's segmented choice: equal halves in a grey track, the chosen one filled in ink.
export const Track = styled.div`
  display: grid;
  grid-auto-columns: 1fr;
  grid-auto-flow: column;
  gap: 3px;
  padding: 3px;
  border-radius: var(--radius);
  background: var(--surface-2);
`;

export const Segment = styled.button<{ $selected: boolean }>`
  min-height: 40px;
  padding: 6px 10px;
  border-radius: 5px;
  font-size: 14px;
  font-weight: 600;
  color: ${({ $selected }) => ($selected ? 'var(--on-accent-fill)' : 'var(--ink-2)')};
  background: ${({ $selected }) => ($selected ? 'var(--accent-fill)' : 'transparent')};
`;
