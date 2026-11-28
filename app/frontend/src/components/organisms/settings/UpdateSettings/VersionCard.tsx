// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useId, useState, type ReactNode, type ReactElement } from 'react';
import { TextButton } from '../../../atoms/TextButton';
import { useI18n } from '../../../../lib/i18n';
import { ReleaseNotes } from './ReleaseNotes';
import { Actions, Card, Kicker, Meta, Title } from './styled';

// Whether a slot holds something to draw: what is not there, false or an empty line is not.
const shows = (node: ReactNode): boolean =>
  node !== undefined && node !== null && node !== false && node !== '';

// A version in a card: a newer one on offer (outlined in ink, as drawn) with what it
// replaces and its notes behind "What's new", or the one the speaker runs. Its buttons and
// what they report go under it.
export function VersionCard({
  offer,
  kicker,
  title,
  meta,
  notes,
  children,
}: {
  readonly offer?: boolean;
  readonly kicker: string;
  readonly title: string;
  readonly meta?: ReactNode;
  readonly notes?: string | undefined;
  readonly children?: ReactNode;
}): ReactElement {
  const { t } = useI18n();
  const [showNotes, setShowNotes] = useState(false);
  const notesId = useId();
  return (
    <Card $offer={offer} aria-label={title}>
      <Kicker>{kicker}</Kicker>
      <Title>{title}</Title>
      {shows(meta) && <Meta>{meta}</Meta>}
      {notes && (
        <TextButton
          aria-expanded={showNotes}
          aria-controls={notesId}
          onClick={() => setShowNotes(!showNotes)}
        >
          {t('whatsNew')}
        </TextButton>
      )}
      {notes && showNotes && <ReleaseNotes id={notesId} md={notes} />}
      {shows(children) && <Actions>{children}</Actions>}
    </Card>
  );
}
