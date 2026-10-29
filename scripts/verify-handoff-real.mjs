// Explicit paid browser verification. At most three runs, without automatic retries.
import assert from 'node:assert/strict';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { chromium, expect } from '@playwright/test';

const [address, consent] = process.argv.slice(2);
if (!address || consent !== '--allow-model-calls')
  throw new Error(
    'Usage: node scripts/verify-handoff-real.mjs http://127.0.0.1:13312 --allow-model-calls',
  );
const base = new URL(address);
assert.equal(base.protocol, 'http:');
assert.ok(['localhost', '127.0.0.1', '[::1]'].includes(base.hostname));
assert.equal(base.username + base.password + base.search + base.hash, '');
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const reportPath = resolve(root, '.data/handoff-real-verification.json');
const tag = Date.now().toString(36);
const report = {
  startedAt: new Date().toISOString(),
  base: base.origin,
  submitted: 0,
  runs: [],
  checks: [],
};
let browser;

async function api(path, method = 'GET', body) {
  const response = await fetch(new URL(path, base), {
    method,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(15000),
  });
  if (!response.ok)
    throw new Error(
      `${method} ${path}: ${response.status} ${await response.text()}`,
    );
  return response.json();
}

async function save() {
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, JSON.stringify(report, null, 2) + '\n', {
    mode: 0o600,
  });
}

async function file(room, commit) {
  const response = await fetch(
    new URL(
      `/api/resources/${room.resourceId}/file?path=onboarding.md&commit=${commit}`,
      base,
    ),
  );
  assert.equal(response.status, 200);
  return response.text();
}

async function openWorkspace(page, room) {
  await page.goto(base.origin);
  await page
    .getByRole('menubar')
    .getByRole('menuitem', { name: 'Desktop', exact: true })
    .click();
  await page
    .getByRole('menu')
    .getByRole('menuitem', { name: 'Workspace', exact: true })
    .click();
  const window = page.locator('[data-app-id="workspace"]');
  const navigation = window.getByRole('button', { name: 'Rooms', exact: true });
  if (await navigation.isVisible()) await navigation.click();
  await window
    .getByRole('navigation', { name: 'Rooms', exact: true })
    .getByRole('button', { name: new RegExp(room.name) })
    .click();
  return window;
}

async function submit(page, window, composer, room, agent) {
  assert.ok(report.submitted < 3, 'The three-run limit was reached.');
  report.submitted++;
  await save();
  const receipt = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname === `/api/rooms/${room.id}/messages`,
  );
  await composer.getByRole('button', { name: 'Send', exact: true }).click();
  const response = await receipt;
  assert.ok(response.ok(), await response.text());
  const accepted = await response.json();
  assert.equal(accepted.runs.length, 1);
  assert.equal(accepted.runs[0].agentId, agent.id);
  const id = accepted.runs[0].id;
  const entry = { acceptedMessage: accepted.message, id, agent: agent.name };
  report.runs.push(entry);
  await save();
  const deadline = Date.now() + 210000;
  let activeScreenshot = false;
  do {
    entry.run = (await api(`/api/runs/${id}`)).run;
    if (!activeScreenshot && entry.run.status === 'running') {
      await page.screenshot({
        path: resolve(
          root,
          `.data/handoff-real-${report.submitted}-working.png`,
        ),
      });
      activeScreenshot = true;
    }
    if (
      ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(
        entry.run.status,
      )
    )
      break;
    await delay(1000);
  } while (Date.now() < deadline);
  const { messages } = await api(
    `/api/rooms/${room.id}/messages?threadRootId=${accepted.message.threadRootId}`,
  );
  const replies = messages.filter(
    (message) => message.runId === id && message.authorKind === 'agent',
  );
  entry.reply = replies[0];
  entry.events = (await api(`/api/runs/${id}/events`)).events;
  entry.observedModelSteps = entry.events.filter(
    (event) => event.type === 'model',
  ).length;
  await save();
  console.log(
    `${agent.name}: ${entry.run.status}; ${entry.run.files.length} changed files; ${entry.run.references.length} references.`,
  );
  assert.equal(entry.run.status, 'succeeded', entry.run.error);
  assert.equal(replies.length, 1);
  const article = window.locator(`[data-message-id="${entry.reply.id}"]`);
  await expect(article.getByText('Completed', { exact: true })).toBeVisible({
    timeout: 15000,
  });
  return entry;
}

async function choose(composer, name) {
  await composer.getByRole('button', { name: /^To:/ }).click();
  await composer.getByRole('checkbox', { name, exact: true }).check();
  await composer.getByRole('button', { name: /^To:/ }).click();
}

try {
  const status = await api('/api/workspace/status');
  assert.equal(status.available, true, status.reason);
  assert.deepEqual(status.budget, {
    maxTokens: 4096,
    maxSteps: 8,
    timeoutSeconds: 180,
  });
  report.budget = status.budget;
  const room = (
    await api('/api/rooms', 'POST', {
      name: `onboarding-${tag}`,
      description: 'Write, review, and improve a customer onboarding guide.',
    })
  ).room;
  report.room = room;
  const writer = (
    await api('/api/agents', 'POST', {
      name: 'Writer',
      handle: `editorial-writer-${tag}`,
      icon: 'document',
      access: 'write',
      enabled: true,
      instructions:
        'Write practical business documents and revise them from explicit feedback. Read the actual current files and preserve relevant work. Be concise and report actual results.',
    })
  ).agent;
  const reviewer = (
    await api('/api/agents', 'POST', {
      name: 'Reviewer',
      handle: `editorial-reviewer-${tag}`,
      icon: 'checklist',
      access: 'read',
      enabled: true,
      instructions:
        'Review the exact evidence selected by the user. Cite concrete details, distinguish suggestions from completed work, and do not modify files.',
    })
  ).agent;
  for (const agent of [writer, reviewer])
    await api(`/api/rooms/${room.id}/members/${agent.id}`, 'PUT', {});
  await api(`/api/rooms/${room.id}`, 'PATCH', { defaultAgentId: writer.id });
  await api(`/api/rooms/${room.id}/members/${room.defaultAgentId}`, 'DELETE');
  report.agents = [writer, reviewer];
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
    acceptDownloads: true,
  });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const window = await openWorkspace(page, room);
  const composer = window.getByRole('form', { name: 'Room composer' });
  await composer
    .getByRole('textbox')
    .fill(
      'Create onboarding.md: a practical checklist for a small B2B team onboarding its first customer. Include a goal, three preparation steps with an owner for each, and a success measure. Keep it under 180 words. Batch writing and reading back the file in one shell call, then summarize briefly. No dependencies or tests are needed.',
    );
  await page.reload();
  await expect(composer.getByRole('textbox')).toHaveValue(
    /Create onboarding.md/,
  );
  const first = await submit(page, window, composer, room, writer);
  assert.equal(first.run.published, true);
  const original = await file(room, first.run.resultCommit);
  assert.ok(original.length > 100);
  await window
    .locator(`[data-message-id="${first.reply.id}"]`)
    .getByRole('button', { name: 'Ask agent…', exact: true })
    .click();
  const threadComposer = window.getByRole('form', { name: 'Thread composer' });
  await threadComposer
    .getByRole('textbox')
    .fill(
      'Review the exact attached onboarding.md for practical readiness. Read the pinned file, quote one short detail, identify two concrete gaps or ambiguities, and propose actionable improvements. Focus on clear ownership, relative deadlines, and measurable completion criteria. Do not modify files; keep your review under 150 words.',
    );
  await expect(
    threadComposer.getByRole('button', { name: 'Send', exact: true }),
  ).toBeDisabled();
  await choose(threadComposer, 'Reviewer');
  await expect(threadComposer.getByText('References · 2 / 8')).toBeVisible();
  await threadComposer
    .locator('summary')
    .filter({ hasText: 'onboarding.md' })
    .last()
    .click();
  const pinnedDownload = page.waitForEvent('download');
  await threadComposer
    .getByRole('link', { name: 'Download pinned file' })
    .click();
  assert.equal(
    await readFile(await (await pinnedDownload).path(), 'utf8'),
    original,
  );
  await page.screenshot({
    path: resolve(root, '.data/handoff-real-review-composer.png'),
  });
  await page.reload();
  await expect(threadComposer.getByRole('textbox')).toHaveValue(
    /Review the exact attached/,
  );
  await expect(threadComposer.getByText('References · 2 / 8')).toBeVisible();
  const second = await submit(page, window, threadComposer, room, reviewer);
  assert.equal(second.run.threadRootId, first.reply.id);
  assert.equal(second.run.files.length, 0);
  assert.equal(second.run.published, false);
  assert.equal(
    second.run.references.find((reference) => reference.kind === 'file').commit,
    first.run.resultCommit,
  );
  await window
    .locator(`[data-message-id="${second.reply.id}"]`)
    .getByRole('button', { name: 'Ask agent…', exact: true })
    .click();
  await threadComposer
    .getByRole('textbox')
    .fill(
      'Apply the selected Reviewer feedback to the current onboarding.md. Read the current document, preserve its useful content, and add concrete relative deadlines, named role ownership, and measurable completion criteria where needed. Keep the guide under 220 words. Batch the edit and read-back, then summarize what you actually changed. No tests or dependency installation are needed.',
    );
  await choose(threadComposer, 'Writer');
  await expect(threadComposer.getByText('References · 1 / 8')).toBeVisible();
  const third = await submit(page, window, threadComposer, room, writer);
  assert.equal(third.run.threadRootId, first.reply.id);
  assert.equal(third.run.references[0].messageId, second.reply.id);
  assert.equal(third.run.published, true);
  assert.equal(third.run.baselineCommit, first.run.resultCommit);
  const revised = await file(room, third.run.resultCommit);
  assert.notEqual(revised, original);
  assert.equal(await file(room, first.run.resultCommit), original);
  const current = (await api('/api/resources')).resources.find(
    (resource) => resource.id === room.resourceId,
  );
  assert.equal(current.currentCommit, third.run.resultCommit);
  await page.reload();
  await expect(
    window.locator(`[data-message-id="${third.reply.id}"]`),
  ).toBeVisible();
  await page.screenshot({
    path: resolve(root, '.data/handoff-real-desktop-result.png'),
  });
  const touchContext = await browser.newContext({
    viewport: { width: 390, height: 844 },
    isMobile: true,
    hasTouch: true,
    deviceScaleFactor: 2,
  });
  const touchPage = await touchContext.newPage();
  const touchWindow = await openWorkspace(touchPage, room);
  await touchWindow
    .locator(`[data-message-id="${first.reply.id}"]`)
    .getByRole('button', { name: /^Open thread/ })
    .click();
  await expect(
    touchWindow.locator(`[data-message-id="${third.reply.id}"]`),
  ).toBeVisible();
  await touchPage.screenshot({
    path: resolve(root, '.data/handoff-real-touch-result.png'),
  });
  assert.deepEqual(errors, []);
  report.documents = { original, revised };
  report.checks.push(
    'Three explicit browser submissions completed in one review thread.',
    'Pinned download matches original bytes before and after revision.',
    'Reviewer made no changes; Writer published a revision from the current baseline.',
    'Draft and references survived reload; desktop and touch results are visible without browser exceptions.',
  );
  report.finishedAt = new Date().toISOString();
  console.log(`Real handoff verification passed. Report: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  console.error(report.error);
  process.exitCode = 1;
} finally {
  await browser?.close();
  await save();
}
