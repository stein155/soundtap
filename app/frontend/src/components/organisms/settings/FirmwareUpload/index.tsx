// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Button } from '../../../atoms/Button';
import { FormSection } from '../../../atoms/FormSection';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { useI18n } from '../../../../lib/i18n';
import { formatBytes } from '../../../../lib/format';
import { ReportCard } from './ReportCard';
import { Progress } from './styled';
import { useFirmwareUpload } from './useFirmwareUpload';

// Install a firmware image on a flashed speaker: pick a .stu, let the speaker check it,
// install into the inactive slot, then reboot into it. Install and reboot each ask first.
export function FirmwareUpload({ speaker }: { readonly speaker: string }): ReactElement {
  const { t, lang } = useI18n();
  const { file, report, signed, progress, outcome, inputRef, onChange, install, restart } =
    useFirmwareUpload(speaker);

  return (
    <>
      <FormSection spaced>{t('firmware')}</FormSection>
      <Note>{t('firmwarePickHint')}</Note>
      <input
        ref={inputRef}
        type="file"
        accept=".stu,.sig"
        multiple
        hidden
        onChange={(e) => {
          void onChange(e);
        }}
      />
      <Button
        variant="ghost"
        wide
        onClick={() => inputRef.current?.click()}
        disabled={progress !== null}
      >
        {file ? t('firmwarePickAnother') : t('firmwarePick')}
      </Button>
      {file && <Hint>{`${file.name} (${formatBytes(file.size, lang)})`}</Hint>}
      {progress !== null && (
        <>
          <Progress value={progress} max={100} />
          <Hint role="status">{`${t('firmwareUploading')}: ${progress}%`}</Hint>
        </>
      )}
      {report && (
        <ReportCard
          report={report}
          signed={signed}
          busy={progress !== null}
          onInstall={install}
          onReboot={restart}
        />
      )}
      {outcome && (
        <Hint error={outcome.error} role={outcome.error ? 'alert' : 'status'}>
          {outcome.text}
        </Hint>
      )}
    </>
  );
}
