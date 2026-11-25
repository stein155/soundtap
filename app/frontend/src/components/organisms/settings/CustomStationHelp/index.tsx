// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Icon, type IconName } from '../../../atoms/Icon';
import { useI18n, type I18nKey } from '../../../../lib/i18n';
import { Details, Reason, ReasonText, Reasons, Summary } from './styled';

const REASONS: { icon: IconName; title: I18nKey; why: I18nKey[] }[] = [
  { icon: 'linkOff', title: 'customHelpInvalid', why: ['customHelpInvalidWhy'] },
  {
    icon: 'lock',
    title: 'customHelpRestricted',
    why: ['customHelpRestrictedLogin', 'customHelpRestrictedPremium'],
  },
  { icon: 'note', title: 'customHelpIncompatible', why: ['customHelpIncompatibleWhy'] },
  { icon: 'globe', title: 'customHelpGeo', why: ['customHelpGeoWhy'] },
];

// Why a stream from a link may not play, folded away under the form until asked for: one
// row per reason, each with its own icon.
export function CustomStationHelp(): ReactElement {
  const { t } = useI18n();
  return (
    <Details>
      <Summary>
        <Icon.help width="22" height="22" aria-hidden="true" />
        <span>{t('customHelpWhy')}</span>
        <Icon.chevronDown width="18" height="18" aria-hidden="true" />
      </Summary>
      <Reasons>
        {REASONS.map(({ icon, title, why }) => {
          const Glyph = Icon[icon];
          return (
            <Reason key={title}>
              <Glyph width="22" height="22" aria-hidden="true" />
              <ReasonText>
                <strong>{t(title)}</strong>
                <span>{why.map((key) => t(key)).join(' ')}</span>
              </ReasonText>
            </Reason>
          );
        })}
      </Reasons>
    </Details>
  );
}
