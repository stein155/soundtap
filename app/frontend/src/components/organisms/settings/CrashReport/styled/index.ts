// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Notice = styled.section`
  display: grid;
  gap: 12px;
  padding: 14px 16px;
  border-radius: var(--radius);
  background: var(--surface-2);
`;

export const Actions = styled.div`
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 16px;
`;

export const Label = styled.h3`
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-3);
`;

// The report as it will leave: kept as written, scrolled rather than reflowed.
export const Preview = styled.pre`
  max-height: 320px;
  margin: 0;
  padding: 10px 12px;
  overflow: auto;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--ink);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.45;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
`;
