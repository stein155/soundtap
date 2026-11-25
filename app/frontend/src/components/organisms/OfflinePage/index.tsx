// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Heading } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { ModelArt } from '../../molecules/ModelArt';
import { SetMenu } from '../../molecules/SetMenu';
import { SheetModal } from '../../molecules/SheetModal';
import { useI18n } from '../../../lib/i18n';

interface Props {
  open: boolean;
  name: string;
  model?: string;
  // Another speaker to control instead (the speakers sheet).
  onOther: () => void;
}

// What the page shows once the speaker it controls has not answered for a while, as the
// design's "Speaker weg": the speaker greyed out, that this page keeps trying and goes on
// where it was, what to check, and a way to another speaker. It closes by itself once the
// speaker answers again; the button only reloads the page sooner.
export function OfflinePage({ open, name, model, onOther }: Props) {
  const { t } = useI18n();
  return (
    <SheetModal open={open} onClose={onOther} label={t('offlineTitle')} variant="full">
      {() => (
        <div className="off-page" role="alert">
          <div className="off-head">
            <span className="off-disc" aria-hidden="true">
              <ModelArt model={model} />
              <span className="off-badge">
                <Icon.wifi />
              </span>
            </span>
            <p className="off-name">{name}</p>
            <Heading slot="title" className="off-title">
              {t('offlineTitle')}
            </Heading>
            <p className="off-text">{t('offlineRetrying')}</p>
          </div>
          <SetMenu title={t('offlineCheck')}>
            <p className="off-row">{t('offlineHelpPower')}</p>
            <p className="off-row">{t('offlineHelpReplug')}</p>
            <p className="off-row">{t('offlineHelpBlink')}</p>
          </SetMenu>
          <div className="off-actions">
            <button type="button" className="off-primary" onClick={() => window.location.reload()}>
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
