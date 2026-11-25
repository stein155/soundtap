// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useMemo, useRef, useState, type ReactNode, type ReactElement } from 'react';
import { createPortal } from 'react-dom';
import { SetItem } from '../SetItem';
import {
  SubPageContext,
  useSubPageHost,
  type SubPage as Page,
  type SubPageAction,
} from '../../../hooks/useSubPage';
import type { IconName } from '../../atoms/Icon';
import { Body } from './styled';

interface PageProps {
  open: boolean;
  onBack: () => void;
  title: string;
  // A button of the page's own in the header, beside the X.
  action?: SubPageAction | undefined;
  children: ReactNode;
}

// A page of its own, Sonos-style: the list makes way, the header shows the page's title
// and back returns to the list. The page is drawn into the sheet's layer but stays part
// of this component, so a form keeps its edits across it.
export function SubPage({ open, onBack, title, action, children }: PageProps): ReactElement | null {
  const { layer, show } = useSubPageHost();
  const back = useRef(onBack);
  const press = useRef(action?.onPress);
  useEffect(() => {
    back.current = onBack;
    press.current = action?.onPress;
  });
  // Rebuilt only when what the header shows changes: the handlers are read through refs.
  const icon = action?.icon;
  const label = action?.label;
  const disabled = action?.disabled;
  const self = useMemo<Page>(
    () => ({
      title,
      back: () => back.current(),
      action:
        icon && label ? { icon, label, disabled, onPress: () => press.current?.() } : undefined,
    }),
    [title, icon, label, disabled]
  );
  useEffect(() => {
    if (!open) return;
    show(self);
    return () => show(null);
  }, [open, self, show]);
  // A page opened from this page names this one above its title, and hands the header
  // back when it closes.
  const within = useMemo(
    () => ({
      layer,
      show: (page: Page | null) => show(page ? { eyebrow: title, ...page } : self),
    }),
    [layer, show, self, title]
  );
  const page = (
    <SubPageContext.Provider value={within}>
      <Body>{children}</Body>
    </SubPageContext.Provider>
  );
  if (!open) return null;
  return layer ? createPortal(page, layer) : page;
}

interface Props {
  open: boolean;
  onToggle: () => void;
  icon?: IconName | undefined;
  title: string;
  // One line shown on the row: the current value.
  summary?: string | undefined;
  // Or a sentence under the title that says what the page is for, beside its icon on a
  // grey disc (a route to take).
  desc?: string | undefined;
  // What waits on the page (a new version), in an ink badge before the chevron.
  badge?: string | undefined;
  children: ReactNode;
}

// A settings row that opens a page of its own.
export function Accordion({
  open,
  onToggle,
  icon,
  title,
  summary,
  desc,
  badge,
  children,
}: Props): ReactElement {
  return (
    <>
      <SetItem
        icon={icon}
        label={title}
        meta={summary}
        desc={desc}
        disc={!!desc}
        badge={badge}
        inkBadge
        onClick={onToggle}
      />
      <SubPage open={open} onBack={onToggle} title={title}>
        {children}
      </SubPage>
    </>
  );
}

// Keeps at most one row of a group open (initially the one given, if any); returns the
// props for each one.
export interface AccordionRow {
  open: boolean;
  onToggle: () => void;
}

export function useAccordionGroup<Id extends string>(
  initial: Id | null = null
): (id: Id) => AccordionRow {
  const [openId, setOpenId] = useState<Id | null>(initial);
  return (id: Id) => ({
    open: openId === id,
    onToggle: () => setOpenId(openId === id ? null : id),
  });
}
