// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// Sticks to the bottom of the sheet's scrolling body: a white bar with a hairline above,
// clear of the phone's home indicator. The negative margins undo the body's own gutter and
// bottom padding; the sheet's body is a column down to it, so margin-top: auto puts it at
// the bottom of a short page (see Sheet's Body).
export const Bar = styled.div`
  position: sticky;
  bottom: calc(-32px - env(safe-area-inset-bottom));
  z-index: 2;
  margin: auto calc(-1 * var(--page-gutter, 16px)) calc(-32px - env(safe-area-inset-bottom));
  padding: 12px var(--page-gutter, 16px) calc(28px + env(safe-area-inset-bottom));
  background: var(--surface);
  box-shadow: 0 -1px 0 var(--border);

  /* The bar's own padding is the space above the button. */
  & > button {
    margin-top: 0;
  }
`;
