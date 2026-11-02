import { randomUUID } from 'node:crypto';
import { expect, request, type Page } from '@playwright/test';
import {
  createRoom,
  library,
  mutate,
  newDocument,
  openApp,
  signIn,
  workspace,
} from './team-helpers';

export async function systemEventScene(page: Page, suffix: string) {
  await signIn(page);
  const { user } = await (await page.request.get('/api/auth/status')).json();
  const { avatars } = await (await page.request.get('/api/avatars')).json();
  const memberAvatar = avatars.find(
    (avatar: { kind: string }) => avatar.kind === 'member',
  ).id as string;
  const agentAvatar = avatars.find(
    (avatar: { kind: string }) => avatar.kind === 'agent',
  ).id as string;
  expect(
    (
      await mutate(
        page,
        '/me/avatar',
        { avatar: memberAvatar, expectedAvatar: user.avatar },
        'PATCH',
      )
    ).ok(),
  ).toBeTruthy();
  const { agents } = await (await page.request.get('/api/agents')).json();
  for (const agent of agents) {
    if (agent.name === 'Assistant' && agent.avatar !== agentAvatar)
      expect(
        (
          await mutate(
            page,
            `/agents/${agent.id}`,
            { avatar: agentAvatar, revision: agent.revision },
            'PATCH',
          )
        ).ok(),
      ).toBeTruthy();
  }
  const { members } = await (
    await page.request.get('/api/team/members')
  ).json();
  let reviewer = members.find(
    (person: { name: string }) => person.name === 'Morgan',
  );
  if (!reviewer) {
    const invitation = await (
      await mutate(page, '/team/invites', { expiresInHours: 1 })
    ).json();
    const invited = await request.newContext({
      baseURL: new URL(page.url()).origin,
    });
    try {
      const result = await invited.post('/api/auth/accept-invite', {
        headers: { Origin: new URL(page.url()).origin },
        data: {
          token: invitation.token,
          username: `event-reviewer-${randomUUID().slice(0, 8)}`,
          name: 'Morgan',
          password: 'A private synthetic reviewer password',
        },
      });
      expect(result.ok()).toBeTruthy();
      reviewer = (await result.json()).user;
    } finally {
      await invited.dispose();
    }
  }
  const room = await createRoom(page, `Launch review ${suffix}`);
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();
  await newDocument(
    page,
    'launch-plan.md',
    '# Launch plan\n\nStart with a small customer pilot.\nOwner: Morgan.\n',
  );
  await library(page)
    .getByRole('button', { name: 'Publish Changes', exact: true })
    .click();
  await expect(
    library(page).getByText('Published to shared files.', { exact: true }),
  ).toBeVisible();
  await library(page)
    .getByRole('button', { name: 'Back to Preview', exact: true })
    .click();
  const file = await (
    await page.request.get(`/api/resources/${room.resourceId}/files`)
  ).json();
  expect(
    (
      await mutate(page, `/rooms/${room.id}/agents`, {
        name: 'Writer',
        handle: `writer-${randomUUID().slice(0, 8)}`,
        icon: 'document',
        avatar: agentAvatar,
        access: 'write',
        enabled: true,
      })
    ).ok(),
  ).toBeTruthy();
  expect(
    (
      await mutate(page, `/rooms/${room.id}/messages`, {
        requestId: randomUUID(),
        content:
          'The launch plan is ready. Please check this version before sharing.',
        postOnly: true,
      })
    ).ok(),
  ).toBeTruthy();
  const input = {
    resourceId: room.resourceId,
    path: 'launch-plan.md',
    commit: file.commit,
  };
  const self = await mutate(page, `/rooms/${room.id}/reviews`, {
    ...input,
    requestId: randomUUID(),
    title: 'Personal final check',
    reviewerUserId: user.id,
  });
  expect(self.ok()).toBeTruthy();
  const selfReview = (await self.json()).review;
  const other = await mutate(page, `/rooms/${room.id}/reviews`, {
    ...input,
    requestId: randomUUID(),
    title: 'Launch plan',
    reviewerUserId: reviewer.id,
  });
  expect(other.ok()).toBeTruthy();
  const review = (await other.json()).review;
  await openApp(page, 'Workspace');
  await expect(
    workspace(page).locator(`[data-message-id="${review.threadRootId}"]`),
  ).toBeVisible();
  await workspace(page)
    .locator('.workspace-message-scroll')
    .evaluate((node) => {
      node.scrollTop = 0;
    });
  return {
    room,
    review,
    selfReview,
    commit: file.commit as string,
    user,
    memberAvatar,
  };
}
