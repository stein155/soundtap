// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useRef, useState } from 'react';
import { useI18n } from '../lib/i18n';

const SHOW_MS = 5000;

// The HTTP status an error carries (an ApiError), if any.
const statusOf = (err: unknown): number | undefined =>
  typeof err === 'object' && err !== null && 'status' in err && typeof err.status === 'number'
    ? err.status
    : undefined;

export type ReportFailure = (
  roomName: string,
  key?: 'actionFailed',
  page?: boolean
) => (err?: unknown) => void;
export type Notify = (text: string, takeBack?: () => void) => void;
export type Block = (text: string) => void;

export interface ErrorReporter {
  message: string;
  undo: (() => void) | null;
  failed: ReportFailure;
  notify: Notify;
  dismiss: () => void;
  blocked: { open: boolean; text: string };
  block: Block;
  dismissBlocked: () => void;
}

// One place where a failed action says so: "Kitchen is not responding", shown for a few
// seconds. `failed(roomName)` is the catch handler for any request to that room; `key`
// picks another message about that room.
export function useErrorReporter(): ErrorReporter {
  const { t } = useI18n();
  const [message, setMessage] = useState('');
  // The toast's action, when the message is about something that can be taken back.
  const [undo, setUndo] = useState<(() => void) | null>(null);
  // A failure that stopped what was asked gets a page, kept until it is closed. Its text stays
  // while the page slides away.
  const [blocked, setBlocked] = useState({ open: false, text: '' });
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const dismiss = useCallback(() => {
    setMessage('');
    setUndo(null);
  }, []);
  // A toast is short and already-translated text: a language change drops it rather than
  // leaving it in the old language. The language page cannot open over a blocking page.
  useEffect(() => dismiss, [t, dismiss]);
  // A failure arriving later speaks the language of that moment, not of the request's start.
  const latestT = useRef(t);
  useEffect(() => {
    latestT.current = t;
  }, [t]);
  const dismissBlocked = useCallback(() => setBlocked((b) => ({ ...b, open: false })), []);
  const block = useCallback((text: string) => setBlocked({ open: true, text }), []);
  const failed = useCallback(
    (roomName: string, key: 'actionFailed' = 'actionFailed', page = false) =>
      (err?: unknown) => {
        // A speaker that answered "no" (4xx) is not one that does not respond.
        const status = statusOf(err);
        const refused = status !== undefined && status >= 400 && status < 500;
        const text = latestT.current(refused ? 'actionRefused' : key, { name: roomName });
        if (page) {
          block(text);
          return;
        }
        setMessage(text);
        setUndo(null);
        clearTimeout(timer.current);
        timer.current = setTimeout(dismiss, SHOW_MS);
      },
    [dismiss, block]
  );
  // A change that went through, with a way to take it back ("Removed #3 · Undo").
  const notify = useCallback(
    (text: string, takeBack?: () => void) => {
      setMessage(text);
      setUndo(() => takeBack ?? null);
      clearTimeout(timer.current);
      timer.current = setTimeout(dismiss, SHOW_MS);
    },
    [dismiss]
  );
  return { message, undo, failed, notify, dismiss, blocked, block, dismissBlocked };
}
