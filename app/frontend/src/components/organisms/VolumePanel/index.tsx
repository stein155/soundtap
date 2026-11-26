// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Button } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { SheetModal } from '../../molecules/SheetModal';
import { CardVolumes } from '../SpeakerCard/CardVolumes';
import { useI18n } from '../../../lib/i18n';
import type { Room } from '../../../hooks/useRooms';
import type { useRoomVolume } from '../../../hooks/useRoomVolume';

interface Props {
  readonly open: boolean;
  readonly onClose: () => void;
  // The name above the sliders: the speaker's, or its group's.
  readonly name: string;
  // The speaker first, then the ones playing along with it.
  readonly group: Room[];
  readonly volume: ReturnType<typeof useRoomVolume>;
  readonly onMute: (room: Room) => void;
}

// Volume without leaving the page, as Stockholm's panel behind the speaker bar's volume icon:
// a sheet from the bottom with its grip, the name and an X, then the sliders.
export function VolumePanel({ open, onClose, name, group, volume, onMute }: Props): ReactElement {
  const { t } = useI18n();
  return (
    <SheetModal open={open} onClose={onClose} label={`${t('volume')} ${name}`} className="volpanel">
      {(close) => (
        <>
          <span className="ract-grab" aria-hidden="true" />
          <div className="volpanel-top">
            <h2>{name}</h2>
            <Button className="top-btn" aria-label={t('close')} onPress={close}>
              <Icon.close />
            </Button>
          </div>
          <CardVolumes group={group} volume={volume} onMute={onMute} />
        </>
      )}
    </SheetModal>
  );
}
