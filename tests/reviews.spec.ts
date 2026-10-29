import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import {
  createRoom,
  library,
  mutate,
  newDocument,
  openApp,
  signIn,
  workspace,
} from './team-helpers';

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
    await workspace(page)
      .getByRole('button', { name: 'Reviews', exact: true })
      .click();
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

test('two people review, revise and deliver an exact document version', async ({
  page,
  browser,
  baseURL,
  isMobile,
}, testInfo) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
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
  reviewer.on('pageerror', (error) => errors.push(error.message));
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
      .getByRole('button', { name: 'Library', exact: true })
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

    await reviewer.getByRole('button', { name: /Inbox, 1 unread/ }).click();
    await reviewer
      .getByRole('complementary', { name: 'Personal inbox' })
      .getByRole('button', { name: /A document needs your review/ })
      .click();
    await expect(workspace(reviewer)).toBeVisible();
    await expect(detail(reviewer)).toContainText(title);
    await expect(
      workspace(reviewer).getByRole('region', { name: 'Room Runner' }),
    ).toHaveCount(0);
    await expect(workspace(reviewer).getByLabel('Room members')).toHaveCount(0);
    await expect(
      workspace(reviewer).getByText(
        'Agents are unavailable · you can still post to the room',
        { exact: true },
      ),
    ).toHaveCount(0);
    expect(
      await detail(reviewer).evaluate((panel) => {
        const document = panel.querySelector('.camp-reviewed-document');
        const actions = panel.querySelector('.camp-review-actions');
        return Boolean(
          document &&
          actions &&
          document.compareDocumentPosition(actions) &
            Node.DOCUMENT_POSITION_FOLLOWING,
        );
      }),
    ).toBeTruthy();
    await detail(reviewer)
      .getByRole('button', { name: 'Open Document', exact: true })
      .click();
    await expect(library(reviewer)).toBeVisible();
    await expect(library(reviewer)).toContainText(
      'Pilot a weekly client update.',
    );
    await expect(library(reviewer)).toContainText('Saved version · read only');
    await openReview(reviewer, title);
    await detail(reviewer)
      .getByLabel('Review comment', { exact: true })
      .fill('Name the owner and the expected client outcome.');
    await detail(reviewer)
      .getByRole('button', { name: 'Open Document', exact: true })
      .click();
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
    await reviewer.getByRole('button', { name: /Inbox, 1 unread/ }).click();
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
    await reviewer.getByRole('button', { name: /Inbox, 1 unread/ }).click();
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
    await page.reload();
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
}, testInfo) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await signIn(page);
  const room = await createRoom(page, `stale-review-${testInfo.project.name}`);
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
    await page.reload();
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
    .getByRole('button', { name: 'Library', exact: true })
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
  await detail(page)
    .getByRole('button', { name: 'Cancel Review', exact: true })
    .click();
  await expect
    .poll(async () => (await readReview(page, review.id)).status)
    .toBe('cancelled');
  await detail(page)
    .getByRole('button', { name: 'Open Document', exact: true })
    .click();
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
  await openReview(page, `${title} restarted`);
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
    .getByRole('button', { name: 'Library', exact: true })
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
