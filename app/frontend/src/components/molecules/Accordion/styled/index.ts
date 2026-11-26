// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { Text as HintText } from '../../../atoms/Hint/styled';
import { Paragraph } from '../../../atoms/Note/styled';
import { Field } from '../../LabeledField/styled';
import { pushIn } from '../../../../theme/keyframes';

// A row's own page: its fields and cards straight on the sheet, like any other page.
// A note that opens the page says what it is for: it joins the page's title above it,
// on white under the headline. It slides in as it opens, as iOS pushes a page; less
// motion asked for ends that at once (theme/GlobalStyle.ts).
export const Body = styled.div`
  display: flex;
  flex-direction: column;
  gap: 12px;
  animation: ${pushIn} var(--dur-base) var(--ease-enter);

  /* A group under a grey bar follows straight on: the bar is the separation. */
  & > * + section:has(> h2:first-child),
  & > * + div:has(> section:first-child > h2:first-child) {
    margin-top: -12px;
  }

  /* After a card of its own (the alarm's switch, Bluetooth's, an update's), a block of
     text and buttons, or the page's intro, the bar stands off it. */
  & > section:not(:has(> h2)) + section:has(> h2:first-child),
  & > div:not(:has(section)) + section:has(> h2:first-child) {
    margin-top: 4px;
  }

  & > ${Paragraph}:first-child + section:has(> h2:first-child),
  & > ${Paragraph}:first-child + div:has(> section:first-child > h2:first-child) {
    margin-top: 0;
  }

  /* A card of its own after another group stands 16 px off it, as the design's. */
  && > section + section:not(:has(> h2)) {
    margin-top: 4px;
  }

  /* A note under a card keeps the page's own spacing, not a margin of its own on top. */
  & > ${HintText} {
    margin-block: 0;
  }

  /* A page that is one field (a name): the field stands off the title. */
  & > ${Field}:first-child {
    margin-top: 22px;
  }

  & > ${Paragraph}:first-child {
    margin: -22px calc(-1 * var(--page-gutter, 0px)) 4px;
    padding: 8px var(--page-gutter, 16px) 22px;
    background: var(--surface);
    font-size: 14px;
    color: var(--ink-3);
  }
`;
