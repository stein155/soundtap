// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Note } from '../../../atoms/Note';
import { useI18n, type I18nKey } from '../../../../lib/i18n';
import { STREAM_FORMATS } from '../CustomStation';
import { Details, Reason, Reasons, Why } from './styled';

const REASONS: { title: I18nKey; why: I18nKey[] }[] = [
  { title: 'customHelpInvalid', why: ['customHelpInvalidWhy'] },
  {
    title: 'customHelpRestricted',
    why: ['customHelpRestrictedLogin', 'customHelpRestrictedPremium'],
  },
  { title: 'customHelpIncompatible', why: ['customHelpIncompatibleWhy'] },
  { title: 'customHelpGeo', why: ['customHelpGeoWhy'] },
];

// Stockholm's troubleshooting for a custom station, with SoundTap's own format list.
export function CustomStationHelp() {
  const { t } = useI18n();
  return (
    <>
      <Why>{t('customHelpWhy')}</Why>
      <Note>{t('customHelpCodec', { formats: STREAM_FORMATS })}</Note>
      <Note>{t('customHelpTest')}</Note>
      <Note>{t('customHelpOther')}</Note>
      <Reasons>
        {REASONS.map(({ title, why }) => (
          <Reason key={title}>
            {t(title)}
            <Details>
              {why.map((key) => (
                <li key={key}>{t(key)}</li>
              ))}
            </Details>
          </Reason>
        ))}
      </Reasons>
    </>
  );
}
