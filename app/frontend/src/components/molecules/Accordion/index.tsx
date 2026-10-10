// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { SetItem } from '../SetItem';
import { SubPageContext, useSubPageHost, type SubPage as Page } from '../../../hooks/useSubPage';
import type { IconName } from '../../atoms/Icon';
import { Body } from './styled';

interface PageProps {
  open: boolean;
  onBack: () => void;
  title: string;
  children: ReactNode;
}

// A page of its own, Sonos-style: the list makes way, the header shows the page's title
// and back returns to the list. The page is drawn into the sheet's layer but stays part
// of this component, so a form keeps its edits across it.
export function SubPage({ open, onBack, title, children }: PageProps) {
  const { layer, show } = useSubPageHost();
  const back = useRef(onBack);
  useEffect(() => {
    back.current = onBack;
  });
  const self = useMemo(() => ({ title, back: () => back.current() }), [title]);
  useEffect(() => {
    if (!open) return;
    show(self);
    return () => show(null);
  }, [open, self, show]);
  // A page opened from this page hands the header back when it closes.
  const within = useMemo(
    () => ({ layer, show: (page: Page | null) => show(page ?? self) }),
    [layer, show, self]
  );
  const page = (
    <SubPageContext.Provider value={within}>
      <Body>{children}</Body>
    </SubPageContext.Provider>
  );
  return open && (layer ? createPortal(page, layer) : page);
}

interface Props {
  open: boolean;
  onToggle: () => void;
  icon?: IconName;
  title: string;
  // One line shown on the row: the current value.
  summary?: string;
  children: ReactNode;
}

// A settings row that opens a page of its own.
export function Accordion({ open, onToggle, icon, title, summary, children }: Props) {
  return (
    <>
      <SetItem icon={icon} label={title} meta={summary} onClick={onToggle} />
      <SubPage open={open} onBack={onToggle} title={title}>
        {children}
      </SubPage>
    </>
  );
}

// Keeps at most one row of a group open; returns the props for each one.
export function useAccordionGroup<Id extends string>() {
  const [openId, setOpenId] = useState<Id | null>(null);
  return (id: Id) => ({
    open: openId === id,
    onToggle: () => setOpenId(openId === id ? null : id),
  });
}
