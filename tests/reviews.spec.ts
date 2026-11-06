import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import {
  openRoomMore,
  createRoom,
  library,
  mutate,
  newDocument,
  openApp,
  openInboxActivity,
  signIn,
  workspace,
  work,
} from './team-helpers';
import { watchPageErrors } from './page-errors';
import type { ReviewActionInput } from '../apps/dashboard/src/features/reviews/api';

interface Review {
  id: string;
  roomId: string;
  resourceId: string;
  title: string;
  reviewerUserId: string;
  submittedCommit: string;
  deliveredCommit: string;
  status: string;
  revision: number;
  threadRootId: string;
  outdated: boolean;
}

const detail = (page: Page) =>
  workspace(page).getByRole('region', { name: 'Document review', exact: true });

async function readReview(page: Page, id: string): Promise<Review> {
  const response = await page.request.get(`/api/reviews/${id}`);
  expect(response.ok()).toBeTruthy();
  return (await response.json()).review as Review;
}

async function openReview(page: Page, title: string) {
  await openApp(page, 'Workspace');
  const back = detail(page).getByRole('button', {
    name: 'Back to Reviews',
    exact: true,
  });
  if (await back.isVisible()) await back.click();
  else if (
    !(await workspace(page)
      .getByRole('region', { name: 'Document reviews', exact: true })
      .isVisible())
  )
    await openRoomMore(page, 'File reviews…');
  await workspace(page)
    .getByRole('region', { name: 'Document reviews', exact: true })
    .getByRole('button')
    .filter({ hasText: title })
    .click();
  await expect(detail(page)).toBeVisible();
}

async function publish(page: Page) {
  await library(page)
    .getByRole('button', { name: 'Publish Changes', exact: true })
    .click();
  await expect(
    library(page).getByText('Published to shared files.', { exact: true }),
  ).toBeVisible();
  await library(page)
    .getByRole('button', { name: 'Back to Preview', exact: true })
    .click();
}

async function saveReviewFile(page: Page, resourceId: string) {
  const initial = await (
    await page.request.get(`/api/resources/${resourceId}/files`)
  ).json();
  const saved = await mutate(
    page,
    `/resources/${resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: initial.commit,
      path: 'review.md',
      content: '# Saved proposal\nThe owner and the next step are explicit.',
    },
    'PUT',
  );
  expect(saved.ok()).toBe(true);
  return (
    await (await page.request.get(`/api/resources/${resourceId}/files`)).json()
  ).commit as string;
}

async function reviewFixture(page: Page, name: string) {
  const room = await createRoom(page, `${name}-${randomUUID().slice(0, 8)}`);
  const commit = await saveReviewFile(page, room.resourceId);
  const { user } = await (await page.request.get('/api/auth/status')).json();
  const title = `${name} decision`;
  const requested = await mutate(page, `/rooms/${room.id}/reviews`, {
    requestId: randomUUID(),
    title,
    resourceId: room.resourceId,
    path: 'review.md',
    commit,
    reviewerUserId: user.id,
  });
  expect(requested.ok()).toBe(true);
  return { room, title, review: (await requested.json()).review as Review };
}

async function chooseRoom(page: Page, room: { id: string; name: string }) {
  await openApp(page, 'Workspace');
  const chooser = workspace(page).getByRole('combobox', {
    name: 'Room',
    exact: true,
  });
  if (await chooser.isVisible()) await chooser.selectOption(room.id);
  else
    await workspace(page)
      .getByRole('navigation', { name: 'Rooms', exact: true })
      .getByRole('button')
      .filter({ has: page.getByText(room.name, { exact: true }) })
      .click();
}

test('an uncertain review decision freezes its original body and recovers after a newer status arrives', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const value = await reviewFixture(page, 'Recover decision');
  await openReview(page, value.title);
  const comment = detail(page).getByLabel('Review comment', { exact: true });
  await comment.fill('The saved proposal is ready.');
  const bodies: ReviewActionInput[] = [];
  let drop = true;
  await page.route(
    `**/api/reviews/${value.review.id}/actions`,
    async (route) => {
      if (route.request().method() !== 'POST') return route.fallback();
      bodies.push(route.request().postDataJSON());
      if (!drop) return route.fallback();
      drop = false;
      expect((await route.fetch()).ok()).toBe(true);
      return route.abort('failed');
    },
  );
  await detail(page)
    .getByRole('button', { name: 'Approve', exact: true })
    .click();
  await expect(comment).toBeDisabled();
  await expect(
    detail(page).getByRole('button', { name: 'Retry Decision', exact: true }),
  ).toBeEnabled();
  await expect
    .poll(async () => (await readReview(page, value.review.id)).status)
    .toBe('approved');
  const refresh = detail(page).getByRole('button', {
    name: 'Refresh Review',
    exact: true,
  });
  await expect(refresh).toBeVisible();
  let failRead = true;
  await page.route(`**/api/reviews/${value.review.id}`, (route) => {
    if (!failRead) return route.fallback();
    return route.fulfill({
      status: 503,
      json: { error: 'This review could not be refreshed.' },
    });
  });
  expect(
    (
      await mutate(page, `/rooms/${value.room.id}/messages`, {
        requestId: randomUUID(),
        content: 'An unrelated update must preserve review recovery.',
        postOnly: true,
      })
    ).ok(),
  ).toBe(true);
  const retryRead = detail(page).getByRole('button', {
    name: 'Retry Review',
    exact: true,
  });
  await expect(retryRead).toBeVisible();
  await expect(refresh).toBeVisible();
  await expect(comment).toHaveValue('The saved proposal is ready.');
  await expect(comment).toBeDisabled();
  await expect(
    detail(page).getByRole('button', { name: 'Retry Decision', exact: true }),
  ).toBeEnabled();
  failRead = false;
  await retryRead.click();
  await expect(retryRead).toHaveCount(0);
  await expect(refresh).toBeVisible();
  await expect(comment).toHaveValue('The saved proposal is ready.');
  await detail(page)
    .getByRole('button', { name: 'Refresh Review', exact: true })
    .click();
  await expect(detail(page).locator('.camp-review-state')).toHaveText(
    'Approved',
  );
  await expect(
    detail(page).getByRole('button', { name: 'Deliver', exact: true }),
  ).toBeDisabled();
  await detail(page)
    .getByRole('button', { name: 'Retry Decision', exact: true })
    .click();
  await expect(
    detail(page).getByRole('button', { name: 'Deliver', exact: true }),
  ).toBeEnabled();
  expect(bodies).toHaveLength(2);
  expect(bodies[1]).toEqual(bodies[0]);
  const result = await (
    await page.request.get(`/api/reviews/${value.review.id}`)
  ).json();
  expect(
    result.events.filter(
      (event: { action: string }) => event.action === 'approve',
    ),
  ).toHaveLength(1);
  await expect(
    detail(page).getByRole('button', { name: 'Retry Decision', exact: true }),
  ).toHaveCount(0);
  expect(errors.errors).toEqual([]);
});

test('a different Library source shows its own review loading state instead of the previous source’s rows', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const source = await reviewFixture(page, 'Review source');
  const destination = await createRoom(
    page,
    `Empty review source-${randomUUID().slice(0, 8)}`,
  );
  await saveReviewFile(page, destination.resourceId);
  await chooseRoom(page, source.room);
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  await library(page)
    .getByRole('tab', { name: 'Reviews & Delivery', exact: true })
    .click();
  await expect(
    library(page).getByRole('button').filter({ hasText: source.title }),
  ).toBeVisible();
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/reviews?*', async (route) => {
    if (
      new URL(route.request().url()).searchParams.get('resourceId') ===
      destination.resourceId
    )
      await held;
    return route.fallback();
  });
  try {
    const sources = library(page).getByRole('complementary', {
      name: 'Library resources',
      exact: true,
    });
    if (!(await sources.isVisible()))
      await library(page)
        .getByRole('button', { name: 'Sources', exact: true })
        .click();
    await sources
      .getByRole('button')
      .filter({ hasText: `${destination.name} files` })
      .click();
    await expect(library(page).locator('.p9-window-title')).toHaveText(
      `${destination.name} — Library`,
    );
    await library(page)
      .getByRole('tab', { name: 'Reviews & Delivery', exact: true })
      .click();
    const list = library(page).getByRole('region', {
      name: 'Document reviews',
      exact: true,
    });
    await expect(list.getByRole('status')).toHaveText('Loading reviews…');
    await expect(
      list.getByRole('button').filter({ hasText: source.title }),
    ).toHaveCount(0);
    release();
    await expect(list).toContainText('No reviews yet.');
    expect(errors.errors).toEqual([]);
  } finally {
    release();
    if (!page.isClosed())
      await page.unrouteAll({ behavior: 'wait' }).catch((error: unknown) => {
        if (!page.isClosed()) throw error;
      });
  }
});

for (const action of ['Back to Reviews', 'Refresh Review']) {
  test(`a delayed ${action} cannot replace a newer document navigation`, async ({
    page,
    browserName,
  }) => {
    const errors = watchPageErrors(page, browserName);
    await signIn(page);
    const value = await reviewFixture(page, `Superseded ${action}`);
    await openReview(page, value.title);
    let release = () => {};
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    let saving = false;
    await page.route('**/api/me/drafts/review', async (route) => {
      if (
        route.request().method() === 'PUT' &&
        route.request().postDataJSON().key === `decision:${value.review.id}`
      ) {
        saving = true;
        await held;
      }
      return route.fallback();
    });
    try {
      const text = 'Preserve this private comment while choosing the document.';
      await detail(page)
        .getByLabel('Review comment', { exact: true })
        .fill(text);
      if (action === 'Refresh Review') {
        expect(
          (
            await mutate(page, `/reviews/${value.review.id}/actions`, {
              requestId: randomUUID(),
              revision: value.review.revision,
              action: 'approve',
            })
          ).ok(),
        ).toBe(true);
        await expect(
          detail(page).getByRole('button', { name: action, exact: true }),
        ).toBeVisible();
      }
      await detail(page)
        .getByRole('button', { name: action, exact: true })
        .click();
      await expect.poll(() => saving).toBe(true);
      await detail(page)
        .getByRole('button', { name: 'Open Document', exact: true })
        .click();
      release();
      await expect(library(page)).toHaveClass(/is-active/);
      await openApp(page, 'Workspace');
      await expect(detail(page)).toBeVisible();
      await expect(
        detail(page).getByLabel('Review comment', { exact: true }),
      ).toHaveValue(text);
      await expect(detail(page).locator('.camp-review-state')).toHaveText(
        'In Review',
      );
      if (action === 'Refresh Review')
        await expect(
          detail(page).getByRole('button', { name: action, exact: true }),
        ).toBeVisible();
      expect(errors.errors).toEqual([]);
    } finally {
      release();
      await page.unrouteAll({ behavior: 'wait' });
    }
  });
}

test('a completed review request remains available without replacing a newer application choice', async ({
  page,
  browserName,
}, info) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Request destination-${randomUUID().slice(0, 8)}`,
  );
  await saveReviewFile(page, room.resourceId);
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  await library(page)
    .getByRole('button', { name: 'Request Review…', exact: true })
    .click();
  const request = library(page).getByRole('form', {
    name: 'Request document review',
    exact: true,
  });
  const title =
    'The recorded request should not change the active application.';
  await request.getByLabel('Title', { exact: true }).fill(title);
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let recorded = false;
  await page.route(`**/api/rooms/${room.id}/reviews`, async (route) => {
    if (route.request().method() !== 'POST') return route.fallback();
    const response = await route.fetch();
    expect(response.ok()).toBe(true);
    recorded = true;
    await held;
    return route.fulfill({ response });
  });
  try {
    await request
      .getByRole('button', { name: 'Request Review', exact: true })
      .click();
    await expect.poll(() => recorded).toBe(true);
    await openApp(page, 'Work');
    release();
    await expect(work(page)).toHaveClass(/is-active/);
    await openApp(page, 'Library');
    await expect(
      request.getByRole('button', { name: 'Open Review', exact: true }),
    ).toBeVisible();
    await page.screenshot({
      path: info.outputPath('review-request-after-newer-application.png'),
    });
    await request
      .getByRole('button', { name: 'Open Review', exact: true })
      .click();
    await expect(detail(page)).toContainText(title);
    const result = await (
      await page.request.get(`/api/reviews?roomId=${room.id}`)
    ).json();
    expect(result.reviews).toHaveLength(1);
    expect(errors.errors).toEqual([]);
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('a recorded review decision updates in the background without taking focus from another application', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const value = await reviewFixture(page, 'Decision focus');
  await openReview(page, value.title);
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let recorded = false;
  await page.route(
    `**/api/reviews/${value.review.id}/actions`,
    async (route) => {
      const response = await route.fetch();
      expect(response.ok()).toBe(true);
      recorded = true;
      await held;
      return route.fulfill({ response });
    },
  );
  try {
    await detail(page)
      .getByRole('button', { name: 'Approve', exact: true })
      .click();
    await expect.poll(() => recorded).toBe(true);
    await openApp(page, 'Work');
    release();
    await expect(workspace(page).locator('.camp-review-state')).toHaveText(
      'Approved',
    );
    await expect(work(page)).toHaveClass(/is-active/);
    expect(
      await workspace(page)
        .locator('.camp-review-content > .camp-section-heading > h2')
        .evaluate((element) => element === document.activeElement),
    ).toBe(false);
    expect(errors.errors).toEqual([]);
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('two people review, revise and deliver an exact document version', async ({
  page,
  browser,
  baseURL,
  isMobile,
  browserName,
}, testInfo) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  const navigation = watchPageErrors(page, browserName, errors);
  await signIn(page);
  const invite = await mutate(page, '/team/invites', { expiresInHours: 24 });
  expect(invite.ok()).toBeTruthy();
  const token = (await invite.json()).token as string;
  const reviewerContext = await browser.newContext({
    baseURL,
    viewport: page.viewportSize() ?? undefined,
    ...(isMobile
      ? { hasTouch: true, isMobile: true, deviceScaleFactor: 2 }
      : {}),
  });
  const reviewer = await reviewerContext.newPage();
  watchPageErrors(reviewer, browserName, errors);
  try {
    await reviewer.goto(`/#invite=${encodeURIComponent(token)}`);
    await reviewer.getByLabel('Your name', { exact: true }).fill('Morgan');
    const reviewerUsername = `reviewer-${testInfo.project.name}-${randomUUID().slice(0, 6)}`;
    await reviewer
      .getByLabel('Username', { exact: true })
      .fill(reviewerUsername);
    await reviewer
      .getByLabel(/^Password/)
      .fill('A private document review password');
    await reviewer
      .getByRole('button', { name: 'Join Team', exact: true })
      .click();
    await expect(workspace(reviewer)).toBeVisible();
    const reviewerUser = (
      await (await reviewer.request.get('/api/auth/status')).json()
    ).user as { id: string };
    await workspace(reviewer)
      .getByRole('button', { name: /^Close .*Workspace$/ })
      .click();
    await expect(workspace(reviewer)).toHaveCount(0);

    const room = await createRoom(page, `review-flow-${testInfo.project.name}`);
    const revisionDraft = 'Keep this existing revision draft.';
    await workspace(page)
      .getByLabel(`Message ${room.name}`, { exact: true })
      .fill(revisionDraft);
    await workspace(page)
      .getByRole('button', { name: 'Files', exact: true })
      .click();
    const first = '# Launch proposal\nPilot a weekly client update.';
    const revised = `${first}\n\nOwner: Morgan. Success: one clear next step per client.`;
    await newDocument(page, 'proposal.md', first);
    await publish(page);
    const submitted = await (
      await page.request.get(`/api/resources/${room.resourceId}/files`)
    ).json();
    await library(page)
      .getByRole('button', { name: 'Request Review…', exact: true })
      .click();
    const request = library(page).getByRole('form', {
      name: 'Request document review',
      exact: true,
    });
    const title = `Client launch review ${testInfo.project.name}`;
    await request.getByLabel('Title', { exact: true }).fill(title);
    await request
      .getByLabel('Reviewer', { exact: true })
      .selectOption(reviewerUser.id);
    await request
      .getByRole('button', { name: 'Request Review', exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (
            await (
              await page.request.get(`/api/reviews?roomId=${room.id}`)
            ).json()
          ).reviews.length,
      )
      .toBe(1);
    const reviews = await (
      await page.request.get(`/api/reviews?roomId=${room.id}`)
    ).json();
    expect(reviews.reviews).toHaveLength(1);
    const review = reviews.reviews[0] as Review;
    expect(review.submittedCommit).toBe(submitted.commit);
    expect(review.threadRootId).not.toBe('');

    await openInboxActivity(reviewer);
    await reviewer
      .getByRole('complementary', { name: 'Personal inbox' })
      .getByRole('button', { name: new RegExp(title) })
      .click();
    await expect(workspace(reviewer)).toBeVisible();
    await expect(detail(reviewer)).toContainText(title);
    await expect(
      workspace(reviewer).getByRole('region', { name: 'Room Runner' }),
    ).toHaveCount(0);
    await expect(workspace(reviewer).getByLabel('Room members')).toHaveCount(0);
    await expect(
      workspace(reviewer).locator('.workspace-unavailable'),
    ).toHaveCount(0);
    expect(
      await detail(reviewer).evaluate((panel) => {
        const document = panel.querySelector('.camp-reviewed-document');
        const actions = panel.querySelector('.camp-review-actions');
        return Boolean(
          document &&
          actions &&
          actions.compareDocumentPosition(document) &
            Node.DOCUMENT_POSITION_FOLLOWING,
        );
      }),
    ).toBeTruthy();
    await expect(
      detail(reviewer).getByRole('button', { name: 'Approve', exact: true }),
    ).toBeInViewport({ ratio: 1 });
    await expect(
      detail(reviewer).getByRole('button', {
        name: 'Request Changes',
        exact: true,
      }),
    ).toBeInViewport({ ratio: 1 });
    await expect(
      detail(reviewer).getByRole('button', {
        name: 'Open Document',
        exact: true,
      }),
    ).toBeInViewport({ ratio: 1 });
    await expect(
      detail(reviewer).locator('.camp-reviewed-document'),
    ).toBeVisible();
    await detail(reviewer)
      .getByRole('button', { name: 'Open Document', exact: true })
      .click();
    await expect(library(reviewer)).toBeVisible();
    await expect(library(reviewer)).toContainText(
      'Pilot a weekly client update.',
    );
    await expect(library(reviewer)).toContainText('Saved version · read only');
    await openReview(reviewer, title);
    let announceSave!: () => void;
    let releaseSave!: () => void;
    let finishSave!: () => void;
    const heldSave = new Promise<void>((resolve) => {
      announceSave = resolve;
    });
    const releasedSave = new Promise<void>((resolve) => {
      releaseSave = resolve;
    });
    const saved = new Promise<void>((resolve) => {
      finishSave = resolve;
    });
    await reviewer.route('**/api/me/drafts/review', async (route) => {
      if (route.request().method() !== 'PUT') return route.continue();
      announceSave();
      await releasedSave;
      await route.continue();
      await route.request().response();
      finishSave();
    });
    await detail(reviewer)
      .getByLabel('Review comment', { exact: true })
      .fill('Name the owner and the expected client outcome.');
    await detail(reviewer)
      .getByRole('button', { name: 'Open Document', exact: true })
      .click();
    await heldSave;
    // The operator selects Workspace while opening the document still waits
    // for a private write. Its response must not replace this newer choice.
    await openApp(reviewer, 'Workspace');
    await expect(workspace(reviewer)).toHaveClass(/is-active/);
    releaseSave();
    await saved;
    await reviewer.unroute('**/api/me/drafts/review');
    await expect
      .poll(
        async () =>
          (
            await (
              await reviewer.request.get(
                `/api/me/drafts/review?key=${encodeURIComponent('decision:' + review.id)}`,
              )
            ).json()
          ).draft.value?.comment,
      )
      .toBe('Name the owner and the expected client outcome.');
    await reviewer.screenshot({
      path: testInfo.outputPath('review-navigation-after-delayed-save.png'),
    });
    await expect(workspace(reviewer)).toHaveClass(/is-active/);
    await openReview(reviewer, title);
    await expect(
      detail(reviewer).getByLabel('Review comment', { exact: true }),
    ).toHaveValue('Name the owner and the expected client outcome.');
    await reviewer.route('**/api/me/drafts/review', async (route) => {
      if (route.request().method() !== 'PUT') return route.continue();
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({
          error: {
            code: 'storage_error',
            message: 'The draft could not be saved.',
          },
        }),
      });
    });
    await detail(reviewer)
      .getByLabel('Review comment', { exact: true })
      .fill(
        'Name the owner and the expected client outcome. Keep the handoff concrete.',
      );
    await expect(detail(reviewer).getByRole('alert')).toBeVisible();
    const mention = await mutate(page, `/rooms/${room.id}/messages`, {
      requestId: randomUUID(),
      threadRootId: review.threadRootId,
      content: `@${reviewerUsername} A quick clarification for this review.`,
    });
    expect(mention.ok()).toBeTruthy();
    const inbox = reviewer.getByRole('complementary', {
      name: 'Personal inbox',
    });
    await openInboxActivity(reviewer);
    await inbox.getByRole('button', { name: /You were mentioned/ }).click();
    await expect(
      inbox.getByRole('button', { name: 'Close inbox' }),
    ).toBeVisible();
    const unread = await (await reviewer.request.get('/api/me/inbox')).json();
    expect(
      unread.items.find((item: { kind: string }) => item.kind === 'mention')
        .readAt,
    ).toBe('');
    await inbox.getByRole('button', { name: 'Close inbox' }).click();
    await reviewer.unrouteAll({ behavior: 'wait' });
    await detail(reviewer)
      .getByRole('button', { name: 'Compare Private Drafts', exact: true })
      .click();
    await detail(reviewer)
      .getByRole('button', { name: 'Save My Draft', exact: true })
      .click();
    await expect(detail(reviewer).getByRole('alert')).toHaveCount(0);
    await openInboxActivity(reviewer);
    await inbox.getByRole('button', { name: /You were mentioned/ }).click();
    await expect(
      inbox.getByRole('button', { name: 'Close inbox' }),
    ).toHaveCount(0);
    await expect
      .poll(
        async () =>
          (
            await (await reviewer.request.get('/api/me/inbox')).json()
          ).items.find((item: { kind: string }) => item.kind === 'mention')
            .readAt,
      )
      .not.toBe('');
    await openReview(reviewer, title);
    await detail(reviewer)
      .getByRole('button', { name: 'Request Changes', exact: true })
      .click();
    await expect
      .poll(async () => (await readReview(page, review.id)).status)
      .toBe('changes_requested');
    await expect(
      detail(reviewer).getByRole('heading', { name: title, exact: true }),
    ).toBeFocused();

    await openReview(page, title);
    await expect(detail(page)).toContainText('Name the owner');
    await expect(
      detail(page).getByRole('button', {
        name: 'Submit New Version',
        exact: true,
      }),
    ).toBeDisabled();
    await expect(
      detail(page).getByRole('button', {
        name: 'Edit Latest File',
        exact: true,
      }),
    ).toBeVisible();
    const feedback = (
      await (await page.request.get(`/api/reviews/${review.id}`)).json()
    ).events.find(
      (event: { action: string }) => event.action === 'request_changes',
    ) as { messageId: string };
    const messagesBeforeHandoff = (
      await (await page.request.get(`/api/rooms/${room.id}/messages`)).json()
    ).messages;
    await detail(page)
      .getByRole('button', { name: 'Ask agent…', exact: true })
      .click();
    const composer = workspace(page).getByRole('form', {
      name: 'Room composer',
      exact: true,
    });
    await expect(composer).toBeVisible();
    await expect(
      workspace(page).getByRole('region', { name: 'Room Runner' }),
    ).toHaveCount(0);
    await expect(workspace(page).getByLabel('Room members')).toBeVisible();
    await expect(
      workspace(page).getByRole('form', {
        name: 'Thread composer',
        exact: true,
      }),
    ).toHaveCount(0);
    await expect(
      composer.getByLabel(`Message ${room.name}`, { exact: true }),
    ).toHaveValue(revisionDraft);
    await expect(
      composer.getByText('Requested changes', { exact: true }),
    ).toBeVisible();
    await expect(composer.getByRole('button', { name: /^To:/ })).toHaveText(
      /Room discussion/,
    );
    await expect
      .poll(async () => {
        const saved = await (
          await page.request.get(
            `/api/me/drafts/conversation?key=${room.id}:room`,
          )
        ).json();
        return JSON.parse(saved.draft.value?.references || '[]');
      })
      .toContainEqual({
        kind: 'message',
        messageId: feedback.messageId,
        title: 'Requested changes',
      });
    expect(
      (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json())
        .runs,
    ).toHaveLength(0);
    expect(
      (await (await page.request.get(`/api/rooms/${room.id}/messages`)).json())
        .messages,
    ).toEqual(messagesBeforeHandoff);
    await openReview(page, title);
    await detail(page)
      .getByRole('button', { name: 'Edit Latest File', exact: true })
      .click();
    await expect(library(page)).not.toContainText(
      /(?:Saved|Historical) version · read only/,
    );
    await library(page)
      .getByRole('button', { name: 'Edit Text', exact: true })
      .click();
    await library(page)
      .getByLabel('Document content', { exact: true })
      .fill(revised);
    await publish(page);
    await openReview(page, title);
    await detail(page)
      .getByRole('button', { name: 'Submit New Version', exact: true })
      .click();
    await expect
      .poll(async () => (await readReview(page, review.id)).status)
      .toBe('in_review');
    const next = await readReview(page, review.id);
    expect(next.submittedCommit).not.toBe(submitted.commit);
    expect(next.threadRootId).toBe(review.threadRootId);

    await detail(reviewer)
      .getByRole('button', { name: 'Refresh Review', exact: true })
      .click();
    await expect(
      detail(reviewer).getByRole('button', { name: 'Approve', exact: true }),
    ).toBeEnabled();
    await detail(reviewer)
      .getByLabel('Review comment', { exact: true })
      .fill('The owner and outcome are clear.');
    const ownerDocumentLink = detail(page).getByRole('button', {
      name: 'Open Document',
      exact: true,
    });
    await ownerDocumentLink.focus();
    await detail(reviewer)
      .getByRole('button', { name: 'Approve', exact: true })
      .click();
    await expect
      .poll(async () => (await readReview(page, review.id)).status)
      .toBe('approved');
    await expect(detail(reviewer).locator('.camp-review-state')).toHaveText(
      'Approved',
    );
    const unsentKey = `decision:${review.id}`;
    const privateComment = await (
      await reviewer.request.get(
        `/api/me/drafts/review?key=${encodeURIComponent(unsentKey)}`,
      )
    ).json();
    const restoredComment =
      'Keep this private note available after the decision.';
    expect(
      (
        await mutate(
          reviewer,
          '/me/drafts/review',
          {
            key: unsentKey,
            revision: privateComment.draft.revision,
            value: { comment: restoredComment, receipt: null },
          },
          'PUT',
        )
      ).ok(),
    ).toBe(true);
    await openReview(reviewer, title);
    await expect(
      detail(reviewer).getByLabel('Unsent private comment', { exact: true }),
    ).toHaveValue(restoredComment);
    await expect(
      detail(page).getByRole('button', {
        name: 'Refresh Review',
        exact: true,
      }),
    ).toBeVisible();
    await expect(ownerDocumentLink).toBeFocused();
    await detail(page)
      .getByRole('button', { name: 'Refresh Review', exact: true })
      .click();
    await expect(
      detail(page).getByRole('button', { name: 'Deliver', exact: true }),
    ).toBeEnabled();
    await detail(page)
      .getByRole('button', { name: 'Deliver', exact: true })
      .click();
    await expect
      .poll(async () => (await readReview(page, review.id)).status)
      .toBe('delivered');
    const delivered = await readReview(page, review.id);
    expect(delivered.deliveredCommit).toBe(next.submittedCommit);
    await expect(detail(page)).toContainText('Delivered');
    const outcome = detail(page).getByRole('heading', {
      name: title,
      exact: true,
    });
    await expect(outcome).toBeFocused();
    await expect(outcome).toBeInViewport();
    await expect(outcome).toHaveAccessibleDescription('Delivered');
    expect(
      await detail(page)
        .locator('.camp-review-body')
        .evaluate((body) => body.scrollTop),
    ).toBe(0);
    await page.screenshot({
      path: testInfo.outputPath('delivered-review.png'),
    });
    await navigation.reload();
    await openReview(page, title);
    await expect(detail(page)).toContainText('Delivered');
    await detail(page)
      .getByRole('button', { name: 'Open Discussion', exact: true })
      .click();
    await expect(
      page.getByLabel('Message thread', { exact: true }),
    ).toBeVisible();
    await openReview(page, title);
    await detail(page)
      .getByRole('button', { name: 'Open Discussion', exact: true })
      .click();
    await expect(
      page.getByLabel('Message thread', { exact: true }),
    ).toBeVisible();
    const messages = await (
      await page.request.get(`/api/rooms/${room.id}/messages`)
    ).json();
    expect(
      messages.messages.filter(
        (message: { id: string }) => message.id === review.threadRootId,
      ),
    ).toHaveLength(1);
    expect(
      (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json())
        .runs,
    ).toEqual([]);
    await page.screenshot({
      path: testInfo.outputPath('delivered-thread.png'),
    });
    expect(errors).toEqual([]);
  } finally {
    await reviewerContext.close();
  }
});

test('a newer publication blocks delivery and preserves the approved document', async ({
  page,
  browserName,
}, testInfo) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  const navigation = watchPageErrors(page, browserName, errors);
  await signIn(page);
  const room = await createRoom(
    page,
    `stale-review-${testInfo.project.name}-${randomUUID().slice(0, 8)}`,
  );
  const user = (await (await page.request.get('/api/auth/status')).json())
    .user as {
    id: string;
  };
  const resource = await (
    await page.request.get(`/api/resources/${room.resourceId}/files`)
  ).json();
  const initial = await mutate(
    page,
    `/resources/${room.resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: resource.commit,
      path: 'handoff.md',
      content: 'The approved handoff.',
    },
    'PUT',
  );
  expect(initial.ok()).toBeTruthy();
  const initialCommit = (await initial.json()).resource.currentCommit as string;
  const title = `Exact delivery ${testInfo.project.name}`;
  const requested = await mutate(page, `/rooms/${room.id}/reviews`, {
    requestId: randomUUID(),
    title,
    resourceId: room.resourceId,
    path: 'handoff.md',
    commit: initialCommit,
    reviewerUserId: user.id,
  });
  expect(requested.ok()).toBeTruthy();
  const review = (await requested.json()).review as Review;
  await openReview(page, title);
  await detail(page)
    .getByRole('button', { name: 'Approve', exact: true })
    .click();
  await expect
    .poll(async () => (await readReview(page, review.id)).status)
    .toBe('approved');
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let submitted = false;
  await page.route(`**/api/reviews/${review.id}/actions`, async (route) => {
    if (route.request().postDataJSON().action !== 'deliver')
      return route.continue();
    submitted = true;
    await held;
    await route.continue();
  });
  try {
    await detail(page)
      .getByRole('button', { name: 'Deliver', exact: true })
      .click();
    await expect.poll(() => submitted).toBe(true);
    const changed = await mutate(
      page,
      `/resources/${room.resourceId}/files`,
      {
        requestId: randomUUID(),
        expectedCommit: initialCommit,
        path: 'handoff.md',
        content: 'A later version that still needs review.',
      },
      'PUT',
    );
    expect(changed.ok()).toBeTruthy();
    const latestCommit = (await changed.json()).resource
      .currentCommit as string;
    release();
    await expect(detail(page).getByRole('alert')).toBeVisible();
    await page.unrouteAll({ behavior: 'wait' });
    const unchanged = await readReview(page, review.id);
    expect(unchanged.status).toBe('approved');
    expect(unchanged.submittedCommit).toBe(initialCommit);
    expect(unchanged.deliveredCommit).toBe('');
    expect(unchanged.outdated).toBe(true);
    await detail(page)
      .getByRole('button', { name: 'Open Document', exact: true })
      .click();
    await expect(library(page)).toContainText('The approved handoff.');
    await expect(library(page)).not.toContainText(
      'A later version that still needs review.',
    );
    await openReview(page, title);
    await detail(page)
      .getByRole('button', { name: 'Submit New Version', exact: true })
      .click();
    await expect
      .poll(async () => (await readReview(page, review.id)).submittedCommit)
      .toBe(latestCommit);
    await detail(page)
      .getByRole('button', { name: 'Approve', exact: true })
      .click();
    await expect(
      detail(page).getByRole('button', { name: 'Deliver', exact: true }),
    ).toBeEnabled();
    await detail(page)
      .getByRole('button', { name: 'Deliver', exact: true })
      .click();
    await expect
      .poll(async () => (await readReview(page, review.id)).deliveredCommit)
      .toBe(latestCommit);
    const later = await mutate(
      page,
      `/resources/${room.resourceId}/files`,
      {
        requestId: randomUUID(),
        expectedCommit: latestCommit,
        path: 'handoff.md',
        content: 'Future planning does not rewrite the delivered version.',
      },
      'PUT',
    );
    expect(later.ok()).toBeTruthy();
    await navigation.reload();
    await openReview(page, title);
    await detail(page)
      .getByRole('button', { name: 'Open Document', exact: true })
      .click();
    await expect(library(page)).toContainText(
      'A later version that still needs review.',
    );
    expect((await readReview(page, review.id)).deliveredCommit).toBe(
      latestCommit,
    );
    const result = await (
      await page.request.get(`/api/reviews/${review.id}`)
    ).json();
    expect(
      result.events.filter(
        (event: { action: string }) => event.action === 'deliver',
      ),
    ).toHaveLength(1);
    expect(errors).toEqual([]);
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('a disabled reviewer can be reassigned and a cancelled version can be reviewed again', async ({
  page,
  browser,
  baseURL,
}, testInfo) => {
  test.setTimeout(90_000);
  await signIn(page);
  const owner = (await (await page.request.get('/api/auth/status')).json())
    .user as {
    id: string;
  };
  const invitation = await mutate(page, '/team/invites', {
    expiresInHours: 24,
  });
  expect(invitation.ok()).toBeTruthy();
  const token = (await invitation.json()).token as string;
  const memberContext = await browser.newContext({ baseURL });
  let reviewerId: string;
  try {
    const member = await memberContext.request.post('/api/auth/accept-invite', {
      headers: { Origin: new URL(baseURL!).origin },
      data: {
        token,
        name: 'Temporary Reviewer',
        username: `temporary-${testInfo.project.name}-${randomUUID().slice(0, 6)}`,
        password: 'A private temporary reviewer password',
      },
    });
    expect(member.ok()).toBeTruthy();
    reviewerId = (await member.json()).user.id as string;
  } finally {
    await memberContext.close();
  }
  const room = await createRoom(
    page,
    `reassign-review-${testInfo.project.name}`,
  );
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  await newDocument(page, 'scope.md', 'A small, explicit delivery scope.');
  await publish(page);
  const title = `Review assignment ${testInfo.project.name}`;
  await library(page)
    .getByRole('button', { name: 'Request Review…', exact: true })
    .click();
  const request = library(page).getByRole('form', {
    name: 'Request document review',
    exact: true,
  });
  await request.getByLabel('Title', { exact: true }).fill(title);
  await request
    .getByLabel('Reviewer', { exact: true })
    .selectOption(reviewerId);
  let dropReceipt = true;
  await page.route(`**/api/rooms/${room.id}/reviews`, async (route) => {
    if (route.request().method() !== 'POST' || !dropReceipt)
      return route.continue();
    const response = await route.fetch();
    expect(response.ok()).toBeTruthy();
    dropReceipt = false;
    await route.abort('failed');
  });
  await request
    .getByRole('button', { name: 'Request Review', exact: true })
    .click();
  await expect(request.getByRole('alert')).toBeVisible();
  await expect(request.getByLabel('Title', { exact: true })).toBeDisabled();
  await expect(request.getByLabel('Reviewer', { exact: true })).toBeDisabled();
  await request
    .getByRole('button', { name: 'Retry Request', exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (
          await (
            await page.request.get(`/api/reviews?roomId=${room.id}`)
          ).json()
        ).reviews.length,
    )
    .toBe(1);
  await page.unrouteAll({ behavior: 'wait' });
  await expect(
    detail(page).getByRole('heading', { name: title, exact: true }),
  ).toBeVisible();
  const reviews = await (
    await page.request.get(`/api/reviews?roomId=${room.id}`)
  ).json();
  const review = reviews.reviews[0] as Review;
  const disabled = await mutate(
    page,
    `/team/members/${reviewerId}`,
    { enabled: false },
    'PATCH',
  );
  expect(disabled.ok()).toBeTruthy();
  await openReview(page, title);
  await expect(detail(page)).toContainText(
    'The assigned reviewer is disabled.',
  );
  await expect(
    detail(page).getByRole('button', { name: 'Cancel Review', exact: true }),
  ).not.toBeVisible();
  await detail(page).getByText('Manage Review', { exact: true }).click();
  await detail(page)
    .getByRole('button', { name: 'Reassign', exact: true })
    .click();
  await detail(page)
    .getByLabel('New reviewer', { exact: true })
    .selectOption(owner.id);
  await detail(page)
    .getByRole('button', { name: 'Assign Reviewer', exact: true })
    .click();
  await expect
    .poll(async () => (await readReview(page, review.id)).reviewerUserId)
    .toBe(owner.id);
  const cancelReview = detail(page).getByRole('button', {
    name: 'Cancel Review',
    exact: true,
  });
  // Complete reassignment's own receipt cleanup before intercepting the next
  // action's clear. Server state alone does not establish UI completion.
  await expect(cancelReview).toBeEnabled();
  let releaseClear = () => {};
  const clearedReceipt = new Promise<void>((resolve) => {
    releaseClear = resolve;
  });
  let clearing = false;
  await page.route('**/api/me/drafts/review', async (route) => {
    if (route.request().method() !== 'PUT') return route.continue();
    const body = route.request().postDataJSON() as {
      key: string;
      value: { receipt: unknown };
    };
    if (body.key !== `decision:${review.id}` || body.value.receipt !== null)
      return route.continue();
    const response = await route.fetch();
    expect(response.ok()).toBeTruthy();
    clearing = true;
    await clearedReceipt;
    await route.fulfill({ response });
  });
  const navigationError = page.getByText(
    'A private draft could not be saved. Resolve its error before switching documents.',
    { exact: true },
  );
  try {
    await cancelReview.click();
    await expect.poll(() => clearing).toBe(true);
    await expect
      .poll(async () => (await readReview(page, review.id)).status)
      .toBe('cancelled');
    // Receipt cleanup stays held while SSE reports the cancelled revision.
    // Let its notice settle before clicking the document control below it.
    await expect(
      detail(page)
        .getByRole('status')
        .filter({ hasText: 'This review changed in another session.' }),
    ).toBeVisible();
    await detail(page)
      .getByRole('button', { name: 'Open Document', exact: true })
      .click();
    await expect(navigationError).not.toBeVisible();
    releaseClear();
    await expect(library(page)).toContainText('Saved version · read only');
    await expect(navigationError).not.toBeVisible();
    await expect(
      library(page).getByRole('button', {
        name: 'Return to Latest',
        exact: true,
      }),
    ).toBeVisible();
    await page.screenshot({
      path: testInfo.outputPath('review-navigation-after-cancel.png'),
    });
  } finally {
    releaseClear();
    await page.unrouteAll({ behavior: 'wait' });
  }
  await library(page)
    .getByRole('button', { name: 'Return to Latest', exact: true })
    .click();
  await library(page)
    .getByRole('button', { name: 'Request Review…', exact: true })
    .click();
  await request
    .getByRole('button', { name: 'New Review', exact: true })
    .click();
  await request.getByLabel('Title', { exact: true }).fill(`${title} restarted`);
  await request.getByLabel('Reviewer', { exact: true }).selectOption(owner.id);
  let releaseCreated = () => {};
  const createdResponse = new Promise<void>((resolve) => {
    releaseCreated = resolve;
  });
  // Server acceptance precedes private receipt persistence and navigation.
  // Keep the response pending so API visibility cannot stand in for the UI.
  await page.route(`**/api/rooms/${room.id}/reviews`, async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    expect(response.ok()).toBeTruthy();
    await createdResponse;
    await route.fulfill({ response });
  });
  try {
    await request
      .getByRole('button', { name: 'Request Review', exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (
            await (
              await page.request.get(`/api/reviews?roomId=${room.id}`)
            ).json()
          ).reviews.length,
      )
      .toBe(2);
    await expect(request.getByLabel('Title', { exact: true })).toBeDisabled();
    releaseCreated();
    await expect(
      detail(page).getByRole('heading', {
        name: `${title} restarted`,
        exact: true,
      }),
    ).toBeVisible();
  } finally {
    releaseCreated();
    await page.unrouteAll({ behavior: 'wait' });
  }
  await expect(detail(page)).toContainText('In Review');
  await expect(detail(page)).not.toContainText(
    'The assigned reviewer is disabled.',
  );
  await detail(page).getByText('Manage Review', { exact: true }).click();
  await detail(page)
    .getByRole('button', { name: 'Cancel Review', exact: true })
    .click();
  const records = await (
    await page.request.get(`/api/reviews?roomId=${room.id}`)
  ).json();
  expect(records.reviews.map((item: Review) => item.submittedCommit)).toEqual([
    review.submittedCommit,
    review.submittedCommit,
  ]);
});

test('review navigation waits for the action and preserves a failed receipt clear for explicit recovery', async ({
  page,
}, testInfo) => {
  await signIn(page);
  const owner = (await (await page.request.get('/api/auth/status')).json())
    .user as { id: string };
  const room = await createRoom(page, `review-clear-${testInfo.project.name}`);
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  await newDocument(
    page,
    'cleanup.md',
    'Keep the completed review recoverable.',
  );
  await publish(page);
  const files = await (
    await page.request.get(`/api/resources/${room.resourceId}/files`)
  ).json();
  const title = `Review receipt recovery ${testInfo.project.name}`;
  const requested = await mutate(page, `/rooms/${room.id}/reviews`, {
    requestId: randomUUID(),
    title,
    resourceId: room.resourceId,
    path: 'cleanup.md',
    commit: files.commit,
    reviewerUserId: owner.id,
  });
  expect(requested.ok()).toBeTruthy();
  const review = (await requested.json()).review as Review;
  await openReview(page, title);
  await detail(page).getByText('Manage Review', { exact: true }).click();
  const cancelReview = detail(page).getByRole('button', {
    name: 'Cancel Review',
    exact: true,
  });
  await expect(cancelReview).toBeEnabled();
  let releaseAction = () => {};
  const actionReceipt = new Promise<void>((resolve) => {
    releaseAction = resolve;
  });
  let committed = false;
  let clearFailed = false;
  await page.route(`**/api/reviews/${review.id}/actions`, async (route) => {
    if (
      route.request().method() !== 'POST' ||
      route.request().postDataJSON().action !== 'cancel'
    )
      return route.continue();
    const response = await route.fetch();
    expect(response.ok()).toBeTruthy();
    committed = true;
    await actionReceipt;
    await route.fulfill({ response });
  });
  await page.route('**/api/me/drafts/review', async (route) => {
    if (route.request().method() !== 'PUT') return route.continue();
    const body = route.request().postDataJSON() as {
      key: string;
      value: { receipt: unknown };
    };
    if (body.key !== `decision:${review.id}` || body.value.receipt !== null)
      return route.continue();
    clearFailed = true;
    await route.fulfill({
      status: 503,
      contentType: 'application/json',
      body: JSON.stringify({
        error: {
          code: 'storage_error',
          message: 'The completed review receipt could not be cleared.',
        },
      }),
    });
  });
  const navigationError = page.getByText(
    'A private draft could not be saved. Resolve its error before switching documents.',
    { exact: true },
  );
  try {
    await cancelReview.click();
    await expect.poll(() => committed).toBe(true);
    await expect
      .poll(async () => (await readReview(page, review.id)).status)
      .toBe('cancelled');
    // At this point the ordinary draft writer has no unsaved work. Only the
    // action owner can wait for and recheck its later receipt-clear write.
    await detail(page)
      .getByRole('button', { name: 'Open Document', exact: true })
      .click();
    await expect(navigationError).not.toBeVisible();
    releaseAction();
    await expect.poll(() => clearFailed).toBe(true);
    await expect(detail(page).locator('.camp-review-state')).toHaveText(
      'Cancelled',
    );
    await expect(navigationError).toBeVisible();
    await expect(workspace(page)).toHaveClass(/is-active/);
    await expect(
      library(page).getByRole('button', {
        name: 'Return to Latest',
        exact: true,
      }),
    ).toHaveCount(0);
    const retained = await (
      await page.request.get(
        `/api/me/drafts/review?key=${encodeURIComponent(`decision:${review.id}`)}`,
      )
    ).json();
    expect(retained.draft.value.receipt).not.toBeNull();
    await page.unrouteAll({ behavior: 'wait' });
    await detail(page)
      .getByRole('button', { name: 'Compare Private Drafts', exact: true })
      .click();
    await detail(page)
      .getByRole('button', { name: 'Save My Draft', exact: true })
      .click();
    await expect(detail(page).getByRole('alert')).toHaveCount(0);
    await detail(page)
      .getByRole('button', { name: 'Open Document', exact: true })
      .click();
    await expect(library(page)).toContainText('Saved version · read only');
    await expect(navigationError).not.toBeVisible();
    const result = await (
      await page.request.get(`/api/reviews/${review.id}`)
    ).json();
    expect(
      result.events.filter(
        (event: { action: string }) => event.action === 'cancel',
      ),
    ).toHaveLength(1);
    await page.screenshot({
      path: testInfo.outputPath('review-navigation-after-receipt-recovery.png'),
    });
  } finally {
    releaseAction();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('a review form retains its draft and waits for an explicit newer version', async ({
  page,
}, testInfo) => {
  await signIn(page);
  const room = await createRoom(page, `review-draft-${testInfo.project.name}`);
  const user = (await (await page.request.get('/api/auth/status')).json())
    .user as {
    id: string;
  };
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  await newDocument(page, 'brief.md', 'The version selected before review.');
  await publish(page);
  const initial = await (
    await page.request.get(`/api/resources/${room.resourceId}/files`)
  ).json();
  await library(page)
    .getByRole('button', { name: 'Request Review…', exact: true })
    .click();
  const request = library(page).getByRole('form', {
    name: 'Request document review',
    exact: true,
  });
  const title = `Keep my review draft ${testInfo.project.name}`;
  await request.getByLabel('Title', { exact: true }).fill(title);
  await request.getByLabel('Reviewer', { exact: true }).selectOption(user.id);
  const changed = await mutate(
    page,
    `/resources/${room.resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: initial.commit,
      path: 'brief.md',
      content: 'A teammate published a newer document while the form was open.',
    },
    'PUT',
  );
  expect(changed.ok()).toBeTruthy();
  const latest = (await changed.json()).resource.currentCommit as string;
  await expect(request).toContainText(
    'Shared files have changed since this form opened.',
  );
  await expect(request.getByLabel('Title', { exact: true })).toHaveValue(title);
  await expect(request.getByLabel('Reviewer', { exact: true })).toHaveValue(
    user.id,
  );
  await expect(
    request.getByRole('button', { name: 'Request Review', exact: true }),
  ).toBeDisabled();
  expect(
    (await (await page.request.get(`/api/reviews?roomId=${room.id}`)).json())
      .reviews,
  ).toEqual([]);
  await request.getByRole('button', { name: 'Cancel', exact: true }).click();
  await library(page)
    .getByRole('button', { name: 'Request Review…', exact: true })
    .click();
  await expect(request.getByLabel('Title', { exact: true })).toHaveValue(title);
  await expect(request.getByLabel('Reviewer', { exact: true })).toHaveValue(
    user.id,
  );
  await request
    .getByRole('button', { name: 'Request Review', exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (
          await (
            await page.request.get(`/api/reviews?roomId=${room.id}`)
          ).json()
        ).reviews.length,
    )
    .toBe(1);
  const reviews = await (
    await page.request.get(`/api/reviews?roomId=${room.id}`)
  ).json();
  expect(reviews.reviews[0].submittedCommit).toBe(latest);
  expect(reviews.reviews[0].title).toBe(title);
  expect(reviews.reviews[0].reviewerUserId).toBe(user.id);
});
