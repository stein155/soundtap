// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { Button } from '../../atoms/Button';
import { Icon } from '../../atoms/Icon';
import { useI18n } from '../../../lib/i18n';
import { SheetModal } from '../SheetModal';
import type { ReactElement } from 'react';

const Body = styled.div`
  flex: 1;
  display: flex;
  flex-direction: column;
  padding: 16px 16px calc(28px + env(safe-area-inset-bottom));
`;

const Message = styled.div`
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  font-size: 15px;
  line-height: 1.5;
  text-align: center;
`;

// A sheet over the app for a failure that stopped what was asked, as Stockholm's: almost the
// whole height, with its grip, what went wrong, and one button at the bottom. A toast stays
// for what went through.
export function ErrorPage({
  open,
  message,
  onClose,
}: {
  open: boolean;
  message: string;
  onClose: () => void;
}): ReactElement {
  const { t } = useI18n();
  return (
    <SheetModal open={open} onClose={onClose} label={message} className="errpage">
      {(close) => (
        <>
          <span className="ract-grab" aria-hidden="true" />
          <Body role="alert">
            <Message>
              <Icon.info width="36" height="36" />
              {message}
            </Message>
            <Button variant="ghost" wide onClick={close}>
              {t('close')}
            </Button>
          </Body>
        </>
      )}
    </SheetModal>
  );
}
