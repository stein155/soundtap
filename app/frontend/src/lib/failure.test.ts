// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { beforeAll, describe, it, expect } from 'vitest';
import { failureText } from './failure';
import { httpError } from './api/http';
import { loadLanguage, makeT } from './i18n';

const answer = (status: number, body: unknown) => httpError(status, JSON.stringify(body), 'POST');

beforeAll(() => loadLanguage('nl'));

// Users see a translated message; a refusal the user can act on adds its reason, said by
// the code of the speaker's answer, never in the speaker's English.
describe('failureText', () => {
  it('adds the translated reason of a code', () => {
    const err = answer(403, {
      error: 'press a button on the device to confirm',
      code: 'not_confirmed',
    });
    expect(failureText(err, 'Kon niet verbinden.', makeT('nl'))).toBe(
      'Kon niet verbinden. (niemand hield op tijd een knop van de speaker vast)'
    );
    expect(failureText(err, 'Couldn’t save', makeT('en'))).toBe(
      'Couldn’t save (nobody held a button on the speaker in time)'
    );
  });

  it('says busy, validation, not found and unavailable in the page language', () => {
    const t = makeT('nl');
    expect(failureText(answer(409, { error: 'x', code: 'busy' }), 'Mislukt.', t)).toBe(
      'Mislukt. (de speaker is bezig met een andere taak)'
    );
    expect(failureText(answer(400, { error: 'x', code: 'invalid_request' }), 'Mislukt.', t)).toBe(
      'Mislukt. (de speaker accepteerde wat er is ingevuld niet)'
    );
    expect(failureText(answer(404, { error: 'x', code: 'not_found' }), 'Mislukt.', t)).toBe(
      'Mislukt. (de speaker kon het niet vinden)'
    );
    expect(failureText(answer(503, { error: 'x', code: 'unavailable' }), 'Mislukt.', t)).toBe(
      'Mislukt. (nu niet beschikbaar op de speaker)'
    );
  });

  it('never shows the speaker’s English', () => {
    const t = makeT('nl');
    // An older speaker sends no code; a newer one may send one this page does not know.
    expect(
      failureText(answer(400, { error: 'WPA password must be 8..63 bytes' }), 'Mislukt.', t)
    ).toBe('Mislukt.');
    expect(failureText(answer(409, { error: 'x', code: 'brand_new' }), 'Mislukt.', t)).toBe(
      'Mislukt.'
    );
  });

  it('keeps outages and failures inside the speaker to the translated text', () => {
    const t = makeT('nl');
    expect(failureText(new Error('App upload timed out.'), 'Installatie mislukt.', t)).toBe(
      'Installatie mislukt.'
    );
    expect(
      failureText(answer(500, { error: 'set wifi failed', code: 'internal' }), 'Mislukt.', t)
    ).toBe('Mislukt.');
    expect(failureText(answer(502, { error: 'x', code: 'upstream_failed' }), 'Mislukt.', t)).toBe(
      'Mislukt.'
    );
  });
});

describe('httpError', () => {
  it('keeps the message, the status and the code', () => {
    const err = answer(409, { error: 'another firmware operation is in progress', code: 'busy' });
    expect(err.message).toBe('another firmware operation is in progress');
    expect(err.status).toBe(409);
    expect(err.code).toBe('busy');
  });

  it('falls back to the body when it is not the error shape', () => {
    const err = httpError(502, 'Bad Gateway', 'POST /api/firmware');
    expect(err.message).toBe('Bad Gateway');
    expect(err.code).toBeUndefined();
  });
});
