// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useRef, type ReactElement } from 'react';
import { Icon } from '../../../atoms/Icon';
import { Button } from '../../../atoms/Button';
import { TextButton } from '../../../atoms/TextButton';
import { ModelArt, modelKey } from '../../../molecules/ModelArt';
import type { UpdateStage } from '../../../../hooks/updates';
import { useDialogFocus } from '../../../../hooks/useDialogFocus';
import { useEscape } from '../../../../hooks/useEscape';
import { useI18n, type I18nKey } from '../../../../lib/i18n';
import {
  Bars,
  Close,
  Dot,
  Head,
  Hero,
  Intro,
  Other,
  Page,
  Percent,
  Ring,
  Spin,
  StepList,
  StepNote,
  StepRow,
  TopLine,
  Warn,
} from './styled/updating';

const STEPS: I18nKey[] = [
  'updatingDownloaded',
  'updatingInstall',
  'updatingRestart',
  'updatingCheck',
];

// Which step is under way: installing passes too quickly to be seen from outside, so it is
// done once the speaker goes away to restart.
const CURRENT: Record<UpdateStage, number> = { download: 0, restart: 2, check: 3 };

const stepState = (i: number, current: number): 'done' | 'now' | 'later' => {
  if (i < current) return 'done';
  return i === current ? 'now' : 'later';
};

// The steps, each done, under way or still to come.
function Steps({ current }: { readonly current: number }) {
  const { t } = useI18n();
  return (
    <StepList aria-label={t('updatingSteps')}>
      {STEPS.map((key, i) => {
        const state = stepState(i, current);
        return (
          <StepRow key={key} $state={state} aria-current={state === 'now' ? 'step' : undefined}>
            <Dot $state={state} aria-hidden="true">
              {state === 'done' && <Icon.check width="12" height="12" />}
            </Dot>
            {t(key)}
          </StepRow>
        );
      })}
    </StepList>
  );
}

// The bar: which speaker updates, and Close, as on the other full pages.
function Bar({ name, onClose }: { readonly name: string; readonly onClose: () => void }) {
  const { t } = useI18n();
  return (
    <TopLine>
      <span>{t('updatingEyebrow', { name })}</span>
      <Close type="button" aria-label={t('close')} onClick={onClose}>
        <Icon.close />
      </Close>
    </TopLine>
  );
}

// The way on to another speaker, to control it while this one updates.
function OtherSpeaker({ onOther }: { readonly onOther: () => void }) {
  const { t } = useI18n();
  return (
    <Other>
      <TextButton onClick={onOther}>{t('offlineOther')}</TextButton>
    </Other>
  );
}

// The page over the app while a speaker updates itself: the speaker in a turning ring, how
// far it is when the speaker can say (progress, null when it cannot: no number is made up),
// what happens, the steps as far as they can be told, and to keep it on the power. Once the
// new version answers it checks in and the page reloads into it (or now, on request).
// The update goes on without it: Close (or Escape, or the phone's back) leaves the page for
// the rest of the app, and Choose another speaker goes on to the speakers.
export function Updating({
  name,
  model,
  stage,
  done,
  progress = null,
  onClose,
  onOther,
}: {
  readonly name: string;
  readonly model: string;
  readonly stage: UpdateStage;
  readonly done: boolean;
  readonly progress?: number | null;
  readonly onClose: () => void;
  readonly onOther?: (() => void) | undefined;
}): ReactElement {
  const { t } = useI18n();
  const ref = useRef<HTMLDivElement>(null);
  useDialogFocus(ref, true);
  useEscape(true, onClose);
  const current = CURRENT[stage];
  return (
    <Page
      ref={ref}
      tabIndex={-1}
      role="alertdialog"
      aria-modal="true"
      aria-label={t('updatingTitle')}
    >
      <Head>
        <Bar name={name} onClose={onClose} />
        <Bars aria-hidden="true">
          {STEPS.map((key, i) => (
            <span key={key} data-done={i <= current || undefined} />
          ))}
        </Bars>
        <Hero>
          <Ring aria-hidden="true">
            <Spin viewBox="0 0 148 148">
              <circle cx="74" cy="74" r="71" />
              <path d="M74 3a71 71 0 0 1 61.5 106.5" />
            </Spin>
            <div>
              <ModelArt model={model} />
            </div>
          </Ring>
          {progress !== null && !done && (
            <Percent>{t('updatingProgress', { percent: progress })}</Percent>
          )}
          <Intro>
            <h1>{t('updatingTitle')}</h1>
            <p>{t('updatingBody')}</p>
          </Intro>
          {done && (
            <Button variant="ghost" onClick={() => window.location.reload()}>
              {t('reloadNow')}
            </Button>
          )}
        </Hero>
      </Head>
      <Steps current={current} />
      {/* Stock warns of it: the soundbar's last step can sit a while before it moves on. */}
      {modelKey(model) === 'st300' && <StepNote>{t('updatingSt300')}</StepNote>}
      <Warn>
        <Icon.warning width="18" height="18" aria-hidden="true" />
        <p>
          <b>{t('updatingPower')}</b> {t('updatingFallback')}
        </p>
      </Warn>
      {onOther && <OtherSpeaker onOther={onOther} />}
    </Page>
  );
}
