// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { desktop } from '../../../../theme/media';
import { hitArea } from '../../../../theme/hitArea';
import { Bar } from '../../SaveBar/styled';

// The bar every full-screen page shares (settings, speakers, search): back chevron on
// the left when there is a level to go up to, a small letter-spaced title in the middle
// (with a quiet line under it), and a plain X on the right.
export const Header = styled.header<{ $wide?: boolean | undefined }>`
  position: relative;
  flex: none;
  display: grid;
  grid-template-columns: ${({ $wide }) => ($wide === true ? '88px 1fr 88px' : '44px 1fr 44px')};
  align-items: center;
  min-height: calc(56px + env(safe-area-inset-top));
  padding: env(safe-area-inset-top) 6px 0;
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
  width: 44px;
  height: 44px;
  display: grid;
  place-items: center;
  color: var(--ink);
`;

// The right-hand buttons: the page's own, then the X.
export const Right = styled.div`
  grid-row: 1;
  grid-column: 3;
  justify-self: end;
  display: flex;
  align-items: center;

  & > button:disabled {
    opacity: 0.4;
  }
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
  font-size: 15px;
  font-weight: 700;
  letter-spacing: 0.5px;
`;

// The page's name for a screen reader where the page draws its own title in a control
// (a speaker's name on its picture, which is a button).
export const HiddenHeadline = styled.h2`
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
`;

// Above a page's own title (the hero below): where the page sits, small and grey.
export const Kicker = styled.p`
  max-width: 100%;
  margin: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.5px;
  text-transform: uppercase;
  color: var(--ink-3);
`;

// A page's own title, large on white at the top of the body, with what the page is for
// under it.
export const Hero = styled.div`
  margin: 0 calc(-1 * var(--page-gutter));
  padding: 6px var(--page-gutter) 22px;
  background: var(--surface);

  h1 {
    margin: 0;
    font-size: 24px;
    font-weight: 600;
    letter-spacing: var(--tracking-title);
    overflow-wrap: anywhere;
  }

  p {
    margin: 8px 0 0;
    font-size: 14px;
    line-height: 1.5;
    color: var(--ink-3);
  }
`;

export const Body = styled.div`
  --page-gutter: 16px;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 0 var(--page-gutter) calc(32px + env(safe-area-inset-bottom));
  scrollbar-width: thin;
  scrollbar-color: rgba(31, 24, 20, 0.12) transparent;

  /* A page with a Save bar is a column down to the bottom of the screen, through every box
     that holds the bar: on a short page the bar sits at the bottom (its margin-top: auto),
     on a long one it sticks there while the page scrolls under it. */
  &:has(${Bar}),
  & :has(${Bar}):not([hidden]) {
    display: flex;
    flex-direction: column;
  }

  & :has(${Bar}):not([hidden]) {
    flex: 1 0 auto;
  }

  /* What comes before the bar keeps its distance from it once the page is long. */
  & :has(+ ${Bar}) {
    margin-bottom: 18px;
  }

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
