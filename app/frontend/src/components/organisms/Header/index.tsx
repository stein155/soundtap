// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Icon } from '../../atoms/Icon';
import { useI18n } from '../../../lib/i18n';
import { Bar, Emoji, Headings, SettingsButton, Subtitle, Title } from './styled';

interface Props {
  emoji: string;
  title: string;
  subtitle: string;
  // The colour the header glows in: what is playing, or the time of day.
  tint: string;
  // The emoji waves a greeting (the first-run page).
  waving?: boolean;
  onSettings?: () => void;
}

// The greeting at the top of the screen, with the settings button.
export function Header({ emoji, title, subtitle, tint, waving, onSettings }: Props) {
  const { t } = useI18n();
  return (
    <Bar $tint={tint}>
      <Headings>
        <Title>
          <Emoji $waving={waving} aria-hidden="true">
            {emoji}
          </Emoji>
          {title}
        </Title>
        <Subtitle>{subtitle}</Subtitle>
      </Headings>
      {onSettings && (
        <SettingsButton onClick={onSettings} aria-label={t('settings')}>
          <Icon.settings width="20" height="20" />
        </SettingsButton>
      )}
    </Bar>
  );
}
