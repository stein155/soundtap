// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

const centred = `
  position: absolute;
  top: 50%;
  transform: translateY(-50%);
`;

export const Track = styled.div<{ $tall?: boolean }>`
  position: relative;
  flex: 1;
  display: flex;
  align-items: center;
  height: ${({ $tall }) => ($tall ? 26 : 24)}px;
  cursor: pointer;
  /* Vertical swipes scroll the page; sideways ones are the slider's. */
  touch-action: pan-y;
`;

export const Rail = styled.div`
  ${centred}
  left: 0;
  right: 0;
  height: 6px;
  border-radius: 999px;
  background: var(--surface-3);
`;

export const Fill = styled.div`
  ${centred}
  height: 6px;
  border-radius: 999px;
  background: var(--accent-fill);
`;

export const OriginMark = styled.div`
  position: absolute;
  top: 50%;
  width: 2px;
  height: 14px;
  border-radius: 999px;
  background: var(--border-strong);
  transform: translate(-50%, -50%);
`;

export const Thumb = styled.div`
  position: absolute;
  top: 50%;
  width: 20px;
  height: 20px;
  border-radius: 999px;
  background: var(--thumb);
  box-shadow:
    0 2px 8px -1px rgba(31, 24, 20, 0.18),
    0 0 0 1px rgba(31, 24, 20, 0.08);
  transform: translate(-50%, -50%);
  transition: transform 120ms ease;

  ${Track}:active & {
    transform: translate(-50%, -50%) scale(1.18);
  }
`;
