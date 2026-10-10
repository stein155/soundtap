// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Frame, Main, Scroll } from './styled';

// The app frame: a header, a scrolling middle, what is docked at the bottom, and what is
// layered on top of it (banners, sheets). A page with a bar of its own takes the place of the
// header and the middle.
export function AppShell({
  header,
  footer,
  overlays,
  page,
  children,
}: {
  header?: ReactNode;
  footer?: ReactNode;
  overlays?: ReactNode;
  page?: ReactNode;
  children: ReactNode;
}) {
  return (
    <Frame>
      {page ? (
        <Main $docked={!!footer}>{page}</Main>
      ) : (
        <>
          {header}
          <Scroll $docked={!!footer}>{children}</Scroll>
        </>
      )}
      {footer}
      {overlays}
    </Frame>
  );
}
