import { randomUUID } from 'node:crypto';
import { expect, test as base, type Page } from '@playwright/test';
import type { Review } from '../apps/dashboard/src/features/reviews/api';
import type { Runner } from '../apps/dashboard/src/features/runners/api';
import type { WorkItem } from '../apps/dashboard/src/features/workitems/api';
import { remoteBrowserEnvironment } from './remote-browser-helpers';
import {
  createRoom,
  library,
  mutate,
  openApp,
  work,
  workspace,
} from './team-helpers';

type Remote = Awaited<ReturnType<typeof remoteBrowserEnvironment>>;
const test = base.extend<{ remote: Remote }>({
  remote: async ({ browserName }, use, info) => {
    const remote = await remoteBrowserEnvironment(info, browserName);
    try {
      await use(remote);
    } finally {
      await remote.close();
    }
  },
  baseURL: async ({ remote }, use) => {
    await use(remote.origin);
  },
});

const resultView = (page: Page) =>
  work(page).getByRole('region', { name: 'Work result', exact: true });
const reviewView = (page: Page) =>
  workspace(page).getByRole('region', { name: 'Document review', exact: true });

async function read<T>(page: Page, path: string): Promise<T> {
  const response = await page.request.get(`/api${path}`);
  expect(response.ok(), `GET ${path}`).toBeTruthy();
  return response.json() as Promise<T>;
}

async function openResult(page: Page, goal: string) {
  await openApp(page, 'Work');
  const win = work(page);
  await win
    .getByRole('combobox', { name: 'View', exact: true })
    .selectOption('all');
  await win
    .getByRole('button')
    .filter({ has: page.getByText(goal, { exact: true }) })
    .click();
  await expect(resultView(page)).toBeVisible();
}

test('Work opens human review of its exact published file and follows its delivery across accounts', async ({
  page,
  browser,
  isMobile,
  remote,
}, info) => {
  test.setTimeout(90_000);
  await remote.setup(page);
  const runnerName = 'Work review fixture Runner';
  const enrollment = await mutate(page, '/runner-enrollments', {
    name: runnerName,
  });
  expect(enrollment.ok()).toBeTruthy();
  const { token } = await enrollment.json();
  await remote.join(runnerName, token);
  let runner: Runner | undefined;
  await expect
    .poll(async () => {
      const { runners } = await read<{ runners: Runner[] }>(page, '/runners');
      runner = runners.find((candidate) => candidate.name === runnerName);
      return Boolean(runner?.online && runner.capabilities.ready);
    })
    .toBe(true);
  const room = await createRoom(page, 'Work file review');
  const writer = await mutate(page, `/rooms/${room.id}/agents`, {
    name: 'Fixture Writer',
    handle: 'work-review-writer',
    access: 'write',
    enabled: true,
    preferredRunnerId: runner!.id,
  });
  expect(writer.ok(), await writer.text()).toBeTruthy();
  const { agent } = await writer.json();
  const goal = 'Prepare the handover file for review';
  const started = await mutate(page, `/rooms/${room.id}/work-items`, {
    requestId: randomUUID(),
    goal,
    completionMode: 'automatic',
    participantAgentIds: [agent.id],
    firstAgentId: agent.id,
    start: true,
  });
  expect(started.ok(), await started.text()).toBeTruthy();
  const workId = (await started.json()).item.id as string;
  const readWork = () =>
    read<{ item: WorkItem }>(page, `/work-items/${workId}?view=current`);
  await expect
    .poll(async () => (await readWork()).item.status)
    .toBe('completed');
  const { item: completed } = await readWork();
  const result = completed.result!;
  expect(result).toMatchObject({
    published: true,
    resourceId: room.resourceId,
  });
  expect(result.files).toHaveLength(1);
  const [{ path }] = result.files;
  const original = await page.request.get(
    `/api/resources/${room.resourceId}/file?${new URLSearchParams({ commit: result.commit, path })}`,
  );
  expect(original.ok()).toBeTruthy();
  const originalBytes = await original.body();

  const invite = await mutate(page, '/team/invites', { expiresInHours: 24 });
  expect(invite.ok()).toBeTruthy();
  const inviteToken = (await invite.json()).token as string;
  const reviewerContext = await browser.newContext({
    baseURL: remote.origin,
    viewport: page.viewportSize() ?? undefined,
    ...(isMobile
      ? { hasTouch: true, isMobile: true, deviceScaleFactor: 2 }
      : {}),
  });
  try {
    const reviewer = await reviewerContext.newPage();
    await reviewer.goto(`/#invite=${encodeURIComponent(inviteToken)}`);
    await reviewer
      .getByLabel('Your name', { exact: true })
      .fill('File Reviewer');
    await reviewer
      .getByLabel('Username', { exact: true })
      .fill('file-reviewer');
    await reviewer
      .getByLabel(/^Password/)
      .fill('A password for the file review fixture');
    await reviewer
      .getByRole('button', { name: 'Join Team', exact: true })
      .click();
    await expect(workspace(reviewer)).toBeVisible();
    const { user } = await read<{ user: { id: string } }>(
      reviewer,
      '/auth/status',
    );

    const reviewReads: URL[] = [];
    page.on('request', (request) => {
      const url = new URL(request.url());
      if (
        request.method() === 'GET' &&
        url.pathname.endsWith('/result-reviews')
      )
        reviewReads.push(url);
    });
    await openResult(page, goal);
    const ownerResult = resultView(page);
    const requestReview = ownerResult.getByRole('button', {
      name: `Request Review… for ${path}`,
      exact: true,
    });
    await expect(requestReview).toBeVisible();
    expect(reviewReads.length).toBeGreaterThan(0);
    for (const url of reviewReads) {
      expect(url.pathname).toBe(
        `/api/rooms/${room.id}/work-items/${workId}/result-reviews`,
      );
      expect(Object.fromEntries(url.searchParams)).toEqual({
        resourceId: room.resourceId,
        commit: result.commit,
      });
    }
    await page.screenshot({
      path: info.outputPath('work-result-request-review.png'),
    });
    await requestReview.click();
    const form = library(page).getByRole('form', {
      name: 'Request document review',
      exact: true,
    });
    await expect(form.getByText(path, { exact: true })).toBeVisible();
    const title = 'Approve this Work handover';
    await form.getByLabel('Title', { exact: true }).fill(title);
    await form.getByLabel('Reviewer', { exact: true }).selectOption(user.id);
    await form
      .getByRole('button', { name: 'Request Review', exact: true })
      .click();
    await expect(reviewView(page)).toContainText(title);
    const { reviews } = await read<{ reviews: Review[] }>(
      page,
      `/reviews?roomId=${room.id}`,
    );
    expect(reviews).toHaveLength(1);
    const [review] = reviews;
    expect(review).toMatchObject({
      resourceId: room.resourceId,
      path,
      submittedCommit: result.commit,
      reviewerUserId: user.id,
      status: 'in_review',
    });

    await openApp(page, 'Work');
    await expect(
      ownerResult.getByText('In Review', { exact: true }),
    ).toBeVisible();
    const viewReview = ownerResult.getByRole('button', {
      name: `View Review for ${path}`,
      exact: true,
    });
    await expect(viewReview).toBeVisible();
    await expect(
      ownerResult.getByRole('button', {
        name: `Review File… for ${path}`,
        exact: true,
      }),
    ).toHaveCount(0);
    await openResult(reviewer, goal);
    await resultView(reviewer)
      .getByRole('button', { name: `Review File… for ${path}`, exact: true })
      .click();
    await expect(reviewView(reviewer)).toContainText(title);
    await reviewView(reviewer)
      .getByRole('button', { name: 'Approve', exact: true })
      .click();

    await expect(
      ownerResult.getByText('Approved', { exact: true }),
    ).toBeVisible();
    const delivery = ownerResult.getByRole('button', {
      name: `Open Delivery… for ${path}`,
      exact: true,
    });
    await expect(delivery).toBeInViewport();
    await page.screenshot({
      path: info.outputPath('work-result-open-delivery.png'),
    });
    await delivery.click();
    const deliver = reviewView(page).getByRole('button', {
      name: 'Deliver',
      exact: true,
    });
    await expect(deliver).toBeEnabled();
    await expect(
      reviewView(page).getByRole('button', {
        name: 'Refresh Review',
        exact: true,
      }),
    ).toHaveCount(0);
    await deliver.click();
    await expect(reviewView(page).locator('.camp-review-state')).toHaveText(
      'Delivered',
    );
    const delivered = await read<{ review: Review }>(
      page,
      `/reviews/${review.id}?view=summary`,
    );
    expect(delivered.review.deliveredCommit).toBe(result.commit);

    await openApp(page, 'Work');
    await expect(
      ownerResult.getByText('Delivered', { exact: true }),
    ).toBeVisible();
    const readsBeforeEdit = reviewReads.length;
    const newer = await mutate(
      page,
      `/resources/${room.resourceId}/files`,
      {
        requestId: randomUUID(),
        expectedCommit: result.commit,
        path,
        content: 'A newer shared file version.\n',
      },
      'PUT',
    );
    expect(newer.ok(), await newer.text()).toBeTruthy();
    const { publication } = await newer.json();
    expect(publication.commit).not.toBe(result.commit);
    await expect
      .poll(() => reviewReads.length)
      .toBeGreaterThan(readsBeforeEdit);
    await expect(viewReview).toBeVisible();
    await expect(
      ownerResult.getByText('Delivered', { exact: true }),
    ).toBeVisible();
    await expect(requestReview).toHaveCount(0);
    await expect(
      ownerResult.getByRole('button', {
        name: `Open Delivery… for ${path}`,
        exact: true,
      }),
    ).toHaveCount(0);
    await ownerResult.getByRole('button', { name: path, exact: true }).click();
    await expect(library(page)).toContainText('Historical version');
    await expect(
      library(page).getByRole('link', {
        name: `Download ${path}`,
        exact: true,
      }),
    ).toHaveAttribute('href', new RegExp(`commit=${result.commit}(?:&|$)`));
    const exactFile = await page.request.get(
      `/api/resources/${room.resourceId}/file?${new URLSearchParams({ commit: delivered.review.deliveredCommit, path })}`,
    );
    expect(exactFile.ok()).toBeTruthy();
    expect(await exactFile.body()).toEqual(originalBytes);
    const { item } = await readWork();
    expect(item).toMatchObject({
      status: 'completed',
      revision: completed.revision,
      usage: completed.usage,
    });
  } finally {
    await reviewerContext.close();
  }
});
