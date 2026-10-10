// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Overlay = styled.div`
  position: fixed;
  inset: 0;
  z-index: 90;
  display: grid;
  place-items: center;
  background: rgba(22, 24, 27, 0.4);
  backdrop-filter: blur(6px);
`;

export const Card = styled.div`
  max-width: 300px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 28px 24px;
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: var(--shadow-lg);
  text-align: center;
`;

export const Title = styled.h2`
  margin: 0;
  font-size: 18px;
  font-weight: 700;
  letter-spacing: var(--tracking-title);
`;

export const Text = styled.p`
  margin: 0;
  font-size: 13.5px;
  line-height: 1.5;
  color: var(--ink-2);
`;

export const ReleaseNotes = styled.span`
  white-space: pre-wrap;
`;
