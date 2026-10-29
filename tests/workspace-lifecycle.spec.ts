import { randomUUID } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { createRoom, mutate, signIn, workspace } from './team-helpers';

type ReadBarrier = {
  held: string[];
  signals: AbortSignal[];
  released: number;
  phase: 'holding' | 'hidden' | 'restored';
  messagesWhileHidden: number;
  release: () => void;
};

test('typing at the first editable moment survives account draft restoration', async ({
  page,
}, info) => {
  await signIn(page);
  const room = await createRoom(page, `first-input-${info.project.name}`);
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
    `page-lifecycle-${testInfo.project.name}`,
  );
  const input = workspace(page).getByRole('textbox', {
    name: `Message ${room.name}`,
    exact: true,
  });
  const draft = 'Keep my unfinished instructions when this page is restored.';
  await input.fill(draft);

  // Buffer real authenticated reads, then release their already-read responses
  // after pagehide. This exercises a resolved read continuation even when fetch
  // itself has finished before the controller is aborted.
  await page.evaluate((roomId) => {
    const scoped = window as typeof window & { campReadBarrier: ReadBarrier };
    const original = window.fetch.bind(window);
    const targets = new Set(['/api/rooms', `/api/rooms/${roomId}/runs`]);
    const releases: (() => void)[] = [];
    const barrier: ReadBarrier = {
      held: [],
      signals: [],
      released: 0,
      phase: 'holding',
      messagesWhileHidden: 0,
      release: () => releases.splice(0).forEach((release) => release()),
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
      if (!targets.delete(url.pathname)) return original(input, init);
      const response = await original(input, init);
      const text = await response.text();
      await new Promise<void>((resolve) => {
        releases.push(resolve);
        barrier.held.push(url.pathname);
        if (init?.signal) barrier.signals.push(init.signal);
      });
      barrier.released++;
      return new Response(text, {
        status: response.status,
        headers: response.headers,
      });
    };
  }, room.id);
  await workspace(page)
    .getByRole('button', { name: 'Room actions', exact: true })
    .click();
  await page.getByRole('menuitem', { name: 'Refresh', exact: true }).click();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as typeof window & { campReadBarrier: ReadBarrier })
            .campReadBarrier.held.length,
      ),
    )
    .toBe(2);
  const aborted = await page.evaluate(() => {
    const barrier = (window as typeof window & { campReadBarrier: ReadBarrier })
      .campReadBarrier;
    barrier.phase = 'hidden';
    window.dispatchEvent(
      new PageTransitionEvent('pagehide', { persisted: true }),
    );
    const aborted = barrier.signals.map((signal) => signal.aborted);
    barrier.release();
    return aborted;
  });
  expect(aborted).toEqual([true, true]);
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
