// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Button } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { useI18n } from '../../../lib/i18n';

interface Props {
  onMenu: () => void;
  onPresets: () => void;
  // What the page shows; left out, the start page.
  icon?: ReactNode;
  title?: string;
  // One level up, in place of the menu.
  onBack?: () => void;
  // A search field takes over the whole bar; its X leaves the search.
  field?: ReactNode;
  onCloseField?: () => void;
}

// The bar above every main page, as in Stockholm: the menu, what the page shows, the presets.
export function TopBar({ onMenu, onPresets, icon, title, onBack, field, onCloseField }: Props) {
  const { t } = useI18n();
  if (field) {
    return (
      <header className="top">
        <div className="top-field">{field}</div>
        <Button className="top-btn" aria-label={t('close')} onPress={onCloseField}>
          <Icon.close />
        </Button>
      </header>
    );
  }
  return (
    <header className="top">
      {onBack ? (
        <Button className="top-btn" aria-label={t('back')} onPress={onBack}>
          <Icon.back />
        </Button>
      ) : (
        <Button className="top-btn" aria-label={t('menu')} onPress={onMenu}>
          <Icon.menu />
        </Button>
      )}
      <h1 className="brand">
        {icon ?? <Icon.clock />}
        <span>{title ?? t('justForYou')}</span>
      </h1>
      <Button className="top-btn" aria-label={t('yourStations')} onPress={onPresets}>
        <Icon.presets />
      </Button>
    </header>
  );
}
