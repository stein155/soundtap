// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, afterEach } from 'vitest';
import { subscribeHousehold, subscribeState } from './events';
import { must } from '../test/must';

class FakeEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static all: FakeEventSource[] = [];
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  readyState = FakeEventSource.CONNECTING;
  closed = false;
  listeners: Partial<Record<string, (e: MessageEvent) => void>> = {};
  constructor(public url: string) {
    FakeEventSource.all.push(this);
  }
  addEventListener(type: string, fn: (e: MessageEvent) => void) {
    this.listeners[type] = fn;
  }
  close() {
    this.closed = true;
    this.readyState = FakeEventSource.CLOSED;
  }
}

// The i-th stream the code under test opened.
const stream = (i: number) => must(FakeEventSource.all[i], `stream ${String(i)}`);

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  FakeEventSource.all = [];
});

// A rebooting speaker black-holes the stream instead of closing it; the missing pings
// are how the page notices.
describe('subscribeState watchdog', () => {
  it('drops a stream that stops pinging, reports it and opens a new one', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeEventSource);
    const onError = vi.fn();
    const stop = subscribeState({ onError });
    const first = stream(0);
    first.onopen?.();
    vi.advanceTimersByTime(10000);
    first.listeners['ping']?.(new MessageEvent('ping'));
    vi.advanceTimersByTime(10000);
    expect(onError).not.toHaveBeenCalled();
    vi.advanceTimersByTime(6000);
    expect(first.closed).toBe(true);
    expect(onError).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(3000);
    expect(FakeEventSource.all).toHaveLength(2);
    stop();
    expect(stream(1).closed).toBe(true);
  });
});

// A non-200 answer (502 while a peer reboots, 503 with no subscriber slot left, 404) makes
// the browser close the stream and never retry it; only a dropped connection is retried.
describe('subscribeState after the speaker answers an error', () => {
  it('opens a new stream when the browser closed this one', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeEventSource);
    const onError = vi.fn();
    const stop = subscribeState({ onError });
    const first = stream(0);
    first.readyState = FakeEventSource.CLOSED;
    first.onerror?.();
    expect(onError).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(2999);
    expect(FakeEventSource.all).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.all).toHaveLength(2);
    stop();
  });

  it('leaves a dropped connection to the browser', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeEventSource);
    const stop = subscribeState({ onError: vi.fn() });
    const first = stream(0);
    first.onerror?.(); // readyState CONNECTING: the browser retries it itself
    vi.advanceTimersByTime(5000);
    expect(FakeEventSource.all).toHaveLength(1);
    stop();
  });

  it('opens one stream, not two, when the watchdog was armed', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeEventSource);
    const stop = subscribeState({ onError: vi.fn() });
    const first = stream(0);
    first.onopen?.();
    first.readyState = FakeEventSource.CLOSED;
    first.onerror?.();
    vi.advanceTimersByTime(20000);
    expect(FakeEventSource.all).toHaveLength(2);
    stop();
  });

  it('does not reopen after it was stopped', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeEventSource);
    const stop = subscribeState({ onError: vi.fn() });
    const first = stream(0);
    first.readyState = FakeEventSource.CLOSED;
    first.onerror?.();
    stop();
    vi.advanceTimersByTime(10000);
    expect(FakeEventSource.all).toHaveLength(1);
  });

  it('ignores the error of a stream that was already replaced', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeEventSource);
    const onError = vi.fn();
    const stop = subscribeHousehold({ onState: vi.fn(), onError });
    const first = stream(0);
    first.readyState = FakeEventSource.CLOSED;
    first.onerror?.();
    vi.advanceTimersByTime(3000);
    expect(FakeEventSource.all).toHaveLength(2);
    first.onerror?.(); // late, from the stream before
    vi.advanceTimersByTime(10000);
    expect(FakeEventSource.all).toHaveLength(2);
    expect(onError).toHaveBeenCalledTimes(1);
    stop();
  });
});

// The household stream pings too; without the watchdog a silent stream kept polling off.
describe('subscribeHousehold watchdog', () => {
  it('reopens a silent household stream', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeEventSource);
    const onError = vi.fn();
    const stop = subscribeHousehold({ onState: vi.fn(), onError });
    const first = stream(0);
    first.onopen?.();
    vi.advanceTimersByTime(16000);
    expect(first.closed).toBe(true);
    expect(onError).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(3000);
    expect(FakeEventSource.all).toHaveLength(2);
    stop();
    vi.advanceTimersByTime(30000);
    expect(FakeEventSource.all).toHaveLength(2);
    expect(stream(1).closed).toBe(true);
  });
});

// Closing a stream does not unqueue an event already on its way: one for the room before
// must not reach the room now shown.
describe('subscribeState after close', () => {
  it('drops an event that arrives after its stream was closed or replaced', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeEventSource);
    const onState = vi.fn();
    const stop = subscribeState({ onState });
    const first = stream(0);
    first.onopen?.();
    vi.advanceTimersByTime(26000); // watchdog replaces the silent stream
    first.listeners['state']?.(new MessageEvent('state', { data: '{"volume":5}' }));
    expect(onState).not.toHaveBeenCalled();
    const second = stream(1);
    second.listeners['state']?.(new MessageEvent('state', { data: '{"volume":7}' }));
    expect(onState).toHaveBeenCalledTimes(1);
    stop();
    second.listeners['state']?.(new MessageEvent('state', { data: '{"volume":9}' }));
    expect(onState).toHaveBeenCalledTimes(1);
  });
});

// An error or open queued from the replaced stream must not mark the new one down.
describe('subscribeState callbacks from a replaced stream', () => {
  it('ignores onerror and onopen of the stream before', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeEventSource);
    const onError = vi.fn();
    const onOpen = vi.fn();
    const stop = subscribeState({ onError, onOpen });
    const first = stream(0);
    first.onopen?.();
    vi.advanceTimersByTime(26000); // watchdog replaces the silent stream
    expect(onError).toHaveBeenCalledTimes(1);
    stream(1).onopen?.();
    first.onerror?.();
    first.onopen?.();
    expect(onError).toHaveBeenCalledTimes(1);
    expect(onOpen).toHaveBeenCalledTimes(2);
    stop();
  });
});
