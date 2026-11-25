// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { useI18n } from '../../../../lib/i18n';
import { Button } from '../../../atoms/Button';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { Dock, Form } from './styled';
import { canJoin, type JoinTarget, type WifiJoin } from './useWifiJoin';

// The form around a network to join: Connect, and only once for what it holds. docked:
// Connect (and after) at the bottom of the screen, with how the join goes (status) above
// it (the first-run page).
export function JoinForm({
  join,
  target,
  children,
  after,
  docked = false,
  status,
}: {
  join: WifiJoin;
  target: JoinTarget;
  children: ReactNode;
  after?: ReactNode;
  docked?: boolean | undefined;
  status?: ReactNode;
}): ReactElement {
  const { t } = useI18n();
  const { kind } = join.phase;
  const locked = !canJoin(target) || kind === 'busy' || kind === 'done';
  const submit = (
    <Button type="submit" variant="stockholm" wide disabled={locked}>
      {t('wifiConnect')}
    </Button>
  );
  return (
    <Form
      noValidate
      $docked={docked}
      onSubmit={(e) => {
        e.preventDefault();
        if (!locked) void join.connect(target);
      }}
    >
      {children}
      {status}
      {docked ? (
        <Dock>
          {submit}
          {after}
        </Dock>
      ) : (
        <>
          {submit}
          {after}
        </>
      )}
    </Form>
  );
}

// How the join goes, and on the first-run page where to find the speaker once it is done.
export function JoinStatus({
  join,
  fixedSurvey = false,
  host,
}: {
  join: WifiJoin;
  fixedSurvey?: boolean | undefined;
  host: string;
}): ReactElement {
  const { t } = useI18n();
  const { kind, text } = join.phase;
  return (
    <>
      {text && (
        <Hint error={kind === 'error'} role="status">
          {text}
        </Hint>
      )}
      {kind === 'busy' && <Hint>{t('wifiPleaseWait')}</Hint>}
      {kind === 'done' && fixedSurvey && (
        <>
          <Note>{t('setupNextSteps', { host })}</Note>
          <Note>{t('setupRouterHint', { device: host.replace(/\.local$/, '') })}</Note>
        </>
      )}
    </>
  );
}
