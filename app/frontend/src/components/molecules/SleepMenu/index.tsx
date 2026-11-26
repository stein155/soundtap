// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Button, Menu, MenuItem, MenuTrigger, Popover } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { useMinutesLeft } from '../../../hooks/useMinutesLeft';
import { useI18n } from '../../../lib/i18n';
import type { ReactElement } from 'react';

const CHOICES = [0, 15, 30, 60, 90];

interface Props {
  // The running timer's length and end (ms since epoch), or null when none runs.
  readonly timer: { minutes: number; endsAt: number } | null;
  readonly onChange: (minutes: number) => void;
  // Drawn on a dark picture (now playing) instead of a card.
  readonly light?: boolean | undefined;
}

// What the button says: on now playing a chip, "Sleep timer off" or the time left with a
// chevron to the menu above it; on a card a row with its value on the right.
function TriggerText({
  light = false,
  left,
}: {
  readonly light?: boolean | undefined;
  readonly left: number | null;
}) {
  const { t } = useI18n();
  if (light && left === null) return <span className="sl-l">{t('sleepTimerOff')}</span>;
  return (
    <>
      <span className="sl-l">{t('sleepTimer')}</span>
      <span className="sl-v">{left === null ? t('off') : t('sleepMinutes', { n: left })}</span>
      {light ? <Icon.chevronUp /> : <Icon.chevron />}
    </>
  );
}

// The sleep timer as a button: its label, how long is left, and a menu of lengths, the
// chosen one ticked.
export function SleepMenu({ timer, onChange, light = false }: Props): ReactElement {
  const { t } = useI18n();
  const left = useMinutesLeft(timer?.endsAt ?? null);
  const selected = String(timer?.minutes ?? 0);
  return (
    <MenuTrigger>
      <Button className={`sleeprow${light ? ' on-dark' : ''}`} aria-label={t('sleepTimer')}>
        <Icon.moon />
        <TriggerText light={light} left={timer ? left : null} />
      </Button>
      <Popover className="sl-pop" placement={light ? 'top start' : 'top end'}>
        <Menu
          className="sl-menu"
          aria-label={t('sleepTimer')}
          selectionMode="single"
          disallowEmptySelection
          selectedKeys={[selected]}
          onSelectionChange={(keys) => {
            if (keys === 'all') return; // single selection: never every item
            const value = [...keys].at(0);
            if (value !== undefined) onChange(Number(value));
          }}
        >
          {CHOICES.map((minutes) => (
            <MenuItem
              key={minutes}
              id={String(minutes)}
              className="sl-item"
              textValue={minutes === 0 ? t('off') : t('sleepMinutes', { n: minutes })}
            >
              {({ isSelected }) => (
                <>
                  {minutes === 0 ? t('off') : t('sleepMinutes', { n: minutes })}
                  {isSelected && <Icon.check width="16" height="16" aria-hidden="true" />}
                </>
              )}
            </MenuItem>
          ))}
        </Menu>
      </Popover>
    </MenuTrigger>
  );
}
