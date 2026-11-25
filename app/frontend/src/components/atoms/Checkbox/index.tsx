// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Icon } from '../Icon';
import { Box } from './styled';
import type { ReactElement } from 'react';

// The visual checkbox square. It is decoration: the row around it carries
// role="checkbox" and aria-checked, so the whole row is the target. `strong` draws the
// empty box darker, for a list where it is the only control.
export function Checkbox({
  checked,
  strong,
}: {
  checked: boolean;
  strong?: boolean | undefined;
}): ReactElement {
  return (
    <Box $checked={checked} $strong={strong} aria-hidden="true">
      {checked && <Icon.check width="14" height="14" />}
    </Box>
  );
}
