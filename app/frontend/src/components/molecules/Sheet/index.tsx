// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, type ReactNode, type ReactElement } from 'react';
import { useFocusMemory } from '../../../hooks/useFocusMemory';
import { useScrollMemory } from '../../../hooks/useScrollMemory';
import { Icon } from '../../atoms/Icon';
import { useEscape } from '../../../hooks/useEscape';
import { useI18n } from '../../../lib/i18n';
import type { SubPageAction } from '../../../hooks/useSubPage';
import {
  Body,
  Header,
  HeaderField,
  Headline,
  Hero,
  HiddenHeadline,
  IconButton,
  Kicker,
  Page,
  Right,
  Titles,
} from './styled';

interface Props {
  // The X on the right: leaves the whole page.
  readonly onExit?: (() => void) | undefined;
  // The back chevron on the left, only when there is a level to go up to.
  readonly onBack?: (() => void) | undefined;
  readonly backLabel?: string;
  // Where the page sits ("Woonkamer"), small in the bar; the headline is then the page's
  // own large title at the top of the body, with intro under it.
  readonly eyebrow?: ReactNode;
  readonly headline?: ReactNode;
  readonly intro?: ReactNode;
  // The page draws its own title (a speaker's name by its picture): no large headline.
  readonly ownHero?: boolean | undefined;
  readonly label?: string;
  // A bar of the page's own in place of this header (the source pages' top bar).
  readonly bar?: ReactNode;
  // Takes the place of the title, as Stockholm's search does: the X then steps back.
  readonly field?: ReactNode | undefined;
  // Rows between the header and the scrolling body (tabs).
  readonly toolbar?: ReactNode;
  // A button of the page's own beside the X (Wi-Fi's search again).
  readonly action?: SubPageAction | undefined;
  // Where in a nested list the body is (a browse level). Each one keeps its own scroll
  // position: going back lands where you were, going deeper starts at the top.
  readonly scrollKey?: string;
  readonly children: ReactNode;
}

interface HeaderProps {
  readonly field?: ReactNode | undefined;
  readonly eyebrow?: ReactNode;
  readonly ownHero?: boolean | undefined;
  readonly headline?: ReactNode;
  readonly back?: (() => void) | undefined;
  readonly backLabel: string;
  readonly exit?: (() => void) | undefined;
  readonly action?: SubPageAction | undefined;
}

// The bar's middle: the page's title, or where it sits when the page has a large title of
// its own below.
function HeaderTitle({
  eyebrow,
  ownHero,
  headline,
}: Pick<HeaderProps, 'eyebrow' | 'ownHero' | 'headline'>) {
  const kicker = Boolean(eyebrow);
  return (
    <Titles>
      {kicker ? <Kicker>{eyebrow}</Kicker> : <Headline>{headline}</Headline>}
      {kicker && ownHero === true && <HiddenHeadline>{headline}</HiddenHeadline>}
    </Titles>
  );
}

// Back, title and X; a field takes the place of the title and its X steps back.
function SheetHeader({
  field,
  eyebrow,
  ownHero,
  headline,
  back,
  backLabel,
  exit,
  action,
}: HeaderProps) {
  const { t } = useI18n();
  const hasField = Boolean(field);
  const Glyph = action && !hasField ? Icon[action.icon] : null;
  return (
    <Header $wide={!!Glyph}>
      {back && !hasField && (
        <IconButton $side="left" onClick={back} aria-label={backLabel}>
          <Icon.back width="22" height="22" strokeWidth="2" />
        </IconButton>
      )}
      {hasField ? (
        <HeaderField>{field}</HeaderField>
      ) : (
        <HeaderTitle eyebrow={eyebrow} ownHero={ownHero} headline={headline} />
      )}
      {(exit ?? Glyph) && (
        <Right>
          {action && Glyph && (
            <IconButton
              $side="right"
              onClick={action.onPress}
              disabled={action.disabled}
              aria-label={action.label}
            >
              <Glyph width="22" height="22" />
            </IconButton>
          )}
          {exit && (
            <IconButton
              $side="right"
              onClick={hasField ? (back ?? exit) : exit}
              aria-label={t('close')}
            >
              <Icon.close width="22" height="22" strokeWidth="2" />
            </IconButton>
          )}
        </Right>
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
  intro,
  ownHero,
  label,
  bar,
  field,
  toolbar,
  scrollKey,
  action,
  children,
}: Props): ReactElement {
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
  // A large title of the page's own, under no custom bar or field.
  const covered = Boolean(bar) || Boolean(field);
  const hero = Boolean(eyebrow) && !covered && ownHero !== true;
  return (
    <Page ref={pageRef} tabIndex={-1} aria-label={name}>
      {bar ?? (
        <SheetHeader
          field={field}
          eyebrow={eyebrow}
          ownHero={ownHero}
          headline={headline}
          back={onBack}
          backLabel={backLabel || t('back')}
          exit={onExit}
          action={action}
        />
      )}
      {toolbar}
      <Body ref={bodyRef} onScroll={onBodyScroll} onFocus={onBodyFocus}>
        {hero && (
          <Hero>
            <h1>{headline}</h1>
            {Boolean(intro) && <p>{intro}</p>}
          </Hero>
        )}
        {children}
      </Body>
    </Page>
  );
}
