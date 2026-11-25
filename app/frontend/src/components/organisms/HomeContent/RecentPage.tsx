// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Sheet } from '../../molecules/Sheet';
import { TopBar } from '../TopBar';
import { useEscape } from '../../../hooks/useEscape';
import { useI18n } from '../../../lib/i18n';
import type { RecentItem } from '../../../lib/recent';
import { RecentList } from './RecentList';

interface Props {
  recent: RecentItem[];
  speaker: string;
  onBack: () => void;
  onPresets: () => void;
  onPlayRecent: (item: RecentItem) => void;
  onRemoveRecent: (item: RecentItem) => void;
  onHoldRecent?: ((item: RecentItem) => void) | undefined;
}

// See All under the start page's Recently Played: a main page, with the bars staying.
export function RecentPage({ onBack, onPresets, ...list }: Props): ReactElement {
  const { t } = useI18n();
  useEscape(true, onBack);
  return (
    <Sheet
      label={t('recentlyPlayed')}
      bar={
        <TopBar onMenu={onBack} onBack={onBack} onPresets={onPresets} title={t('recentlyPlayed')} />
      }
    >
      <RecentList {...list} />
    </Sheet>
  );
}
