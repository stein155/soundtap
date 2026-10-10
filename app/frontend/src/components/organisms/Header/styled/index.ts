// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { keyframes } from 'styled-components';
import { reducedMotion } from '../../../../theme/keyframes';
import { hitArea } from '../../../../theme/hitArea';

const wave = keyframes`
  0%, 60%, 100% { transform: rotate(0); }
  10%, 30% { transform: rotate(14deg); }
  20% { transform: rotate(-8deg); }
  40% { transform: rotate(-4deg); }
  50% { transform: rotate(10deg); }
`;

// Glows in --hdr-tint: a gradient behind, and a soft circle in the top-right corner.
export const Bar = styled.header<{ $tint: string }>`
  --hdr-tint: ${({ $tint }) => $tint};
  position: relative;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: max(30px, calc(env(safe-area-inset-top) + 12px)) 20px 18px;
  background: linear-gradient(
    180deg,
    color-mix(in oklab, var(--hdr-tint) 22%, var(--bg)) 0%,
    color-mix(in oklab, var(--hdr-tint) 8%, var(--bg)) 60%,
    var(--bg) 100%
  );
  transition: background 600ms ease;

  &::after {
    content: '';
    position: absolute;
    top: -60px;
    right: -40px;
    width: 200px;
    height: 200px;
    border-radius: 50%;
    background: radial-gradient(
      circle,
      color-mix(in oklab, var(--hdr-tint) 32%, transparent),
      transparent 70%
    );
    pointer-events: none;
  }

  & > * {
    position: relative;
    z-index: 1;
  }
`;

export const Headings = styled.div`
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
`;

export const Title = styled.h1`
  margin: 0;
  display: inline-flex;
  align-items: center;
  font-size: 18px;
  font-weight: 700;
  letter-spacing: var(--tracking-title);
`;

export const Emoji = styled.span<{ $waving?: boolean }>`
  display: inline-block;
  margin-right: 8px;
  transform-origin: 70% 80%;
  animation: ${({ $waving }) => ($waving ? wave : 'none')} 1.8s ease-in-out 0.4s 2;
  ${reducedMotion}
`;

export const Subtitle = styled.p`
  margin: 0;
  font-size: 13px;
  font-weight: 500;
  color: var(--ink-3);
`;

export const SettingsButton = styled.button`
  ${hitArea()}
  width: 36px;
  height: 36px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  border-radius: 50%;
  background: color-mix(in srgb, var(--surface) 80%, transparent);
  color: var(--ink-2);
  box-shadow:
    0 1px 2px rgba(22, 24, 27, 0.06),
    0 0 0 1px var(--border);
  transition:
    transform 160ms ease,
    color 160ms ease,
    background 160ms ease;

  &:hover {
    background: var(--surface);
    color: var(--ink);
    transform: rotate(35deg);
  }

  &:active {
    transform: rotate(35deg) scale(0.94);
  }
`;
