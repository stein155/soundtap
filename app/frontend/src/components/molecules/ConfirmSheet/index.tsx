// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Heading } from 'react-aria-components';
import styled from 'styled-components';
import { Button } from '../../atoms/Button';
import { SheetModal } from '../SheetModal';
import { useI18n } from '../../../lib/i18n';
import { useState, type ReactElement, type ReactNode } from 'react';

const Body = styled.div`
  padding: 24px 20px 20px;
  display: grid;
  gap: 12px;
  text-align: center;

  h2 {
    margin: 0;
    font-size: 17px;
    font-weight: 700;
    color: var(--ink);
  }

  p {
    margin: 0;
    color: var(--ink-2);
    line-height: 1.45;
  }
`;

// The buttons under each other, the one that goes on top: a long label fits whole.
const Actions = styled.div`
  display: grid;
  gap: 10px;
  margin-top: 8px;

  & > button {
    width: 100%;
    margin: 0;
  }
`;

// A short question in a dialog in the middle of the screen before something that takes a
// while or erases something starts, as stock asks: what happens, then go or Cancel.
// Closing it any other way (Escape, back, a tap outside) is Cancel.
export function ConfirmSheet({
  open,
  title,
  body,
  confirmLabel,
  destructive = false,
  onConfirm,
  onClose,
}: {
  readonly open: boolean;
  readonly title: string;
  readonly body: ReactNode;
  readonly confirmLabel: ReactNode;
  // An action that erases something: its button in the danger colour.
  readonly destructive?: boolean;
  readonly onConfirm: () => void;
  readonly onClose: () => void;
}): ReactElement {
  const { t } = useI18n();
  return (
    <SheetModal open={open} onClose={onClose} label={title} variant="dialog">
      {(close) => (
        <Body>
          <Heading slot="title">{title}</Heading>
          <p>{body}</p>
          <Actions>
            <Button
              variant={destructive ? 'danger' : 'stockholm'}
              onClick={() => {
                close();
                onConfirm();
              }}
            >
              {confirmLabel}
            </Button>
            <Button variant="ghost" onClick={close}>
              {t('cancel')}
            </Button>
          </Actions>
        </Body>
      )}
    </SheetModal>
  );
}

// Asks before unsaved edits are dropped, in the app's own sheet instead of the browser's
// dialog: ask(go) runs go at once when nothing is unsaved, otherwise after Discard.
export function useDiscardConfirm(dirty: boolean): {
  ask: (go: () => void) => void;
  sheet: ReactElement;
} {
  const { t } = useI18n();
  const [pending, setPending] = useState<(() => void) | null>(null);
  const ask = (go: () => void): void => {
    if (dirty) setPending(() => go);
    else go();
  };
  const sheet = (
    <ConfirmSheet
      open={pending !== null}
      title={t('discardChanges')}
      body={t('discardChangesBody')}
      confirmLabel={t('discardChangesConfirm')}
      onConfirm={() => pending?.()}
      onClose={() => setPending(null)}
    />
  );
  return { ask, sheet };
}
