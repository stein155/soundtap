// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, type ReactNode, type ReactElement } from 'react';
import { Dialog, Modal, ModalOverlay } from 'react-aria-components';
import { useEscape } from '../../../hooks/useEscape';
import { swipeToClose } from './swipe';

export type SheetVariant = 'sheet' | 'full' | 'page' | 'drawer' | 'dialog';

interface Props {
  open: boolean;
  onClose: () => void;
  // The dialog's accessible name.
  label: string;
  variant?: SheetVariant;
  // Extra classes on the sheet itself (a background, a layout).
  className?: string;
  // The content, or a function of the sheet's own close.
  children: ReactNode | ((close: () => void) => ReactNode);
}

// A layer over the page: a sheet from the bottom, a full-height one, the drawer from the
// left, a plain page from the right, as Stockholm's settings, or a dialog in the middle for
// a question. It traps focus, closes on
// Escape and on the phone's back, and animates in and out (the styles are in
// pages/HomePage/home.css). All but the page also dim what is under them and close on a tap
// outside and on a swipe (a sheet down, the drawer left).
export function SheetModal({
  open,
  onClose,
  label,
  variant = 'sheet',
  className,
  children,
}: Props): ReactElement {
  // Only useEscape closes on Escape, one level at a time: the overlay's own handling would
  // take the key first and close every level at once.
  useEscape(open, onClose, true);
  const cls = {
    sheet: 'sheet',
    full: 'sheet full',
    page: 'sheet full page',
    drawer: 'drawer',
    dialog: 'sheet dialog',
  };
  const overlay = { sheet: '', full: '', page: ' bare', drawer: ' left', dialog: ' center' };
  return (
    <ModalOverlay
      className={`overlay${overlay[variant]}`}
      isDismissable
      isKeyboardDismissDisabled
      isOpen={open}
      onOpenChange={(v) => !v && onClose()}
    >
      <Modal className={`${cls[variant]}${className ? ` ${className}` : ''}`}>
        <Dialog className="dlg" aria-label={label}>
          {({ close }): ReactNode => {
            const body = typeof children === 'function' ? children(close) : children;
            if (variant === 'page' || variant === 'dialog') return body;
            return (
              <SwipeClose drawer={variant === 'drawer'} onClose={close}>
                {body}
              </SwipeClose>
            );
          }}
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}

// SwipeClose lets a finger drag a sheet down, or the drawer to the left, and closes it
// when let go far or fast enough; otherwise it springs back. The direction locks after
// a few pixels: a sideways swipe stays the speaker tray's or a slider's, and a swipe that
// starts in a list scrolled away from its top stays the list's. While the layer follows
// the finger its moves are not passive, so nothing under it scrolls along. The wrapper is
// display: contents, so the layout is the layer's own, and the exit animation runs on
// from where the finger let go.
function SwipeClose({
  drawer,
  onClose,
  children,
}: {
  drawer: boolean;
  onClose: () => void;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const close = useRef(onClose);
  useEffect(() => {
    close.current = onClose;
  });
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    return swipeToClose(el, drawer, () => close.current());
  }, [drawer]);
  return (
    <div ref={ref} style={{ display: 'contents' }}>
      {children}
    </div>
  );
}
