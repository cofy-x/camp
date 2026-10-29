import { randomUUID } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { createRoom, mutate, signIn, workspace } from './team-helpers';

type ReadBarrier = {
  held: string[];
  signals: AbortSignal[];
  released: number;
  superseded: number;
  phase: 'holding' | 'hidden' | 'restored';
  messagesWhileHidden: number;
  release: () => void;
};

test('typing at the first editable moment survives account draft restoration', async ({
  page,
}, info) => {
  await signIn(page);
  const room = await createRoom(
    page,
    `first-input-${info.project.name}-${randomUUID()}`,
  );
  const text = 'Keep input typed as soon as this conversation is ready.';
  await page.addInitScript(
    ({ label, text }) => {
      const observer = new MutationObserver(() => {
        const input = [...document.querySelectorAll('textarea')].find(
          (node) => node.getAttribute('aria-label') === label,
        );
        if (!input || input.disabled) return;
        observer.disconnect();
        // Inject the normal input event at the enable boundary, before any later
        // passive restoration effect can overwrite the newly editable field.
        Object.getOwnPropertyDescriptor(
          HTMLTextAreaElement.prototype,
          'value',
        )!.set!.call(input, text);
        input.dispatchEvent(new Event('input', { bubbles: true }));
      });
      observer.observe(document, {
        childList: true,
        subtree: true,
        attributes: true,
        attributeFilter: ['disabled'],
      });
    },
    { label: `Message ${room.name}`, text },
  );
  const saved = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/me/drafts/conversation') &&
      response.request().method() === 'PUT' &&
      response.request().postDataJSON().value.draft === text,
  );
  await page.reload();
  const input = workspace(page).getByRole('textbox', {
    name: `Message ${room.name}`,
    exact: true,
  });
  expect((await saved).ok()).toBeTruthy();
  await expect(input).toHaveValue(text);
});

test('page hide aborts workspace reads and a restored page resumes without losing its draft', async ({
  page,
}, testInfo) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await signIn(page);
  const room = await createRoom(
    page,
    `page-lifecycle-${testInfo.project.name}-${randomUUID()}`,
  );
  const input = workspace(page).getByRole('textbox', {
    name: `Message ${room.name}`,
    exact: true,
  });
  const draft = 'Keep my unfinished instructions when this page is restored.';
  await input.fill(draft);

  // Supersede the first read pair before either body can be buffered, as an SSE
  // refresh can do while the barrier is being installed.
  const readPaths = new Set(['/api/rooms', `/api/rooms/${room.id}/runs`]);
  const deferred = new Set<string>();
  let releaseNetwork = () => {};
  const networkGate = new Promise<void>((resolve) => {
    releaseNetwork = resolve;
  });
  await page.route(
    (url) => readPaths.has(url.pathname),
    async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (!deferred.has(path)) {
        deferred.add(path);
        await networkGate;
      }
      await route.continue();
    },
  );

  // Buffer real authenticated reads, then release their already-read responses
  // after pagehide. This exercises a resolved read continuation even when fetch
  // itself has finished before the controller is aborted.
  await page.evaluate((roomId) => {
    const scoped = window as typeof window & { campReadBarrier: ReadBarrier };
    const original = window.fetch.bind(window);
    const targets = new Set(['/api/rooms', `/api/rooms/${roomId}/runs`]);
    const releases = new Set<() => void>();
    const barrier: ReadBarrier = {
      held: [],
      signals: [],
      released: 0,
      superseded: 0,
      phase: 'holding',
      messagesWhileHidden: 0,
      release: () => {
        releases.forEach((release) => release());
        releases.clear();
      },
    };
    scoped.campReadBarrier = barrier;
    window.fetch = async (input, init) => {
      const url = new URL(
        input instanceof Request ? input.url : String(input),
        location.href,
      );
      if (
        barrier.phase === 'hidden' &&
        url.pathname === `/api/rooms/${roomId}/messages`
      )
        barrier.messagesWhileHidden++;
      const signal = init?.signal;
      if (!signal || !targets.delete(url.pathname))
        return original(input, init);
      let superseded = false;
      let resume: (() => void) | undefined;
      const rearm = () => {
        if (barrier.phase !== 'holding') return;
        superseded = true;
        barrier.superseded++;
        targets.add(url.pathname);
        const index = barrier.held.indexOf(url.pathname);
        if (index >= 0) {
          barrier.held.splice(index, 1);
          barrier.signals.splice(index, 1);
        }
        resume?.();
      };
      signal.addEventListener('abort', rearm, { once: true });
      try {
        const response = await original(input, init);
        const text = await response.text();
        if (!superseded) {
          await new Promise<void>((resolve) => {
            resume = resolve;
            releases.add(resolve);
            barrier.held.push(url.pathname);
            barrier.signals.push(signal);
          });
          if (!superseded) barrier.released++;
        }
        return new Response(text, {
          status: response.status,
          headers: response.headers,
        });
      } finally {
        signal.removeEventListener('abort', rearm);
        if (resume) releases.delete(resume);
      }
    };
  }, room.id);
  const refresh = async () => {
    await workspace(page)
      .getByRole('button', { name: 'Room actions', exact: true })
      .click();
    await page.getByRole('menuitem', { name: 'Refresh', exact: true }).click();
  };
  const supersededReads = () =>
    page.evaluate(
      () =>
        (window as typeof window & { campReadBarrier: ReadBarrier })
          .campReadBarrier.superseded,
    );
  const heldReads = () =>
    page.evaluate(
      () =>
        (window as typeof window & { campReadBarrier: ReadBarrier })
          .campReadBarrier.held.length,
    );
  try {
    await refresh();
    await expect.poll(() => deferred.size).toBe(2);
    await refresh();
    await expect.poll(supersededReads).toBeGreaterThanOrEqual(2);
  } finally {
    releaseNetwork();
  }
  await expect.poll(heldReads).toBe(2);
  // A refresh can also replace reads whose response bodies are already held.
  const cancelledBefore = await supersededReads();
  await refresh();
  await expect
    .poll(supersededReads)
    .toBeGreaterThanOrEqual(cancelledBefore + 2);
  // Observe the live pair and dispatch pagehide in the same browser task, so a
  // queued refresh cannot replace it between a poll and a later evaluate call.
  await expect
    .poll(() =>
      page.evaluate(() => {
        const barrier = (
          window as typeof window & { campReadBarrier: ReadBarrier }
        ).campReadBarrier;
        if (
          barrier.phase !== 'holding' ||
          barrier.signals.length !== 2 ||
          barrier.signals.some((signal) => signal.aborted)
        )
          return null;
        const before = barrier.signals.map((signal) => signal.aborted);
        barrier.phase = 'hidden';
        window.dispatchEvent(
          new PageTransitionEvent('pagehide', { persisted: true }),
        );
        const after = barrier.signals.map((signal) => signal.aborted);
        barrier.release();
        return { before, after };
      }),
    )
    .toEqual({ before: [false, false], after: [true, true] });
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as typeof window & { campReadBarrier: ReadBarrier })
            .campReadBarrier.released,
      ),
    )
    .toBe(2);

  const content = 'Discussion posted while the page was away.';
  const response = await mutate(page, `/rooms/${room.id}/messages`, {
    requestId: randomUUID(),
    content,
    postOnly: true,
  });
  expect(response.ok()).toBeTruthy();
  expect(
    await page.evaluate(
      () =>
        (window as typeof window & { campReadBarrier: ReadBarrier })
          .campReadBarrier.messagesWhileHidden,
    ),
  ).toBe(0);
  await expect(input).toHaveValue(draft);

  await page.evaluate(() => {
    const barrier = (window as typeof window & { campReadBarrier: ReadBarrier })
      .campReadBarrier;
    barrier.phase = 'restored';
    window.dispatchEvent(
      new PageTransitionEvent('pageshow', { persisted: true }),
    );
  });
  await expect(
    workspace(page).getByText(content, { exact: true }),
  ).toBeVisible();
  await expect(input).toHaveValue(draft);
  expect(errors).toEqual([]);
});
