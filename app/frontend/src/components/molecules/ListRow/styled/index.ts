// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// The design's browse row: white across the page, a rounded square picture, the name in
// the weight of a title and a faint line between rows. The row is the button. Hover is
// for a mouse only, since on touch it sticks after the tap. A track's row is lower, as its
// cover is, and a folder's lower still: no picture, a chevron on the right.
const rowHeight = ({
  $small,
  $folder,
}: {
  $small?: boolean | undefined;
  $folder?: boolean | undefined;
}): number => {
  if ($folder === true) return 52;
  return $small === true ? 60 : 64;
};

export const Row = styled.button<{ $small?: boolean | undefined; $folder?: boolean | undefined }>`
  display: flex;
  align-items: center;
  gap: 14px;
  min-height: ${rowHeight}px;
  padding: ${({ $folder }) => ($folder === true ? 0 : 8)}px var(--page-gutter, 16px);
  margin-inline: calc(-1 * var(--page-gutter, 0px));
  width: calc(100% + 2 * var(--page-gutter, 0px));
  background: var(--surface);
  color: var(--ink);
  text-align: left;

  & + & {
    border-top: 1px solid var(--border);
  }

  @media (hover: hover) {
    transition: background var(--dur-fast) var(--ease);

    &:hover {
      background: var(--surface-2);
    }
  }
`;

// A track's cover is a size smaller than a station's logo.
export const Art = styled.span<{ $small?: boolean | undefined }>`
  position: relative;
  width: ${({ $small }) => ($small === true ? 44 : 48)}px;
  height: ${({ $small }) => ($small === true ? 44 : 48)}px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  overflow: hidden;
  border-radius: ${({ $small }) => ($small === true ? 'var(--radius-sm)' : 'var(--radius)')};
  background: var(--surface);
  color: var(--ink);
`;

export const Text = styled.span`
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
`;

const line = `
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
`;

// A folder's name is plain, a station's or a track's a title.
export const Title = styled.span<{ $folder?: boolean | undefined }>`
  ${line}
  font-size: 15px;
  font-weight: ${({ $folder }) => ($folder === true ? 400 : 600)};
  color: var(--ink);
`;

export const Chevron = styled.span`
  display: grid;
  flex-shrink: 0;
  color: var(--ink-3);
`;

export const Subtitle = styled.span`
  ${line}
  font-size: 12px;
  font-weight: 400;
  color: var(--ink-3);
`;
