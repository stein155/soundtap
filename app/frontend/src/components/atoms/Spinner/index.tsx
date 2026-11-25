// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Ring } from './styled';
import type { ReactElement } from 'react';

// An indeterminate progress ring. `small` fits a preset tile's status line, `big` stands in
// for a whole list, as Stockholm's loader does.
export function Spinner({ small, big }: { small?: boolean; big?: boolean }): ReactElement {
  return <Ring $small={small} $big={big} aria-hidden="true" />;
}
