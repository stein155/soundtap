// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { afterEach, describe, expect, it, vi } from 'vitest';
import { onPressWait } from '../base';
import { uploadFirmware } from './firmware';

// A stand-in XMLHttpRequest that answers at once with a report.
class FakeXHR {
  upload = { onprogress: null as unknown };
  status = 200;
  responseText = JSON.stringify({
    size: 1,
    modules: [],
    kernel: 'mainline',
    models: [],
    installable: true,
  });
  timeout = 0;
  withCredentials = false;
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onabort: (() => void) | null = null;
  ontimeout: (() => void) | null = null;
  open = vi.fn();
  setRequestHeader = vi.fn();
  send() {
    queueMicrotask(() => this.onload?.());
  }
}

afterEach(() => vi.unstubAllGlobals());

// ADR 0048: every install upload waits for the press on the speaker, signed or not, and the
// page says so while it waits; a check without install needs none.
describe('uploadFirmware', () => {
  const run = async (install: boolean, signature?: string) => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR);
    const seen: boolean[] = [];
    const stop = onPressWait((waiting) => seen.push(waiting));
    const file = new File(['stu'], 'Update.stu');
    await uploadFirmware(file, { install, onProgress: vi.fn(), signature });
    stop();
    return seen.includes(true);
  };

  it('asks for the press for a signed package', async () => {
    expect(await run(true, 'soundtap-package v1 kind=core')).toBe(true);
  });

  it('asks for the press for an unsigned package', async () => {
    expect(await run(true)).toBe(true);
  });

  it('asks for no press for a check', async () => {
    expect(await run(false, 'soundtap-package v1 kind=core')).toBe(false);
  });
});
