// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { I18nKey, TranslateFn } from './i18n';
import type { ApiError, ApiErrorCode } from './types';

// The reason a speaker gives for a code the user can act on, in the page's language. An
// outage, a timeout or a failure inside the speaker has nothing to add to the fallback.
const REASONS: Partial<Record<ApiErrorCode, I18nKey>> = {
  invalid_request: 'errInvalidRequest',
  not_found: 'errNotFound',
  forbidden: 'errForbidden',
  signature_invalid: 'errSignatureInvalid',
  confirm_required: 'errConfirmRequired',
  not_confirmed: 'errNotConfirmed',
  confirm_superseded: 'errConfirmSuperseded',
  busy: 'errBusy',
  conflict: 'errConflict',
  too_large: 'errTooLarge',
  package_rejected: 'errPackageRejected',
  precondition_failed: 'errPreconditionFailed',
  unsupported: 'errUnsupported',
  unavailable: 'errUnavailable',
  clock_not_set: 'errClockNotSet',
  storage_full: 'errStorageFull',
};

// What a failed action says: always the translated `fallback`, followed by the
// translated reason when the speaker's error code has one (ADR 0091). The speaker's own
// English message never reaches the page: a code from a newer speaker, or none from an
// older one, leaves the fallback on its own.
export function failureText(err: unknown, fallback: string, t: TranslateFn): string {
  const code = (err as Partial<ApiError> | null)?.code;
  const key = code ? REASONS[code as ApiErrorCode] : undefined;
  return key ? `${fallback} (${t(key)})` : fallback;
}
