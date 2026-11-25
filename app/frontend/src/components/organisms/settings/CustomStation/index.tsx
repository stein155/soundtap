// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactElement } from 'react';
import { Button } from '../../../atoms/Button';
import { TextField } from '../../../atoms/TextField';
import { LabeledField } from '../../../molecules/LabeledField';
import { CustomStationHelp } from '../CustomStationHelp';
import { useI18n } from '../../../../lib/i18n';
import { isStreamUrl } from '../../../../lib/station';
import type { Station } from '../../../../lib/types';
import { Footer, Form, Small, Text } from './styled';

// What ffmpeg on the speaker has decoders and demuxers for (os/configs/soundtap_defconfig).
export const STREAM_FORMATS = 'MP3, MP4-AAC, ALAC, AAC, HEAAC, HLS, OGG, Vorbis, Opus, FLAC, WAV';

// What a preset can hold, and Stockholm's length for a name.
const MAX_URL = 512;
const MAX_NAME = 32;

// Play from a link: a stream that is in no directory, as Stockholm's Custom Station page
// has it. From here on it is a station like any other, with the URL as its id (ADR 0041).
// What to check when it does not play folds open under the form.
export function CustomStation({
  onStream,
}: {
  onStream: (station: Station) => void;
}): ReactElement {
  const { t } = useI18n();
  const [url, setUrl] = useState('');
  const [name, setName] = useState('');
  const [submitted, setSubmitted] = useState(false);
  const stream = url.trim();
  const station = name.trim();

  let urlProblem: string | null = null;
  if (!stream) urlProblem = t('requiredField');
  else if (!isStreamUrl(stream))
    urlProblem = /^https?:\/\//i.test(stream) ? t('customHelpInvalid') : t('urlScheme');
  const nameProblem = station ? null : t('requiredField');

  return (
    <>
      <Form
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          setSubmitted(true);
          if (urlProblem || nameProblem) return;
          onStream({
            tuneInId: stream,
            name: station,
            tagline: '',
            genre: '',
            country: '',
            logo: '',
          });
        }}
      >
        <Text>{t('customStationIntro')}</Text>
        <Small>
          {t('supportedFormats')} {STREAM_FORMATS}
        </Small>
        <LabeledField id="custom-url" label={t('streamUrl')} error={submitted ? urlProblem : null}>
          <TextField
            id="custom-url"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            type="url"
            inputMode="url"
            maxLength={MAX_URL}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
          />
        </LabeledField>
        <LabeledField id="custom-name" label={t('name')} error={submitted ? nameProblem : null}>
          <TextField
            id="custom-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={t('customStationNameExample')}
            maxLength={MAX_NAME}
          />
        </LabeledField>
        <Footer>
          <Small>{t('customStationFooter')}</Small>
          <Button type="submit" variant="stockholm">
            {t('streamStation')}
          </Button>
        </Footer>
      </Form>
      <CustomStationHelp />
    </>
  );
}
