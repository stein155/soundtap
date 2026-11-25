// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Button } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { useI18n } from '../../../lib/i18n';

interface Props {
  onMenu: () => void;
  onPresets: () => void;
  // What the page shows; left out, the start page.
  icon?: ReactNode | undefined;
  title?: string | undefined;
  // One level up, in place of the menu.
  onBack?: (() => void) | undefined;
  // A search field in a strip under the bar; the back chevron then leaves the search.
  field?: ReactNode | undefined;
  onCloseField?: () => void;
}

// The bar above every main page, as in Stockholm: the menu, what the page shows, the presets.
export function TopBar({
  onMenu,
  onPresets,
  icon,
  title,
  onBack,
  field,
  onCloseField,
}: Props): ReactElement {
  const { t } = useI18n();
  // Every field passed is an element; nothing (undefined, null, false) means no field.
  const hasField = field !== undefined && field !== null && field !== false;
  const back = hasField ? onCloseField : onBack;
  const bar = (
    <header className="top">
      {back ? (
        <Button className="top-btn" aria-label={t('back')} onPress={back}>
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
  if (!hasField) return bar;
  return (
    <>
      {bar}
      <div className="top-field">{field}</div>
    </>
  );
}
