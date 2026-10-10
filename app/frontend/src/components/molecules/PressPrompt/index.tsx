// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState } from 'react';
import styled from 'styled-components';
import { onPressWait } from '../../../lib/base';
import { useI18n } from '../../../lib/i18n';
import { Bubble, Text } from '../Toast/styled';

// Pinned to the window rather than the TopLayer: it has to show on the setup page too.
const Pinned = styled(Bubble)`
  position: fixed;
  z-index: 90;
`;

// While a request waits for a key held on the speaker, say so.
export function PressPrompt() {
  const { t } = useI18n();
  const [waiting, setWaiting] = useState(false);
  useEffect(() => onPressWait(setWaiting), []);
  if (!waiting) return null;
  return (
    <Pinned role="status">
      <Text>{t('pressToConfirm')}</Text>
    </Pinned>
  );
}
