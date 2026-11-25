// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Icon } from '../../atoms/Icon';
import { EqualizerBars } from '../../atoms/EqualizerBars';
import { Spinning } from '../../atoms/Spinning';
import { ModelArt } from '../ModelArt';
import { TransportButton } from '../TransportButton';
import { useI18n } from '../../../lib/i18n';
import type { PlaybackView } from '../../../lib/playback';
import {
  Card,
  Check,
  Drawing,
  Main,
  Name,
  RetryButton,
  Row,
  Status,
  StatusText,
  Text,
} from './styled';
import type { ReactElement } from 'react';

interface Props {
  // "Kitchen", or "Kitchen + Office" for a group.
  name: string;
  // The lead speaker's model, for its drawing when nothing with artwork plays.
  model?: string | undefined;
  playback: PlaybackView;
  on: boolean;
  // The room the player at the bottom controls.
  active: boolean;
  onOpen: () => void;
  onTransport: () => void;
}

// A room in the speakers list: the speaker's drawing, name, what it plays, and its own transport button.
// Tapping it makes it the room the player controls.
export function RoomCard({
  name,
  model,
  playback,
  on,
  active,
  onOpen,
  onTransport,
}: Props): ReactElement {
  return (
    <Card $active={active}>
      <Row>
        <Main onClick={onOpen} aria-current={active || undefined}>
          <Drawing>
            <ModelArt model={model} />
          </Drawing>
          <Text>
            <Name>{name}</Name>
            <Status $on={on}>
              {playback.live && <EqualizerBars />}
              <StatusText>{playback.status}</StatusText>
            </Status>
          </Text>
          {active && (
            <Check aria-hidden="true">
              <Icon.check width="18" height="18" />
            </Check>
          )}
        </Main>
        <TransportButton
          action={playback.transport}
          onClick={onTransport}
          variant="card"
          on={on}
          target={name}
        />
      </Row>
    </Card>
  );
}

// A speaker discovery knows about but cannot reach right now.
export function OfflineRoomCard({
  name,
  model,
  connecting,
  onRetry,
}: {
  name: string;
  model?: string | undefined;
  connecting: boolean;
  onRetry: () => void;
}): ReactElement {
  const { t } = useI18n();
  return (
    <Card $offline>
      <Row>
        <Main as="span">
          <Drawing>
            <ModelArt model={model} />
          </Drawing>
          <Text>
            <Name>{name}</Name>
            <Status>
              <StatusText>{connecting ? t('connecting') : t('offline')}</StatusText>
            </Status>
          </Text>
        </Main>
        <RetryButton onClick={onRetry} disabled={connecting}>
          <Spinning active={connecting}>
            <Icon.refresh width="15" height="15" />
          </Spinning>
          {t('reconnect')}
        </RetryButton>
      </Row>
    </Card>
  );
}
