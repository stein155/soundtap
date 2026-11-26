// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactNode, type ReactElement } from 'react';
import { Button, Heading } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { Art } from '../../molecules/Art';
import { SheetModal } from '../../molecules/SheetModal';
import { useI18n } from '../../../lib/i18n';
import { logoURL } from '../../../lib/artwork';
import { timeAgo, type RecentItem } from '../../../lib/recent';

const SERVICE: Record<string, string> = { TUNEIN: 'TuneIn', SPOTIFY: 'Spotify' };

// The quiet line under a recent entry: when it last played and on which service.
export function recentLine(item: RecentItem, lang: string): string {
  return [timeAgo(item.stoppedAt, lang), SERVICE[item.source]].filter(Boolean).join(' · ');
}

export const serviceName = (item: RecentItem): string => SERVICE[item.source] ?? '';

export interface RecentActionsProps {
  // The entry the sheet is for; null closes it.
  readonly item: RecentItem | null;
  readonly speaker: string;
  readonly onClose: () => void;
  readonly onPlay: (item: RecentItem) => void;
  // Holds the entry on its way to a preset; left out while there is no room to store it on.
  readonly onHold?: ((item: RecentItem) => void) | undefined;
  readonly onRemove: (item: RecentItem) => void;
}

function Action({
  icon,
  danger,
  more,
  onPress,
  children,
}: {
  readonly icon: ReactNode;
  readonly danger?: boolean;
  readonly more?: boolean;
  readonly onPress: () => void;
  readonly children: string;
}) {
  return (
    <Button className={`ract-row${danger === true ? ' danger' : ''}`} onPress={onPress}>
      {icon}
      <span>{children}</span>
      {more === true && <Icon.chevron className="ract-more" aria-hidden="true" />}
    </Button>
  );
}

// What can be done with one recently played entry, in a sheet from the bottom: play it
// again, put it on a preset, or take it off the list.
export function RecentActions({
  item,
  speaker,
  onClose,
  onPlay,
  onHold,
  onRemove,
}: RecentActionsProps): ReactElement {
  const { t, lang } = useI18n();
  // The entry stays drawn while the sheet slides away.
  const [shown, setShown] = useState(item);
  if (item && item !== shown) setShown(item);
  return (
    <SheetModal open={!!item} onClose={onClose} label={shown?.name ?? ''} className="ract">
      {(close) =>
        shown && (
          <>
            <span className="ract-grab" aria-hidden="true" />
            <div className="ract-head">
              <Art
                src={logoURL({ logo: shown.art, tuneInId: shown.tuneInId }, speaker)}
                name={shown.name}
                className="ract-art"
              />
              <span className="ract-text">
                <Heading slot="title">{shown.name}</Heading>
                <small>{recentLine(shown, lang)}</small>
              </span>
              <Button className="top-btn" aria-label={t('close')} onPress={close}>
                <Icon.close />
              </Button>
            </div>
            <div className="ract-list">
              <Action
                icon={<Icon.play />}
                onPress={() => {
                  close();
                  onPlay(shown);
                }}
              >
                {t('play')}
              </Action>
              {onHold && (
                <Action
                  icon={<Icon.presets />}
                  more
                  onPress={() => {
                    close();
                    onHold(shown);
                  }}
                >
                  {t('presetPut')}
                </Action>
              )}
              <Action
                icon={<Icon.trash />}
                danger
                onPress={() => {
                  close();
                  onRemove(shown);
                }}
              >
                {t('recentRemove')}
              </Action>
            </div>
          </>
        )
      }
    </SheetModal>
  );
}
