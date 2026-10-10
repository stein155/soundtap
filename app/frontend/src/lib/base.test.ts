// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { afterEach, describe, expect, it, vi } from 'vitest';

import { apiFetch, apiURL, setTargetDevice } from './base';

function at(href: string) {
  const u = new URL(href);
  vi.stubGlobal('location', {
    protocol: u.protocol,
    hostname: u.hostname,
    port: u.port,
    host: u.host,
    href,
  });
}

afterEach(() => vi.unstubAllGlobals());

describe('apiURL with another speaker selected', () => {
  afterEach(() => setTargetDevice(''));

  it('routes through the serving speaker, except the device list', () => {
    at('http://192.0.2.126/');
    setTargetDevice('SERIAL0000000010A');
    expect(apiURL('/api/now')).toBe('/api/devices/SERIAL0000000010A/api/now');
    expect(apiURL('/api/devices')).toBe('/api/devices');
    setTargetDevice('');
    expect(apiURL('/api/now')).toBe('/api/now');
  });
});

describe('apiFetch', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it('sends a write once more under the same id after a network error', async () => {
    const ids: (string | null)[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init?: RequestInit) => {
        ids.push(new Headers(init?.headers).get('X-SoundTap-Request'));
        if (ids.length === 1) throw new TypeError('Load failed');
        return new Response('{}');
      })
    );
    const r = await apiFetch('/api/play/1', { method: 'POST' });
    expect(r.status).toBe(200);
    expect(ids).toHaveLength(2);
    expect(ids[0]).toBeTruthy();
    expect(ids[1]).toBe(ids[0]);
  });

  it('does not repeat reads or aborted writes', async () => {
    const f = vi.fn(async () => {
      throw new TypeError('Load failed');
    });
    vi.stubGlobal('fetch', f);
    await expect(apiFetch('/api/now')).rejects.toThrow(TypeError);
    const c = new AbortController();
    c.abort();
    await expect(apiFetch('/api/stop', { method: 'POST', signal: c.signal })).rejects.toThrow(
      TypeError
    );
    expect(f).toHaveBeenCalledTimes(2);
  });
});
