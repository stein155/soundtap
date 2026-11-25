// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { css } from 'styled-components';
import { Row as CheckRow } from '../../../../molecules/CheckRow/styled';

// docked: a column that fills the page down to Connect at its bottom (the first-run page).
export const Form = styled.form<{ $docked?: boolean | undefined }>`
  display: ${({ $docked }) => ($docked === true ? 'flex' : 'grid')};
  flex: 1 0 auto;
  flex-direction: column;
  gap: 16px;

  /* As the design's join sheet: "show password" right under its field, Connect 14 px
     under that. */
  & > ${CheckRow} {
    margin-top: -16px;
  }

  & > button[type='submit'] {
    margin-top: -2px;
  }
`;

// Refresh sits under the network list at the right, as in Stockholm.
export const Refresh = styled.div`
  justify-self: end;
`;

export const Searching = styled.p`
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0;
  padding: 15px 16px;
  color: var(--ink-2);
`;

export const NoNetworks = styled.div`
  display: grid;
  gap: 6px;
  padding: 4px;
`;

export const NoNetworksTitle = styled.h3`
  margin: 0;
  font-size: 16px;
  font-weight: 600;
`;

// One line of the Wi-Fi cards: a glyph, the network and what goes with it on the right.
const row = css`
  min-height: 52px;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 8px var(--page-gutter, 16px);
  font-size: 15px;
  color: var(--ink);
`;

export const Row = styled.div`
  ${row}

  /* Forget, as a small underlined word in the middle of the row. */
  & > button {
    align-self: center;
    font-size: 13px;
    font-weight: 600;
  }
`;

// A network to pick: the whole row is the button.
export const PickRow = styled.button`
  ${row}
  width: 100%;
  text-align: left;

  &:disabled {
    opacity: 0.45;
  }

  @media (hover: hover) {
    &:hover:not(:disabled) {
      background: var(--surface-2);
    }
  }
`;

export const Glyph = styled.span`
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  color: var(--ink-3);

  svg {
    width: 22px;
    height: 22px;
    stroke-width: 1.8;
  }
`;

export const Text = styled.span`
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
`;

// The network in use, in the weight the design gives it, and its signal and address.
export const Current = styled.span`
  display: block;
  font-weight: 600;
  color: var(--ink);
`;

export const Detail = styled.span`
  display: block;
  margin-top: 2px;
  font-size: 12px;
  color: var(--ink-3);
`;

// The lock and the chevron after a network's name.
export const Trail = styled.span`
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 14px;
  color: var(--ink-3);
`;

// The join sheet's own layout: the form under its header.
export const SheetBody = styled.div`
  --page-gutter: 16px;
  padding: 4px 16px calc(28px + env(safe-area-inset-bottom));
  display: grid;
  gap: 12px;
`;

// A password field with its eye inside it, on the right (the first-run page).
export const Reveal = styled.div`
  position: relative;

  input {
    padding-right: 52px;
  }

  button {
    position: absolute;
    top: 50%;
    right: 2px;
    width: 44px;
    height: 44px;
    display: grid;
    place-items: center;
    transform: translateY(-50%);
    color: var(--ink-3);
  }

  button[aria-pressed='true'] {
    color: var(--ink);
  }
`;

// Connect at the bottom of the screen, on the page's own grey, clear of the home indicator;
// a long list scrolls under it. The negative margins undo the page's gutter and its bottom
// padding.
export const Dock = styled.div`
  position: sticky;
  bottom: 0;
  z-index: 1;
  display: grid;
  gap: 12px;
  margin: auto calc(-1 * var(--page-gutter, 16px)) calc(-28px - env(safe-area-inset-bottom));
  padding: 12px var(--page-gutter, 16px) calc(28px + env(safe-area-inset-bottom));
  background: var(--bg);
`;
