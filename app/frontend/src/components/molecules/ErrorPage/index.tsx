// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { Button } from '../../atoms/Button';
import { Icon } from '../../atoms/Icon';
import { useI18n } from '../../../lib/i18n';
import { SheetModal } from '../SheetModal';

const Body = styled.div`
  flex: 1;
  display: flex;
  flex-direction: column;
  padding: 16px;
`;

const Message = styled.div`
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  font-size: 14.4px;
  text-align: center;
`;

// A page over the app for a failure that stopped what was asked, as Stockholm's: what went
// wrong, and one button at the bottom. A toast stays for what went through.
export function ErrorPage({
  open,
  message,
  onClose,
}: {
  open: boolean;
  message: string;
  onClose: () => void;
}) {
  const { t } = useI18n();
  return (
    <SheetModal open={open} onClose={onClose} label={message} variant="full">
      {(close) => (
        <Body role="alert">
          <Message>
            <Icon.info width="36" height="36" />
            {message}
          </Message>
          <Button variant="ghost" wide onClick={close}>
            {t('close')}
          </Button>
        </Body>
      )}
    </SheetModal>
  );
}
