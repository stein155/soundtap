// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, type ReactNode } from 'react';
import { useFocusMemory } from '../../../hooks/useFocusMemory';
import { useScrollMemory } from '../../../hooks/useScrollMemory';
import { Icon } from '../../atoms/Icon';
import { useEscape } from '../../../hooks/useEscape';
import { useI18n } from '../../../lib/i18n';
import { Body, Header, HeaderField, Headline, IconButton, Page, Sub, Titles } from './styled';

interface Props {
  // The X on the right: leaves the whole page.
  onExit?: () => void;
  // The back chevron on the left, only when there is a level to go up to.
  onBack?: () => void;
  backLabel?: string;
  // A quiet line under the title ("for Kitchen").
  eyebrow?: ReactNode;
  headline?: ReactNode;
  label?: string;
  // A bar of the page's own in place of this header (the source pages' top bar).
  bar?: ReactNode;
  // Takes the place of the title, as Stockholm's search does: the X then steps back.
  field?: ReactNode;
  // Rows between the header and the scrolling body (tabs).
  toolbar?: ReactNode;
  // Where in a nested list the body is (a browse level). Each one keeps its own scroll
  // position: going back lands where you were, going deeper starts at the top.
  scrollKey?: string;
  children: ReactNode;
}

interface HeaderProps {
  field?: ReactNode;
  eyebrow?: ReactNode;
  headline?: ReactNode;
  back?: () => void;
  backLabel: string;
  exit?: () => void;
}

// Back, title and X; a field takes the place of the title and its X steps back.
function SheetHeader({ field, eyebrow, headline, back, backLabel, exit }: HeaderProps) {
  const { t } = useI18n();
  return (
    <Header>
      {back && !field && (
        <IconButton $side="left" onClick={back} aria-label={backLabel}>
          <Icon.back width="24" height="24" />
        </IconButton>
      )}
      {field ? (
        <HeaderField>{field}</HeaderField>
      ) : (
        <Titles>
          <Headline>{headline}</Headline>
          {eyebrow && <Sub>{eyebrow}</Sub>}
        </Titles>
      )}
      {exit && (
        <IconButton $side="right" onClick={field ? (back ?? exit) : exit} aria-label={t('close')}>
          <Icon.close width="24" height="24" />
        </IconButton>
      )}
    </Header>
  );
}

// The page every list and browse view uses: one shared header (back, title, close) over a
// scrolling body. It fills what holds it. Escape does what the back chevron does.
export function Sheet({
  onExit,
  onBack,
  backLabel,
  eyebrow,
  headline,
  label,
  bar,
  field,
  toolbar,
  scrollKey,
  children,
}: Props) {
  const { t } = useI18n();
  const [bodyRef, onBodyScroll] = useScrollMemory(scrollKey);
  useEscape(!!onBack, () => onBack?.());
  const name = label || (typeof headline === 'string' ? headline : undefined);
  // The row that opened this page is gone or covered: without this, focus falls to the body
  // and a keyboard or screen reader starts over from the top of the app.
  const pageRef = useRef<HTMLElement>(null);
  useEffect(() => {
    pageRef.current?.focus({ preventScroll: true });
  }, []);
  const onBodyFocus = useFocusMemory(true, scrollKey, pageRef, bodyRef);
  return (
    <Page ref={pageRef} tabIndex={-1} aria-label={name}>
      {bar ?? (
        <SheetHeader
          field={field}
          eyebrow={eyebrow}
          headline={headline}
          back={onBack}
          backLabel={backLabel || t('back')}
          exit={onExit}
        />
      )}
      {toolbar}
      <Body ref={bodyRef} onScroll={onBodyScroll} onFocus={onBodyFocus} $tight={!!toolbar}>
        {children}
      </Body>
    </Page>
  );
}
