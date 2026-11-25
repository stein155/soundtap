// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// The speaker at the top of its page, as the installer's model head: its drawing on the grey
// disc, its name large, model and version under it, a pencil that says the row renames it.
export const Hero = styled.button`
  display: flex;
  align-items: center;
  gap: 14px;
  width: calc(100% + 2 * var(--page-gutter, 0px));
  margin: 0 calc(-1 * var(--page-gutter, 0px));
  padding: 4px var(--page-gutter, 16px) 20px;
  background: var(--surface);
  text-align: left;
`;

export const Disc = styled.span`
  width: 72px;
  height: 72px;
  padding: 9px;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--surface-2);

  svg {
    display: block;
    width: 100%;
    height: 100%;
  }

  /* A near-black speaker drawing needs lifting on the dark theme. */
  [data-theme='dark'] & svg {
    filter: brightness(1.8);
  }
`;

export const Text = styled.span`
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
`;

export const Name = styled.span`
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 22px;
  font-weight: 600;
  letter-spacing: var(--tracking-title);
  color: var(--ink);
`;

// The model, then the versions: each on a line of its own, never broken inside.
export const Meta = styled.span`
  font-size: 12px;
  line-height: 1.45;
  color: var(--ink-3);

  span {
    display: block;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
`;

export const Pencil = styled.span`
  width: 44px;
  height: 44px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  color: var(--ink-2);
`;
