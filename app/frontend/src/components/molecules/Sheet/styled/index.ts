// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { desktop } from '../../../../theme/media';
import { hitArea } from '../../../../theme/hitArea';

// The bar every full-screen page shares (settings, speakers, search): back chevron on
// the left when there is a level to go up to, a small letter-spaced title in the middle
// (with a quiet line under it), and a plain X on the right.
export const Header = styled.header`
  position: relative;
  flex: none;
  display: grid;
  grid-template-columns: 52px 1fr 52px;
  align-items: center;
  min-height: calc(60px + env(safe-area-inset-top));
  padding: env(safe-area-inset-top) 4px 0;
  background: var(--surface);
`;

export const HeaderField = styled.div`
  grid-row: 1;
  grid-column: 1 / span 2;
  min-width: 0;
  padding: 0 4px 0 16px;
`;

export const IconButton = styled.button<{ $side: 'left' | 'right' }>`
  ${hitArea()}
  grid-row: 1;
  grid-column: ${({ $side }) => ($side === 'left' ? 1 : 3)};
  justify-self: ${({ $side }) => ($side === 'left' ? 'start' : 'end')};
  width: 48px;
  height: 48px;
  display: grid;
  place-items: center;
  color: var(--ink);
`;

export const Titles = styled.div`
  grid-column: 2;
  grid-row: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  text-align: center;
`;

export const Headline = styled.h2`
  max-width: 100%;
  margin: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 1.5px;
`;

export const Sub = styled.span`
  max-width: 100%;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.04em;
  color: var(--ink-3);
`;

export const Body = styled.div<{ $tight?: boolean }>`
  --page-gutter: 20px;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: ${({ $tight }) => ($tight ? '8px' : '16px')} var(--page-gutter)
    calc(32px + env(safe-area-inset-bottom));
  scrollbar-width: thin;
  scrollbar-color: rgba(31, 24, 20, 0.12) transparent;

  /* The page body of Stockholm's wide layout: a readable column in the middle. */
  @media ${desktop} {
    padding-inline: max(var(--page-gutter), calc((100% - 40em) / 2));
  }
`;

// A page: the header stays, the body scrolls.
export const Page = styled.section`
  &:focus {
    outline: none;
  }

  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--bg);
`;
