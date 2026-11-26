// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Heading } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { Spinner } from '../../atoms/Spinner';
import { ModelArt } from '../../molecules/ModelArt';
import { SetMenu } from '../../molecules/SetMenu';
import { SheetModal } from '../../molecules/SheetModal';
import { useI18n } from '../../../lib/i18n';

interface Props {
  readonly open: boolean;
  readonly name: string;
  readonly model?: string | undefined;
  // Away with the page until the speaker is lost again (the banner stays meanwhile).
  readonly onClose: () => void;
  // Another speaker to control instead (the speakers sheet).
  readonly onOther: () => void;
}

// What to look at, with the line drawing the design gives each.
const TIPS = [
  ['offlineTipPower', 'plug'],
  ['offlineTipWifi', 'wifi'],
  ['offlineTipRouter', 'clock'],
] as const;

// What the page shows once the speaker it controls has not answered for a while, as the
// design's "Speaker weg": the speaker greyed out, that this page keeps trying and goes on
// where it was, what to check, and a way to another speaker. It closes by itself once the
// speaker answers again; the button only tries at once.
export function OfflinePage({ open, name, model, onClose, onOther }: Props): ReactElement {
  const { t } = useI18n();
  return (
    <SheetModal open={open} onClose={onClose} label={t('offlineTitle')} variant="full">
      {(close) => (
        <div className="off-page" role="alert">
          <div className="off-bar">
            <button type="button" className="off-x" aria-label={t('close')} onClick={close}>
              <Icon.close />
            </button>
          </div>
          <div className="off-head">
            <span className="off-disc" aria-hidden="true">
              <ModelArt model={model} />
              <span className="off-badge">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4">
                  <path
                    strokeLinecap="round"
                    d="M2 9a15 15 0 0 1 20 0M5 12.5a10 10 0 0 1 14 0M12 19.5h.01M3 3l18 18"
                  />
                </svg>
              </span>
            </span>
            <p className="off-name">{name}</p>
            <Heading slot="title" className="off-title">
              {t('offlineTitle')}
            </Heading>
            <p className="off-text">{t('offlineRetrying')}</p>
          </div>
          <SetMenu title={t('offlineCheck')}>
            {TIPS.map(([key, icon]) => {
              const Glyph = Icon[icon];
              return (
                <p key={key} className="off-row">
                  <Glyph aria-hidden="true" />
                  <span>{t(key)}</span>
                </p>
              );
            })}
          </SetMenu>
          <div className="off-actions">
            <button type="button" className="off-primary" onClick={() => window.location.reload()}>
              <Spinner small />
              {t('offlineRetry')}
            </button>
            <button type="button" className="off-link" onClick={onOther}>
              {t('offlineOther')}
            </button>
          </div>
        </div>
      )}
    </SheetModal>
  );
}
