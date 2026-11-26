// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Icon } from '../../../atoms/Icon';
import { Button } from '../../../atoms/Button';
import { ModelArt, modelKey } from '../../../molecules/ModelArt';
import type { UpdateStage } from '../../../../hooks/useSelfUpdate';
import { useI18n, type I18nKey } from '../../../../lib/i18n';
import {
  Bars,
  Dot,
  Head,
  Hero,
  Intro,
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

// The page over the app while a speaker updates itself: the speaker in a turning ring, how
// far it is when the speaker can say (progress, null when it cannot: no number is made up),
// what happens, the steps as far as they can be told, and to keep it on the power. Once the
// new version answers it checks in and the page reloads into it (or now, on request).
export function Updating({
  name,
  model,
  stage,
  done,
  progress = null,
}: {
  readonly name: string;
  readonly model: string;
  readonly stage: UpdateStage;
  readonly done: boolean;
  readonly progress?: number | null;
}): ReactElement {
  const { t } = useI18n();
  const current = CURRENT[stage];
  return (
    <Page role="alertdialog" aria-modal="true" aria-label={t('updatingTitle')}>
      <Head>
        <TopLine>{t('updatingEyebrow', { name })}</TopLine>
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
    </Page>
  );
}
