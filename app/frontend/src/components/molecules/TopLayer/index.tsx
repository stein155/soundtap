// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { createPortal } from 'react-dom';

// Notices (the connection banner, the toast) above everything, sheets included. A sheet
// hides the rest of the page from screen readers and keeps clicks outside it from
// reaching it; a layer marked as the top layer is the exception, so a message about an
// action taken in a sheet can still be read and undone from there.
export function TopLayer({ children }: { children: ReactNode }): ReactElement {
  return createPortal(
    <div className="top-layer" data-react-aria-top-layer="true">
      <div className="top-layer-frame">{children}</div>
    </div>,
    document.body
  );
}
