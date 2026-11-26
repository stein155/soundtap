// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { keyframes } from 'styled-components';

// A main page taking over from another.
const pageIn = keyframes`
  from {
    opacity: 0;
  }
`;

// One viewport tall, so only the middle scrolls and the dock stays at the bottom. In a wide
// window the same column as Stockholm's wide layout, up to --shell-max.
export const Frame = styled.div`
  position: relative;
  width: 100%;
  max-width: var(--shell-max);
  height: 100dvh;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--bg);
`;

// Leaves room below the content for the docked player and speaker bars.
export const Scroll = styled.main<{ $docked: boolean }>`
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  animation: ${pageIn} var(--dur-base) var(--ease);
  padding-bottom: ${({ $docked }) =>
    $docked ? 'calc(158px + max(0px, env(safe-area-inset-bottom) - 16px))' : '32px'};
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }
`;

// A page that scrolls itself, above the docked bars (128px, see .minibar and .spkbar).
export const Main = styled.main<{ $docked: boolean }>`
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  animation: ${pageIn} var(--dur-base) var(--ease);
  padding-bottom: ${({ $docked }) => ($docked ? 'calc(128px + max(0px, env(safe-area-inset-bottom) - 16px))' : '0')};
`;
