import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { expect, test } from '@playwright/test';
import { createRoom, library, mutate, signIn, workspace } from './team-helpers';

test('Library downloads its selected saved revision through pointer and keyboard menu links', async ({
  page,
}, info) => {
  await signIn(page);
  const room = await createRoom(page, `Saved project ${info.project.name}`);
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  const more = library(page).getByRole('button', {
    name: 'More Library actions',
    exact: true,
  });
  await expect(
    library(page).getByText('No files yet', { exact: true }),
  ).toBeVisible();
  await more.click();
  const downloadItem = page.getByRole('menuitem', {
    name: 'Download Files',
    exact: true,
  });
  await expect(downloadItem).toHaveAttribute('aria-disabled', 'true');
  await expect(downloadItem).not.toHaveAttribute('href');
  let unexpectedDownload = false;
  page.on('download', () => {
    unexpectedDownload = true;
  });
  await downloadItem.evaluate((node) => (node as HTMLElement).click());
  await expect(page.getByRole('menu')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(more).toBeFocused();
  expect(unexpectedDownload).toBe(false);

  let commit = (
    await (
      await page.request.get(`/api/resources/${room.resourceId}/files`)
    ).json()
  ).commit as string;
  const save = async (content: string) => {
    const response = await mutate(
      page,
      `/resources/${room.resourceId}/files`,
      {
        requestId: randomUUID(),
        expectedCommit: commit,
        path: 'index.html',
        content,
      },
      'PUT',
    );
    expect(response.ok()).toBe(true);
    commit = (await response.json()).publication.commit;
    return commit;
  };
  const original = await save('<h1>Saved Notes app</h1>');
  await expect(
    library(page).getByRole('heading', { name: 'index.html', exact: true }),
  ).toBeVisible();
  const originalResponse = await page.request.get(
    `/api/resources/${room.resourceId}/archive?commit=${original}`,
  );
  expect(originalResponse.ok()).toBe(true);
  const originalBytes = await originalResponse.body();
  await more.click();
  await expect(downloadItem).toHaveAttribute(
    'href',
    `/api/resources/${room.resourceId}/archive?commit=${original}`,
  );
  await expect(downloadItem).toHaveAttribute('download', '');
  await library(page).screenshot({
    path: info.outputPath('library-archive-menu.png'),
  });
  const latest = await save('<h1>Later Notes app</h1>');
  const completed = page.waitForEvent('download');
  await downloadItem.click();
  const download = await completed;
  expect(download.suggestedFilename()).toBe(
    `camp-files-${original.slice(0, 12)}.zip`,
  );
  expect(await readFile((await download.path())!)).toEqual(originalBytes);
  await expect(more).toBeFocused();
  await expect(page.getByRole('menu')).toHaveCount(0);

  const latestResponse = await page.request.get(
    `/api/resources/${room.resourceId}/archive?commit=${latest}`,
  );
  expect(latestResponse.ok()).toBe(true);
  const latestBytes = await latestResponse.body();
  expect(latestBytes).not.toEqual(originalBytes);
  await expect(
    library(page).getByRole('link', {
      name: 'Download index.html',
      exact: true,
    }),
  ).toHaveAttribute(
    'href',
    `/api/resources/${room.resourceId}/file?path=index.html&commit=${latest}`,
  );
  for (const key of ['Enter', ' ']) {
    await more.focus();
    await more.press('ArrowDown');
    await expect(downloadItem).toHaveAttribute(
      'href',
      `/api/resources/${room.resourceId}/archive?commit=${latest}`,
    );
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('ArrowDown');
    await expect(downloadItem).toHaveClass(/is-selected/);
    const complete = page.waitForEvent('download');
    await page.keyboard.press(key);
    const keyboardDownload = await complete;
    expect(await readFile((await keyboardDownload.path())!)).toEqual(
      latestBytes,
    );
    await expect(more).toBeFocused();
    await expect(page.getByRole('menu')).toHaveCount(0);
  }
});
