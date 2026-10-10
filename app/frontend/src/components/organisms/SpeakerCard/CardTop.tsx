// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Button, Heading, ToggleButton } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { useI18n } from '../../../lib/i18n';

interface Props {
  title: string;
  // Only the selected page names the dialog; a peeking neighbour shows plain text.
  live: boolean;
  poweredOn: boolean;
  onClose: () => void;
  onPower: (on: boolean) => void;
}

// The card's top bar: close, the speaker's name, and its power switch.
export function CardTop({ title, live, poweredOn, onClose, onPower }: Props) {
  const { t } = useI18n();
  return (
    <div className="card-top">
      <Button className="top-btn" aria-label={t('close')} onPress={onClose}>
        <Icon.chevronDown />
      </Button>
      {live ? (
        <Heading slot="title" className="card-room">
          {title}
        </Heading>
      ) : (
        <p className="card-room">{title}</p>
      )}
      <ToggleButton
        className="top-btn"
        aria-label={t('power')}
        isSelected={poweredOn}
        onChange={onPower}
      >
        <Icon.power />
      </ToggleButton>
    </div>
  );
}
