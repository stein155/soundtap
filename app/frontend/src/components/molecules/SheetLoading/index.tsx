// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { keyframes } from 'styled-components';
import { Spinner } from '../../atoms/Spinner';
import { reducedMotion } from '../../../theme/keyframes';
import { useI18n } from '../../../lib/i18n';
import type { ReactElement } from 'react';

const hidden = {
  position: 'absolute',
  width: 1,
  height: 1,
  overflow: 'hidden',
  clip: 'rect(0 0 0 0)',
} as const;

const appear = keyframes`
  from { opacity: 0; }
`;

// A fast answer never shows the ring: it waits 250 ms, then fades in as Stockholm's loader.
const Late = styled.span`
  display: inline-block;
  animation: ${appear} 0.3s var(--ease) 250ms backwards;
  ${reducedMotion}
`;

const centred = { padding: '60px 20px', textAlign: 'center' } as const;

// A sheet's list while it loads. The spinner is decoration; a screen reader hears
// "Loading…".
export function SheetLoading(): ReactElement {
  const { t } = useI18n();
  return (
    <div style={centred}>
      <span role="status">
        <Late>
          <Spinner big />
        </Late>
        <span style={hidden}>{t('loading')}</span>
      </span>
    </div>
  );
}
