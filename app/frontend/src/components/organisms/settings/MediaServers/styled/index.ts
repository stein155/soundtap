// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Intro = styled.p`
  margin: 0;
  padding: 16px 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--ink-3);
`;

export const SearchAgain = styled.div`
  padding-top: 16px;

  & > button {
    margin-top: 0;
  }
`;

// Where in the server the page is: the way down to it, small, over the folder's name.
export const FolderHead = styled.div`
  margin: 0 calc(-1 * var(--page-gutter, 0px));
  padding: 6px var(--page-gutter, 16px) 18px;
  background: var(--surface);
`;

export const Crumbs = styled.p`
  margin: 0 0 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--ink-3);
`;

export const FolderTitle = styled.h1`
  margin: 0;
  font-size: 24px;
  font-weight: 600;
  letter-spacing: var(--tracking-title);
`;

export const Track = styled.button<{ $playing: boolean }>`
  width: 100%;
  min-height: 52px;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 0 var(--page-gutter, 16px);
  text-align: left;
  font-size: 15px;
  font-weight: ${({ $playing }) => ($playing ? 600 : 400)};
  color: var(--ink);

  &:disabled {
    cursor: default;
  }

  &:active:not(:disabled) {
    background: var(--surface-2);
  }
`;

// A track's play sign is drawn in outline, as the design's; the playing one has its bars.
export const TrackGlyph = styled.span`
  width: 22px;
  height: 22px;
  flex: none;
  display: grid;
  place-items: center;

  svg path {
    fill: none;
    stroke: currentColor;
    stroke-width: 2;
    stroke-linejoin: round;
  }
`;
