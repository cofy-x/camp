import { randomUUID } from 'node:crypto';
import { expect, test, type Browser, type Page } from '@playwright/test';
import type { AttentionPage } from '../apps/dashboard/src/features/attention/api';
import type { Review } from '../apps/dashboard/src/features/reviews/api';
import type { WorkItem } from '../apps/dashboard/src/features/workitems/api';
import type { InboxItem } from '../apps/dashboard/src/team/api';
import { watchPageErrors } from './page-errors';
import {
  createRoom,
  inboxToggle,
  mutate,
  openApp,
  openInboxActivity,
  signIn,
  work,
  workspace,
} from './team-helpers';

const workDetail = (page: Page) =>
  work(page).getByRole('region', { name: 'Work item detail', exact: true });
const reviewDetail = (page: Page) =>
  workspace(page).getByRole('region', { name: 'Document review', exact: true });
const palette = (page: Page) =>
  page.getByRole('region', { name: 'Your inbox', exact: true });
const queue = (page: Page) =>
  palette(page).getByRole('list', { name: 'Needs me', exact: true });
const workQueue = (page: Page) =>
  work(page).getByRole('list', { name: 'Needs me', exact: true });

async function pending(page: Page, roomId = ''): Promise<AttentionPage> {
  const response = await page.request.get(
    `/api/attention?${new URLSearchParams({ roomId, kind: 'all', limit: '20' })}`,
  );
  expect(response.ok()).toBe(true);
  return response.json();
}

async function notices(page: Page): Promise<InboxItem[]> {
  return (await (await page.request.get('/api/me/inbox')).json()).items;
}

async function openPending(page: Page) {
  if (!(await palette(page).isVisible())) await inboxToggle(page).click();
  await palette(page)
    .getByRole('tab', { name: 'Needs me', exact: true })
    .click();
  return queue(page);
}

async function openWorkPending(page: Page, roomId = '') {
  await openApp(page, 'Work');
  const back = workDetail(page).getByRole('button', {
    name: 'Back to Work',
    exact: true,
  });
  if (await back.isVisible()) await back.click();
  await work(page)
    .getByRole('combobox', { name: 'View', exact: true })
    .selectOption('needs_me');
  await work(page)
    .getByRole('combobox', { name: 'Room', exact: true })
    .selectOption(roomId);
  return workQueue(page);
}

async function question(page: Page, roomId: string, goal: string) {
  const created = await mutate(page, `/rooms/${roomId}/work-items`, {
    requestId: randomUUID(),
    goal,
  });
  expect(created.ok()).toBe(true);
  const { item } = (await created.json()) as { item: WorkItem };
  const questionText = `Which date should we use for ${goal}?`;
  const asked = await mutate(page, `/work-items/${item.id}/decisions`, {
    requestId: randomUUID(),
    revision: item.revision,
    kind: 'request_judgment',
    reason: 'The customer needs one explicit decision.',
    question: questionText,
  });
  expect(asked.ok()).toBe(true);
  const saved = ((await asked.json()) as { item: WorkItem }).item;
  expect(saved.question).toBe(questionText);
  return { ...saved, question: questionText };
}

async function saveFile(page: Page, resourceId: string, content: string) {
  const before = await (
    await page.request.get(`/api/resources/${resourceId}/files`)
  ).json();
  const saved = await mutate(
    page,
    `/resources/${resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: before.commit,
      path: 'attention-review.md',
      content,
    },
    'PUT',
  );
  expect(saved.ok()).toBe(true);
  return (
    await (await page.request.get(`/api/resources/${resourceId}/files`)).json()
  ).commit as string;
}

async function requestReview(
  page: Page,
  room: { id: string; resourceId: string },
  commit: string,
  reviewerUserId: string,
  title: string,
) {
  const response = await mutate(page, `/rooms/${room.id}/reviews`, {
    requestId: randomUUID(),
    resourceId: room.resourceId,
    path: 'attention-review.md',
    commit,
    reviewerUserId,
    title,
  });
  expect(response.ok()).toBe(true);
  return ((await response.json()) as { review: Review }).review;
}

async function readReview(page: Page, id: string) {
  return (
    (await (await page.request.get(`/api/reviews/${id}`)).json()) as {
      review: Review;
    }
  ).review;
}

async function actReview(
  page: Page,
  review: Review,
  action: 'cancel' | 'request_changes' | 'resubmit',
  commit?: string,
) {
  const response = await mutate(page, `/reviews/${review.id}/actions`, {
    requestId: randomUUID(),
    revision: review.revision,
    action,
    comment: 'Keep the owner and the next action explicit.',
    ...(commit ? { commit } : {}),
  });
  expect(response.ok()).toBe(true);
  return ((await response.json()) as { review: Review }).review;
}

async function teammate(browser: Browser, owner: Page, baseURL?: string) {
  const invitation = await mutate(owner, '/team/invites', {});
  expect(invitation.ok()).toBe(true);
  const { token } = await invitation.json();
  const touch = await owner.evaluate(() => navigator.maxTouchPoints > 0);
  const context = await browser.newContext({
    baseURL,
    viewport: owner.viewportSize() ?? undefined,
    ...(touch ? { hasTouch: true, isMobile: true, deviceScaleFactor: 2 } : {}),
  });
  const page = await context.newPage();
  await page.goto('/');
  const response = await mutate(page, '/auth/accept-invite', {
    token,
    username: `attention-${randomUUID().slice(0, 8)}`,
    name: 'Attention teammate',
    password: 'A private attention browser test password',
  });
  expect(response.ok()).toBe(true);
  const { user } = (await response.json()) as { user: { id: string } };
  await page.reload();
  await expect(workspace(page)).toBeVisible();
  return { context, page, user };
}

test('reading Activity never resolves current questions or reviews, while actual decisions remove them', async ({
  page: ownerPage,
  browser,
  browserName,
  baseURL,
}, info) => {
  await signIn(ownerPage);
  const first = await createRoom(
    ownerPage,
    `Attention decision ${randomUUID().slice(0, 8)}`,
  );
  const second = await createRoom(
    ownerPage,
    `Attention reviews ${randomUUID().slice(0, 8)}`,
  );
  const commit = await saveFile(
    ownerPage,
    second.resourceId,
    '# Saved launch proposal',
  );
  const member = await teammate(browser, ownerPage, baseURL);
  const page = member.page;
  const errors = watchPageErrors(page, browserName);
  try {
    const matter = await question(
      page,
      first.id,
      'Confirm the customer launch',
    );
    const { user } = await (await page.request.get('/api/auth/status')).json();
    const approved = await requestReview(
      ownerPage,
      second,
      commit,
      user.id,
      'Review the launch proposal',
    );
    const cancelled = await requestReview(
      ownerPage,
      second,
      commit,
      user.id,
      'Review the retired proposal',
    );
    const initial = await notices(page);
    const questionNotice = initial.find(
      (item) => item.workItemId === matter.id,
    )!;
    const reviewNotice = initial.find(
      (item) =>
        item.reviewId === approved.id && item.kind === 'review_requested',
    )!;
    const cancelledNotice = initial.find(
      (item) =>
        item.reviewId === cancelled.id && item.kind === 'review_requested',
    )!;
    expect(questionNotice.readAt).toBe('');
    expect(reviewNotice.readAt).toBe('');

    await openPending(page);
    await expect(
      palette(page).getByRole('tab', { name: 'Needs me', exact: true }),
    ).toHaveAttribute('aria-selected', 'true');
    await expect(
      queue(page).getByRole('button').filter({ hasText: matter.goal }),
    ).toBeVisible();
    await expect(
      queue(page).getByRole('button').filter({ hasText: approved.title }),
    ).toBeVisible();
    await page.screenshot({ path: info.outputPath('needs-me-cross-room.png') });
    await palette(page)
      .getByRole('button', { name: 'Close inbox', exact: true })
      .click();
    await openWorkPending(page);
    await expect(
      workQueue(page).getByRole('button').filter({ hasText: matter.goal }),
    ).toBeVisible();
    await expect(
      workQueue(page).getByRole('button').filter({ hasText: approved.title }),
    ).toBeVisible();

    const activity = await openInboxActivity(page);
    await activity.getByRole('button').filter({ hasText: matter.goal }).click();
    await expect(work(page)).toHaveClass(/is-active/);
    await expect(workDetail(page)).toContainText(matter.question);
    await expect
      .poll(async () =>
        Boolean(
          (await notices(page)).find((item) => item.id === questionNotice.id)
            ?.readAt,
        ),
      )
      .toBe(true);
    await openPending(page);
    await expect(
      queue(page).getByRole('button').filter({ hasText: matter.goal }),
    ).toBeVisible();
    await palette(page)
      .getByRole('button', { name: 'Close inbox', exact: true })
      .click();
    await workDetail(page)
      .getByLabel('Your answer', { exact: true })
      .fill('Use the customer-confirmed Tuesday.');
    await workDetail(page)
      .getByRole('button', { name: 'Send answer', exact: true })
      .click();
    await expect
      .poll(async () =>
        (await pending(page, first.id)).items.some(
          (item) => item.target.workItemId === matter.id,
        ),
      )
      .toBe(false);

    await openPending(page);
    await expect(
      queue(page).getByRole('button').filter({ hasText: matter.goal }),
    ).toHaveCount(0);
    await queue(page)
      .getByRole('button')
      .filter({ hasText: approved.title })
      .click();
    await expect(workspace(page)).toHaveClass(/is-active/);
    await expect(reviewDetail(page)).toContainText(approved.title);
    expect(
      (await notices(page)).find((item) => item.id === reviewNotice.id)?.readAt,
    ).toBe('');
    await reviewDetail(page)
      .getByRole('button', { name: 'Approve', exact: true })
      .click();
    await expect
      .poll(async () =>
        (await pending(page, second.id)).items.some(
          (item) => item.target.reviewId === approved.id,
        ),
      )
      .toBe(false);
    await actReview(ownerPage, cancelled, 'cancel');
    await expect
      .poll(async () => (await pending(page, second.id)).total)
      .toBe(0);
    await openPending(page);
    await expect(
      queue(page).getByRole('button').filter({ hasText: approved.title }),
    ).toHaveCount(0);
    await expect(
      queue(page).getByRole('button').filter({ hasText: cancelled.title }),
    ).toHaveCount(0);
    const retained = await notices(page);
    expect(retained.find((item) => item.id === reviewNotice.id)?.readAt).toBe(
      '',
    );
    expect(
      retained.find((item) => item.id === cancelledNotice.id)?.readAt,
    ).toBe('');
    expect(errors.errors).toEqual([]);
  } finally {
    await member.context.close();
  }
});

test('review responsibility follows the current actor and an outdated review leaves only real requester recovery', async ({
  page,
  browser,
  browserName,
  baseURL,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Attention authority ${randomUUID().slice(0, 8)}`,
  );
  const commit = await saveFile(
    page,
    room.resourceId,
    '# Original review version',
  );
  const member = await teammate(browser, page, baseURL);
  try {
    let review = await requestReview(
      page,
      room,
      commit,
      member.user.id,
      'Approve the current customer plan',
    );
    expect((await pending(page, room.id)).total).toBe(0);
    expect((await pending(member.page, room.id)).items).toEqual([
      expect.objectContaining({
        kind: 'review',
        target: expect.objectContaining({ reviewId: review.id }),
      }),
    ]);
    const unauthorized = await mutate(page, `/reviews/${review.id}/actions`, {
      requestId: randomUUID(),
      revision: review.revision,
      action: 'approve',
    });
    expect(unauthorized.status()).toBe(403);
    await openWorkPending(page, room.id);
    await expect(
      workQueue(page).getByRole('button').filter({ hasText: review.title }),
    ).toHaveCount(0);

    const nextCommit = await saveFile(
      page,
      room.resourceId,
      '# Updated review version',
    );
    await expect
      .poll(async () => (await pending(page, room.id)).counts.exception)
      .toBe(1);
    expect((await pending(member.page, room.id)).total).toBe(0);
    await expect(
      workQueue(page).getByRole('button').filter({ hasText: review.title }),
    ).toBeVisible();
    review = await readReview(page, review.id);
    expect(review.outdated).toBe(true);
    review = await actReview(page, review, 'resubmit', nextCommit);
    await expect.poll(async () => (await pending(page, room.id)).total).toBe(0);
    await expect(
      workQueue(page).getByRole('button').filter({ hasText: review.title }),
    ).toHaveCount(0);
    expect((await pending(member.page, room.id)).counts.review).toBe(1);

    review = await actReview(member.page, review, 'request_changes');
    await expect
      .poll(async () => (await pending(page, room.id)).counts.judgment)
      .toBe(1);
    expect((await pending(member.page, room.id)).total).toBe(0);
    await actReview(page, review, 'resubmit', nextCommit);
    await expect.poll(async () => (await pending(page, room.id)).total).toBe(0);
    expect(errors.errors).toEqual([]);
  } finally {
    await member.context.close();
  }
});

test('Needs me clears prior Room rows while loading and ignores a late response after switching back', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const first = await createRoom(
    page,
    `Attention first ${randomUUID().slice(0, 8)}`,
  );
  const second = await createRoom(
    page,
    `Attention second ${randomUUID().slice(0, 8)}`,
  );
  const original = await question(
    page,
    first.id,
    'Choose the original Room date',
  );
  const other = await question(page, second.id, 'Choose the other Room date');
  await openWorkPending(page, first.id);
  await expect(
    workQueue(page).getByRole('button').filter({ hasText: original.goal }),
  ).toBeVisible();
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let waiting = false;
  let returned = false;
  await page.route('**/api/attention?*', async (route) => {
    if (new URL(route.request().url()).searchParams.get('roomId') !== second.id)
      return route.fallback();
    const response = await route.fetch();
    waiting = true;
    await held;
    await route.fulfill({ response });
    returned = true;
  });
  try {
    const room = work(page).getByRole('combobox', {
      name: 'Room',
      exact: true,
    });
    await room.selectOption(second.id);
    await expect.poll(() => waiting).toBe(true);
    await expect(
      workQueue(page).getByRole('button').filter({ hasText: original.goal }),
    ).toHaveCount(0);
    await expect(work(page).getByRole('status')).toContainText(/Loading/);
    await room.selectOption(first.id);
    await expect(
      workQueue(page).getByRole('button').filter({ hasText: original.goal }),
    ).toBeVisible();
    release();
    await expect.poll(() => returned).toBe(true);
    await expect(room).toHaveValue(first.id);
    await expect(
      workQueue(page).getByRole('button').filter({ hasText: original.goal }),
    ).toBeVisible();
    await expect(
      workQueue(page).getByRole('button').filter({ hasText: other.goal }),
    ).toHaveCount(0);
    expect(errors.errors).toEqual([]);
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('attention navigation preserves an unfinished answer and blocks leaving when its private draft cannot save', async ({
  page: ownerPage,
  browser,
  browserName,
  baseURL,
}) => {
  await signIn(ownerPage);
  const room = await createRoom(
    ownerPage,
    `Attention draft ${randomUUID().slice(0, 8)}`,
  );
  const commit = await saveFile(
    ownerPage,
    room.resourceId,
    '# A separately saved review',
  );
  const member = await teammate(browser, ownerPage, baseURL);
  const page = member.page;
  const errors = watchPageErrors(page, browserName);
  try {
    const matter = await question(
      page,
      room.id,
      'Keep the unfinished customer answer',
    );
    const { user } = await (await page.request.get('/api/auth/status')).json();
    const review = await requestReview(
      ownerPage,
      room,
      commit,
      user.id,
      'Review without losing the answer',
    );
    await openPending(page);
    await queue(page)
      .getByRole('button')
      .filter({ hasText: matter.goal })
      .click();
    await expect(workDetail(page)).toContainText(matter.goal);
    let fail = true;
    await page.route('**/api/me/drafts/conversation', (route) => {
      const request = route.request();
      if (
        fail &&
        request.method() === 'PUT' &&
        request.postDataJSON().key === `work-item-decision:${matter.id}`
      ) {
        return route.fulfill({
          status: 503,
          json: { error: 'The pending answer could not be saved.' },
        });
      }
      return route.fallback();
    });
    const answer = workDetail(page).getByLabel('Your answer', { exact: true });
    const draft = 'Keep this answer private until I choose to send it.';
    await answer.fill(draft);
    await expect(
      workDetail(page)
        .getByRole('alert')
        .filter({ hasText: 'The pending answer could not be saved.' }),
    ).toBeVisible();
    await openPending(page);
    await queue(page)
      .getByRole('button')
      .filter({ hasText: review.title })
      .click();
    await expect(work(page)).toHaveClass(/is-active/);
    await expect(answer).toHaveValue(draft);
    await expect(reviewDetail(page)).toHaveCount(0);
    await expect(palette(page).getByRole('alert')).toContainText(
      'Resolve its error before switching',
    );
    await palette(page)
      .getByRole('button', { name: 'Close inbox', exact: true })
      .click();
    fail = false;
    await workDetail(page)
      .getByRole('button', { name: 'Compare Private Drafts', exact: true })
      .click();
    await workDetail(page)
      .getByRole('button', { name: 'Save My Draft', exact: true })
      .click();
    await expect(workDetail(page).getByRole('alert')).toHaveCount(0);
    await openPending(page);
    await queue(page)
      .getByRole('button')
      .filter({ hasText: review.title })
      .click();
    await expect(workspace(page)).toHaveClass(/is-active/);
    await expect(reviewDetail(page)).toContainText(review.title);
    await openPending(page);
    await queue(page)
      .getByRole('button')
      .filter({ hasText: matter.goal })
      .click();
    await expect(work(page)).toHaveClass(/is-active/);
    await expect(answer).toHaveValue(draft);
    expect((await pending(page, room.id)).total).toBe(2);
    expect(errors.errors).toEqual([]);
  } finally {
    await member.context.close();
  }
});

test('failed attention refresh shows an unknown count, keeps accepted rows, and removes them after access is refused', async ({
  page: ownerPage,
  browser,
  browserName,
  baseURL,
}) => {
  await signIn(ownerPage);
  const room = await createRoom(
    ownerPage,
    `Attention read recovery ${randomUUID().slice(0, 8)}`,
  );
  const member = await teammate(browser, ownerPage, baseURL);
  const page = member.page;
  const errors = watchPageErrors(page, browserName);
  try {
    const matter = await question(
      page,
      room.id,
      'Keep the current review date visible',
    );
    await openWorkPending(page);
    await expect(
      workQueue(page).getByRole('button').filter({ hasText: matter.goal }),
    ).toBeVisible();
    await expect(inboxToggle(page)).toContainText('Needs me (1)');
    let failure = 503;
    await page.route('**/api/attention?*', (route) => {
      if (new URL(route.request().url()).searchParams.get('roomId') || !failure)
        return route.fallback();
      return route.fulfill({
        status: failure,
        json: {
          error:
            failure === 503
              ? 'Attention is temporarily unavailable.'
              : 'Attention access was refused.',
        },
      });
    });
    await work(page)
      .getByRole('button', { name: 'Refresh', exact: true })
      .click();
    await expect(inboxToggle(page)).toContainText('Needs me (?)');
    await expect(inboxToggle(page)).toHaveAccessibleName(
      /attention count unavailable/,
    );
    await expect(
      workQueue(page).getByRole('button').filter({ hasText: matter.goal }),
    ).toBeVisible();
    await openPending(page);
    await expect(palette(page).getByRole('alert')).toContainText(
      'Attention is temporarily unavailable.',
    );
    await expect(
      queue(page).getByRole('button').filter({ hasText: matter.goal }),
    ).toBeVisible();
    failure = 0;
    await palette(page)
      .getByRole('button', { name: 'Retry attention', exact: true })
      .click();
    await expect(palette(page).getByRole('alert')).toHaveCount(0);
    await expect(inboxToggle(page)).toContainText('Needs me (1)');
    await palette(page)
      .getByRole('button', { name: 'Close inbox', exact: true })
      .click();

    failure = 403;
    await work(page)
      .getByRole('button', { name: 'Refresh', exact: true })
      .click();
    await expect(work(page).getByRole('alert')).toContainText(
      'Attention access was refused.',
    );
    await expect(
      workQueue(page).getByRole('button').filter({ hasText: matter.goal }),
    ).toHaveCount(0);
    await openPending(page);
    await expect(
      queue(page).getByRole('button').filter({ hasText: matter.goal }),
    ).toHaveCount(0);
    await expect(inboxToggle(page)).toContainText('Needs me (?)');
    failure = 0;
    await palette(page)
      .getByRole('button', { name: 'Retry attention', exact: true })
      .click();
    await expect(
      queue(page).getByRole('button').filter({ hasText: matter.goal }),
    ).toBeVisible();
    await expect(inboxToggle(page)).toContainText('Needs me (1)');
    expect((await pending(page, room.id)).total).toBe(1);
    expect(errors.errors).toEqual([]);
  } finally {
    await member.context.close();
  }
});
