// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Button, Menu, MenuItem, MenuTrigger, Popover } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { useMinutesLeft } from '../../../hooks/useMinutesLeft';
import { useI18n } from '../../../lib/i18n';

const CHOICES = [0, 15, 30, 60, 90];

interface Props {
  // The running timer's length and end (ms since epoch), or null when none runs.
  timer: { minutes: number; endsAt: number } | null;
  onChange: (minutes: number) => void;
  // Drawn on a dark picture (now playing) instead of a card.
  light?: boolean;
}

// The sleep timer as a row: its label, how long is left, and a menu of lengths.
export function SleepMenu({ timer, onChange, light }: Props) {
  const { t } = useI18n();
  const left = useMinutesLeft(timer?.endsAt ?? null);
  const selected = String(timer?.minutes ?? 0);
  return (
    <MenuTrigger>
      <Button className={`sleeprow${light ? ' on-dark' : ''}`} aria-label={t('sleepTimer')}>
        <Icon.moon />
        <span className="sl-l">{t('sleepTimer')}</span>
        <span className="sl-v">{timer ? t('sleepMinutes', { n: left }) : t('off')}</span>
        <Icon.chevron />
      </Button>
      <Popover className="sl-pop" placement="top end">
        <Menu
          className="sl-menu"
          aria-label={t('sleepTimer')}
          selectionMode="single"
          disallowEmptySelection
          selectedKeys={[selected]}
          onSelectionChange={(keys) => {
            const value = [...keys].at(0);
            if (value !== undefined) onChange(Number(value));
          }}
        >
          {CHOICES.map((minutes) => (
            <MenuItem key={minutes} id={String(minutes)} className="sl-item">
              {minutes === 0 ? t('off') : t('sleepMinutes', { n: minutes })}
            </MenuItem>
          ))}
        </Menu>
      </Popover>
    </MenuTrigger>
  );
}
