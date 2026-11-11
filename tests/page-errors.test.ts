import { EventEmitter } from 'node:events';
import type { Page } from '@playwright/test';
import { describe, expect, it } from 'vitest';
import { isNavigationCancellation, watchPageErrors } from './page-errors';

const origin = 'http://127.0.0.1:13009';

function diagnostic(url = `${origin}/api/me/room-reads/room`) {
  const error = new Error();
  error.name = 'Fetch API cannot load http';
  error.message = url.slice('http:'.length) + ' due to access control checks.';
  error.stack = `Fetch API cannot load ${url} due to access control checks.`;
  return error;
}

function streamDiagnostic(url = `${origin}/api/workspace/events?after=325`) {
  const error = new Error();
  const protocol = new URL(url).protocol.slice(0, -1);
  error.name = `EventSource cannot load ${protocol}`;
  error.message =
    url.slice(`${protocol}:/`.length) + ' due to access control checks.';
  error.stack = '';
  return error;
}

describe('browser page error guard', () => {
  it('recognizes only the native WebKit same-origin API navigation diagnostic', () => {
    const native = diagnostic();
    expect(isNavigationCancellation(native, 'webkit', origin)).toBe(true);
    for (const browser of ['chromium', 'firefox'])
      expect(isNavigationCancellation(native, browser, origin)).toBe(false);
    expect(isNavigationCancellation(native, 'webkit', null)).toBe(false);
    expect(
      isNavigationCancellation(
        diagnostic('http://other.test/api/me'),
        'webkit',
        origin,
      ),
    ).toBe(false);
    expect(
      isNavigationCancellation(
        diagnostic(`${origin}/assets/app.js`),
        'webkit',
        origin,
      ),
    ).toBe(false);
    expect(
      isNavigationCancellation(new Error(native.message), 'webkit', origin),
    ).toBe(false);
    expect(
      isNavigationCancellation(new TypeError('Load failed'), 'webkit', origin),
    ).toBe(false);
    expect(
      isNavigationCancellation(diagnostic('http://['), 'webkit', origin),
    ).toBe(false);
  });

  it('recognizes the empty-stack native EventSource diagnostic only for the departing workspace stream', () => {
    const native = streamDiagnostic();
    expect(isNavigationCancellation(native, 'webkit', origin)).toBe(true);
    for (const browser of ['chromium', 'firefox'])
      expect(isNavigationCancellation(native, browser, origin)).toBe(false);
    expect(isNavigationCancellation(native, 'webkit', null)).toBe(false);
    expect(
      isNavigationCancellation(
        streamDiagnostic('http://other.test/api/workspace/events'),
        'webkit',
        origin,
      ),
    ).toBe(false);
    expect(
      isNavigationCancellation(
        streamDiagnostic(`${origin}/api/other-events`),
        'webkit',
        origin,
      ),
    ).toBe(false);
    const plain = new Error();
    plain.stack = '';
    expect(isNavigationCancellation(plain, 'webkit', origin)).toBe(false);
    native.name = 'Another EventSource error';
    expect(isNavigationCancellation(native, 'webkit', origin)).toBe(false);
  });

  it('keeps empty-stack errors readable and fails stream diagnostics outside the departing document', async () => {
    const frame = {};
    const events = new EventEmitter();
    const page = Object.assign(events, {
      mainFrame: () => frame,
      url: () => origin,
      reload: async () => {
        events.emit('pageerror', streamDiagnostic());
        const ordinary = new Error('Application failed during navigation');
        ordinary.stack = '';
        events.emit('pageerror', ordinary);
        events.emit('framenavigated', frame);
        events.emit('pageerror', streamDiagnostic());
      },
    }) as unknown as Page;
    const guard = watchPageErrors(page, 'webkit');
    events.emit('pageerror', streamDiagnostic());
    await guard.reload();
    expect(guard.navigationCancellations).toHaveLength(1);
    expect(guard.errors).toHaveLength(3);
    expect(guard.errors[0]).toContain('EventSource cannot load http');
    expect(guard.errors[1]).toBe('Error: Application failed during navigation');
    expect(guard.errors[2]).toContain('EventSource cannot load http');
    expect(guard.errors).not.toContain('');
  });

  it('retains ordinary errors during navigation and every error after document commit', async () => {
    const frame = {};
    const events = new EventEmitter();
    const page = Object.assign(events, {
      mainFrame: () => frame,
      url: () => origin,
      reload: async () => {
        events.emit('pageerror', diagnostic());
        events.emit(
          'pageerror',
          new Error('Application failed during navigation'),
        );
        events.emit('framenavigated', {}); // A subframe cannot end the guard.
        events.emit('pageerror', diagnostic());
        events.emit('framenavigated', frame);
        events.emit('pageerror', diagnostic());
      },
    }) as unknown as Page;
    const guard = watchPageErrors(page, 'webkit');
    events.emit('pageerror', diagnostic());
    await guard.reload();
    events.emit('pageerror', diagnostic());
    expect(guard.navigationCancellations).toHaveLength(2);
    expect(guard.errors).toHaveLength(4);
    expect(guard.errors[1]).toContain('Application failed during navigation');
  });

  it('ends the navigation exception when reload fails', async () => {
    const events = new EventEmitter();
    const page = Object.assign(events, {
      url: () => origin,
      reload: async () => {
        throw new Error('Navigation failed');
      },
    }) as unknown as Page;
    const guard = watchPageErrors(page, 'webkit');
    await expect(guard.reload()).rejects.toThrow('Navigation failed');
    events.emit('pageerror', diagnostic());
    expect(guard.errors).toHaveLength(1);
    expect(guard.navigationCancellations).toEqual([]);
  });
});
