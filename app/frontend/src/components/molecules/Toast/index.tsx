// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Icon } from '../../atoms/Icon';
import { useI18n } from '../../../lib/i18n';
import { Bubble, Close, Text, UndoButton } from './styled';
import type { ReactElement } from 'react';

// A short message floating above the player: what just failed, and a way to dismiss it.
export function Toast({
  message,
  onDismiss,
  onUndo,
  undoLabel,
}: {
  readonly message: string;
  readonly onDismiss: () => void;
  readonly onUndo?: (() => void) | undefined;
  // The action's name, when it is not "Undo".
  readonly undoLabel?: string;
}): ReactElement {
  const { t } = useI18n();
  return (
    <Bubble role="alert">
      <Text>{message}</Text>
      {onUndo && (
        <UndoButton
          onClick={() => {
            onDismiss();
            onUndo();
          }}
        >
          {undoLabel ?? t('undo')}
        </UndoButton>
      )}
      <Close onClick={onDismiss} aria-label={t('close')}>
        <Icon.close width="16" height="16" />
      </Close>
    </Bubble>
  );
}
