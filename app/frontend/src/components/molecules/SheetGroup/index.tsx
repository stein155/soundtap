// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Eyebrow } from '../../atoms/Eyebrow';
import { Group, Head, Rows } from './styled';

// A group of rows in a sheet, with a title when it has one.
export function SheetGroup({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <Group>
      {title && (
        <Head>
          <Eyebrow>{title}</Eyebrow>
        </Head>
      )}
      <Rows>{children}</Rows>
    </Group>
  );
}
