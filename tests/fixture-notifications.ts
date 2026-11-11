import type { Page } from '@playwright/test';

// Synthetic execution fixtures do not write Runs to the real Server. Deliver
// their explicit changes through the actual browser notification listeners;
// transport replay and authority are qualified separately by Server tests.
export async function fixtureNotifications(page: Page) {
  await page.addInitScript(() => {
    const NativeEventSource = window.EventSource;
    const state = { source: null as EventSource | null, cursor: 0 };
    (
      window as typeof window & { campFixtureNotifications: typeof state }
    ).campFixtureNotifications = state;
    window.EventSource = class extends NativeEventSource {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options);
        const target = new URL(url, location.href);
        if (target.pathname !== '/api/workspace/events') return;
        state.source = this;
        state.cursor = Number(target.searchParams.get('after') || 0);
        this.addEventListener('workspace', (message: MessageEvent<string>) => {
          state.cursor = Math.max(state.cursor, JSON.parse(message.data).id);
        });
      }
    };
  });
  return async (type: string, roomId: string, entityId: string) =>
    page.evaluate(
      ({ type, roomId, entityId }) => {
        const state = (
          window as typeof window & {
            campFixtureNotifications: {
              source: EventSource | null;
              cursor: number;
            };
          }
        ).campFixtureNotifications;
        if (!state.source)
          throw new Error('The fixture notification stream has not opened.');
        state.source.dispatchEvent(
          new MessageEvent('workspace', {
            data: JSON.stringify({
              id: ++state.cursor,
              type,
              roomId,
              entityId,
              createdAt: new Date().toISOString(),
            }),
          }),
        );
      },
      { type, roomId, entityId },
    );
}
