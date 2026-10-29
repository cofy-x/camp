// Actual UI journey with two explicit paid sends. No API mutations or prompt
// retries: the UI's own full request is journaled before network dispatch.
import { chromium, expect } from '@playwright/test';
import { createHash, randomUUID } from 'node:crypto';
import { mkdir, rename, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { setTimeout as pause } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import {
  normalizeOrigin,
  privateJSON,
  readAccess,
  runSummary,
  syncDirectory,
  validatePreflight,
} from './verify-remote-real.mjs';

const usage =
  'Usage: node scripts/demo-remote-ui.mjs --allow-two-real-runs ORIGIN ACCESS_JSON RUNNER_ID_OR_NAME SOURCE_ALIAS REPORT_DIR';
const terminal = new Set(['succeeded', 'failed', 'cancelled', 'interrupted']);

class DemoError extends Error {}

function check(value, message) {
  if (!value) throw new DemoError(message);
}

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

function escapeHTML(value) {
  return value.replace(
    /[&<>"']/g,
    (character) =>
      ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[
        character
      ],
  );
}

export async function main(args = process.argv.slice(2)) {
  check(args.length === 6 && args[0] === '--allow-two-real-runs', usage);
  check(
    process.env.NODE_TLS_REJECT_UNAUTHORIZED !== '0',
    'TLS verification must remain enabled.',
  );
  const origin = normalizeOrigin(args[1]);
  const access = await readAccess(path.resolve(args[2]), origin);
  const output = path.resolve(args[5]);
  await mkdir(output, { recursive: true, mode: 0o700 });
  const outputInfo = await stat(output);
  check(
    process.platform === 'win32' ||
      ((outputInfo.mode & 0o077) === 0 && outputInfo.uid === process.getuid()),
    'The demo report directory must be private to this user (chmod 700).',
  );
  const ledgerFile = path.join(output, 'ledger.json');
  const ledger = {
    schema: 1,
    origin,
    allowedRuns: 2,
    startedAt: new Date().toISOString(),
    state: 'starting',
    requests: [],
  };
  // Permanent exclusive claim. A failed demo is inspected, never automatically
  // restarted with a new Room or new model requests.
  try {
    await privateJSON(ledgerFile, ledger);
    await syncDirectory(output);
  } catch {
    throw new Error(
      'Cannot claim this report directory. Preserve any existing ledger; a new directory requires a new explicit two-run budget.',
    );
  }
  const report = {
    schema: 1,
    origin,
    status: 'pending',
    screenshots: [],
    runs: [],
    checks: [],
    pageErrorCount: 0,
  };
  let stage = 'sign-in';
  let browser;
  let recordingContext;
  let page;
  let signedIn = false;
  let armed = null;
  let dispatchError = false;

  async function save() {
    for (const [name, value] of [
      ['ledger.json', ledger],
      ['report.json', report],
    ]) {
      const filename = path.join(output, name);
      await privateJSON(`${filename}.next`, value);
      await rename(`${filename}.next`, filename);
    }
    await syncDirectory(output);
  }

  async function snapshot(caption) {
    const filename = `${String(report.screenshots.length + 1).padStart(2, '0')}.png`;
    await page.screenshot({ path: path.join(output, filename) });
    report.screenshots.push({
      filename,
      caption,
      at: new Date().toISOString(),
      viewport: page.viewportSize(),
    });
    await save();
    await writeFile(
      path.join(output, 'storyboard.html'),
      `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>Camp remote UI demo</title><style>body{font:16px system-ui;max-width:1440px;margin:24px auto;padding:0 16px}figure{margin:24px 0}img,video{max-width:100%;border:1px solid #999}figcaption{margin:8px 0}</style><h1>Camp 远程执行与交付演示</h1><video controls preload="metadata" src="demo.webm"></video>${report.screenshots.map((item) => `<figure><figcaption>${escapeHTML(item.caption)}</figcaption><a href="${item.filename}"><img src="${item.filename}" alt="${escapeHTML(item.caption)}"></a></figure>`).join('')}</html>`,
      { mode: 0o600 },
    );
  }

  try {
    browser = await chromium.launch();
    const loginContext = await browser.newContext({ baseURL: origin });
    const login = await loginContext.newPage();
    await login.goto(origin);
    await login.getByLabel('Username', { exact: true }).fill(access.username);
    await login.getByLabel(/^Password/).fill(access.password);
    await login.getByRole('button', { name: 'Sign In', exact: true }).click();
    await expect(
      login.locator('[data-window-id="workspace:main"]'),
    ).toBeVisible();
    // Keep authentication in memory. The password form, cookies, traces and
    // network bodies are never included in video/artifacts.
    const state = await loginContext.storageState();
    await loginContext.close();
    recordingContext = await browser.newContext({
      baseURL: origin,
      viewport: { width: 1440, height: 1000 },
      storageState: state,
      recordVideo: { dir: output, size: { width: 1440, height: 1000 } },
    });
    page = await recordingContext.newPage();
    page.setDefaultTimeout(20_000);
    page.on('pageerror', () => {
      report.pageErrorCount++;
    });
    const workspace = page.locator('[data-window-id="workspace:main"]');
    const library = page.locator('[data-window-id="library:main"]');
    const reviewPanel = workspace.getByRole('region', {
      name: 'Document review',
      exact: true,
    });
    await page.route('**/api/rooms/*/messages', async (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      try {
        const body = route.request().postDataJSON();
        check(
          armed &&
            armed.state === 'reserved' &&
            new URL(route.request().url()).pathname === armed.route &&
            body.content === armed.prompt &&
            body.threadRootId === '' &&
            body.runnerRevision === armed.runnerRevision &&
            body.recipientAgentIds?.length === 1 &&
            body.recipientAgentIds[0] === armed.agentId &&
            typeof body.requestId === 'string' &&
            body.requestId.length > 0 &&
            ledger.requests.every(
              (request) =>
                request === armed || request.body?.requestId !== body.requestId,
            ) &&
            !body.postOnly,
          'Unexpected message submission was blocked.',
        );
        if (armed.feedbackMessageId)
          check(
            body.references?.some(
              (reference) =>
                reference.kind === 'message' &&
                reference.messageId === armed.feedbackMessageId,
            ),
            'Review feedback reference was not attached.',
          );
        armed.body = body;
        armed.state = 'dispatched-outcome-unknown';
        await save();
        await route.continue();
      } catch {
        dispatchError = true;
        await route.abort('blockedbyclient');
      }
    });

    async function get(route, text = false) {
      const response = await page.request.get(route, {
        timeout: 30_000,
        maxRedirects: 0,
      });
      check(response.ok(), 'A read-only API observation failed.');
      return text ? response.text() : response.json();
    }

    async function openApp(name) {
      await page
        .getByRole('menubar')
        .getByRole('menuitem', { name: 'Desktop', exact: true })
        .click();
      await page
        .getByRole('menu')
        .getByRole('menuitem', { name, exact: true })
        .click();
    }

    async function file(resourceId, commit, filename) {
      check(/^[0-9a-f]{40}$/.test(commit), 'An immutable commit is required.');
      return get(
        `/api/resources/${resourceId}/file?${new URLSearchParams({ path: filename, commit })}`,
        true,
      );
    }

    await page.goto(origin);
    await expect(workspace).toBeVisible();
    signedIn = true;
    const { user } = await get('/api/auth/status');
    check(
      user?.role === 'admin' && user.enabled,
      'An enabled administrator must sign in.',
    );
    const matches = (await get('/api/runners')).runners.filter(
      (runner) => runner.id === args[3] || runner.name === args[3],
    );
    check(matches.length === 1, 'Choose one unambiguous Runner ID or name.');
    const runnerId = matches[0].id;
    const configuration = await get('/api/settings/models');
    const budget = validatePreflight(configuration, matches[0]);
    const sources = (
      await get(`/api/runners/${runnerId}/sources`)
    ).sources.filter(
      (source) => source.id === args[4] || source.alias === args[4],
    );
    check(
      sources.length === 1 && sources[0].available,
      'Choose one available registered source alias.',
    );
    const source = sources[0];
    ledger.runnerId = report.runnerId = runnerId;
    ledger.configurationRevision = configuration.revision;
    report.budget = budget;
    const roomName = `Camp UI 演示 ${randomUUID().slice(0, 8)}`;
    ledger.roomName = report.roomName = roomName;
    await save();

    stage = 'source-import';
    await openApp('Library');
    const drawer = library.getByRole('button', {
      name: 'Sources',
      exact: true,
    });
    if (await drawer.isVisible()) await drawer.click();
    await library
      .getByRole('button', { name: 'Import from Runner…', exact: true })
      .click();
    const panel = library.getByRole('region', {
      name: 'Import from Runner',
      exact: true,
    });
    await panel
      .getByLabel('Source Runner', { exact: true })
      .selectOption(runnerId);
    await panel
      .getByLabel('Registered directory', { exact: true })
      .selectOption(source.id);
    await panel.getByLabel('New Room name', { exact: true }).fill(roomName);
    await snapshot('1. 从 Runner 已授权来源导入独立资料快照');
    await panel
      .getByRole('button', { name: 'Import Snapshot', exact: true })
      .click();
    await expect(panel).toContainText('Room created', { timeout: 360_000 });
    const imported = (await get('/api/source-imports')).sourceImports.find(
      (value) => value.roomName === roomName && value.runnerId === runnerId,
    );
    check(
      imported?.status === 'succeeded' && imported.roomId,
      'The source import did not create a Room.',
    );
    const room = (await get('/api/rooms')).rooms.find(
      (value) => value.id === imported.roomId,
    );
    check(
      room && room.runnerId === runnerId,
      'The imported Room must target the source Runner.',
    );
    ledger.roomId = report.roomId = room.id;
    report.resourceId = room.resourceId;
    report.sourceImportId = imported.id;
    report.sourceCommit = imported.commit;
    const sourceFiles = await get(`/api/resources/${room.resourceId}/files`);
    check(
      sourceFiles.files.includes('brief.md') &&
        !sourceFiles.files.includes('onboarding.md'),
      'Demo source must contain brief.md and no onboarding.md.',
    );
    const brief = await file(room.resourceId, sourceFiles.commit, 'brief.md');
    check(brief.trim().length > 0, 'The imported brief is empty.');
    report.briefSHA256 = sha256(brief);
    await snapshot('2. 快照导入完成，创建新的演示 Room');
    await panel.getByRole('button', { name: 'Open Room', exact: true }).click();
    await expect(
      workspace.getByLabel(`Message ${roomName}`, { exact: true }),
    ).toBeVisible();
    const members = (await get(`/api/rooms/${room.id}/members`)).members;
    const agent = members.find(
      (value) =>
        value.id === room.defaultAgentId &&
        value.enabled &&
        value.access === 'write',
    );
    check(agent, 'The imported Room needs an enabled default writing Agent.');
    ledger.agentId = report.agentId = agent.id;
    await save();

    async function send(prompt, feedbackMessageId = '') {
      const settings = await get('/api/settings/models');
      const runner = (await get('/api/runners')).runners.find(
        (value) => value.id === runnerId,
      );
      validatePreflight(settings, runner, configuration.revision);
      check(ledger.requests.length < 2, 'The two-run budget is exhausted.');
      const composer = workspace.getByRole('form', {
        name: 'Room composer',
        exact: true,
      });
      await expect(composer).toBeVisible();
      await composer.getByRole('button', { name: /^To:/ }).click();
      const choices = composer.getByRole('checkbox');
      for (let index = 0; index < (await choices.count()); index++)
        await choices.nth(index).uncheck();
      await composer
        .getByRole('checkbox', { name: agent.name, exact: true })
        .check();
      await composer.getByRole('button', { name: /^To:/ }).click();
      await composer
        .getByLabel(`Message ${roomName}`, { exact: true })
        .fill(prompt);
      const request = {
        intentId: randomUUID(),
        reservedAt: new Date().toISOString(),
        route: `/api/rooms/${room.id}/messages`,
        prompt,
        runnerRevision: room.runnerRevision,
        agentId: agent.id,
        feedbackMessageId,
        state: 'reserved',
      };
      ledger.requests.push(request);
      armed = request;
      await save();
      await snapshot(
        `第 ${ledger.requests.length} 次任务：明确选择 Agent，检查内容后发送`,
      );
      const responsePromise = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === request.route &&
          response.request().method() === 'POST',
        { timeout: 45_000 },
      );
      // Attach rejection immediately: a failing click must not leave an
      // unhandled response timeout or trigger a second click.
      void responsePromise.catch(() => {});
      await composer
        .getByRole('button', { name: 'Send to Agent', exact: true })
        .click();
      const response = await responsePromise;
      armed = null;
      check(
        !dispatchError && response.ok(),
        'The UI send was not confirmed; inspect the preserved ledger.',
      );
      const accepted = await response.json();
      check(accepted.runs?.length === 1, 'Expected one explicit Agent Run.');
      let run = accepted.runs[0];
      request.runId = run.id;
      request.state = 'accepted';
      await save();
      check(
        run.runnerId === runnerId &&
          run.agentId === agent.id &&
          Object.entries(budget).every(([key, value]) => run[key] === value),
        'The accepted Run changed Runner, Agent or budget.',
      );
      const deadline = Date.now() + (budget.timeoutSeconds + 360) * 1000;
      let workingCaptured = false;
      while (!terminal.has(run.status) && Date.now() < deadline) {
        if (!workingCaptured && ['running', 'checking'].includes(run.status)) {
          await snapshot(
            `第 ${ledger.requests.length} 次任务：远端 Agent 正在执行`,
          );
          workingCaptured = true;
        }
        await pause(1000);
        ({ run } = await get(`/api/runs/${run.id}`));
      }
      request.state = terminal.has(run.status)
        ? 'terminal'
        : 'observation-timeout';
      request.run = runSummary(run);
      report.runs.push(request.run);
      await save();
      check(
        run.status === 'succeeded' && run.published === true,
        'The Run did not publish successfully; no prompt will be retried.',
      );
      await snapshot(`第 ${ledger.requests.length} 次任务：文件已发布到 Camp`);
      return run;
    }

    stage = 'first-agent-task';
    const first = await send(
      '请先使用工具读取工作区 brief.md，依据这份资料创建中文 onboarding.md。标题为“Camp 团队上手指南”，包含“## 目标”“## 操作流程”“## 验收标准”三个章节，内容简洁、具体，不编造资料中没有的承诺。必须实际写入文件并读回检查；不要修改 brief.md 或其他文件。此版本暂不添加 Recovery 章节。',
    );
    const firstText = await file(
      room.resourceId,
      first.resultCommit,
      'onboarding.md',
    );
    check(
      firstText.includes('## 目标') &&
        firstText.includes('## 操作流程') &&
        firstText.includes('## 验收标准') &&
        !firstText.includes('## Recovery'),
      'First draft did not match the requested document structure.',
    );
    check(
      (await file(room.resourceId, first.resultCommit, 'brief.md')) === brief,
      'The original brief changed.',
    );
    report.firstFile = {
      commit: first.resultCommit,
      sha256: sha256(firstText),
    };

    stage = 'request-review';
    await workspace
      .getByRole('button', { name: 'Open onboarding.md', exact: true })
      .click();
    await expect(
      workspace.getByRole('region', {
        name: 'Preview onboarding.md',
        exact: true,
      }),
    ).toContainText('操作流程');
    await snapshot('5. 从任务结果展开 onboarding.md 阅读正文');
    await workspace
      .getByRole('button', { name: 'Open in Library', exact: true })
      .click();
    await expect(
      library.getByRole('region', {
        name: 'Preview onboarding.md',
        exact: true,
      }),
    ).toContainText('操作流程');
    await snapshot('5. 在 Library 阅读实际生成的上手指南');
    await library
      .getByRole('button', { name: 'Request Review…', exact: true })
      .click();
    const reviewRequest = library.getByRole('form', {
      name: 'Request document review',
      exact: true,
    });
    const title = `${roomName} · 上手指南审核`;
    await reviewRequest.getByLabel('Title', { exact: true }).fill(title);
    await reviewRequest
      .getByLabel('Reviewer', { exact: true })
      .selectOption(user.id);
    await reviewRequest
      .getByRole('button', { name: 'Request Review', exact: true })
      .click();
    await expect(reviewPanel).toBeVisible();
    let review = (await get(`/api/reviews?roomId=${room.id}`)).reviews.find(
      (value) => value.title === title,
    );
    check(review?.status === 'in_review', 'Review request was not confirmed.');
    ledger.reviewId = report.reviewId = review.id;
    await reviewPanel
      .getByLabel('Review comment', { exact: true })
      .fill(
        '请增加“## Recovery”章节，明确断线后先查看当前任务状态、取消后等待清理确认，以及失败后由人明确重新发送，不能自动重复任务。保持其余章节和 brief.md 不变。',
      );
    await snapshot('6. 人工审核提出明确修改意见');
    await reviewPanel
      .getByRole('button', { name: 'Request Changes', exact: true })
      .click();
    await expect(
      reviewPanel.getByRole('button', { name: 'Ask agent…', exact: true }),
    ).toBeVisible();
    const changed = await get(`/api/reviews/${review.id}`);
    check(
      changed.review.status === 'changes_requested',
      'The change request was not committed.',
    );
    const feedback = changed.events
      .filter((event) => event.action === 'request_changes')
      .at(-1);
    check(
      feedback?.messageId,
      'Review feedback must identify its Room message.',
    );
    await reviewPanel
      .getByRole('button', { name: 'Ask agent…', exact: true })
      .click();

    stage = 'second-agent-task';
    const second = await send(
      '请按附带的人工审核意见修改 onboarding.md：保留原有内容，并新增“## Recovery”章节，分别说明断线后的状态确认、取消后的清理确认，以及失败后由人明确重新发送任务。必须实际修改文件并读回检查。不要修改 brief.md 或其他文件。',
      feedback.messageId,
    );
    check(
      second.sessionMode === 'resume' &&
        second.sessionId === first.sessionId &&
        second.sessionGeneration === first.sessionGeneration &&
        second.baselineCommit === first.resultCommit,
      'The second task did not resume the same Room native session.',
    );
    const secondText = await file(
      room.resourceId,
      second.resultCommit,
      'onboarding.md',
    );
    check(
      secondText.includes('## Recovery') && secondText !== firstText,
      'The revised document is missing Recovery.',
    );
    check(
      (await file(room.resourceId, second.resultCommit, 'brief.md')) === brief,
      'The original brief changed.',
    );
    report.secondFile = {
      commit: second.resultCommit,
      sha256: sha256(secondText),
    };
    report.checks.push(
      'imported-brief-preserved',
      'two-explicit-ui-sends',
      'same-room-native-session-resume',
      'review-message-reference',
    );

    stage = 'approve-and-deliver';
    await workspace
      .getByRole('button', { name: 'Reviews', exact: true })
      .click();
    await workspace
      .getByRole('region', { name: 'Document reviews', exact: true })
      .getByRole('button')
      .filter({ hasText: title })
      .click();
    await expect(
      reviewPanel.getByRole('button', {
        name: 'Submit New Version',
        exact: true,
      }),
    ).toBeEnabled();
    await reviewPanel
      .getByRole('button', { name: 'Submit New Version', exact: true })
      .click();
    await expect(
      reviewPanel.getByRole('button', { name: 'Approve', exact: true }),
    ).toBeEnabled();
    await snapshot('8. 提交新版本，人工重新确认修改结果');
    await reviewPanel
      .getByRole('button', { name: 'Approve', exact: true })
      .click();
    await reviewPanel
      .getByRole('button', { name: 'Deliver', exact: true })
      .click();
    await expect(reviewPanel).toContainText('Delivered');
    await expect(reviewPanel).toContainText('Recovery');
    ({ review } = await get(`/api/reviews/${review.id}`));
    check(
      review.status === 'delivered' &&
        review.deliveredCommit === second.resultCommit,
      'Delivery did not retain the revised immutable commit.',
    );
    check(
      (await file(room.resourceId, first.resultCommit, 'onboarding.md')) ===
        firstText &&
        (await file(
          room.resourceId,
          review.deliveredCommit,
          'onboarding.md',
        )) === secondText,
      'Immutable original or delivered content changed.',
    );
    report.deliveredCommit = review.deliveredCommit;
    report.checks.push(
      'human-request-changes-resubmit-approve-deliver',
      'immutable-original-and-delivered-file',
    );
    await snapshot('9. 完成审核交付，交付版本保持固定');
    await page.setViewportSize({ width: 390, height: 844 });
    await snapshot('10. 窄屏查看已交付版本');
    report.narrowHorizontalOverflow = await page.evaluate(() =>
      Math.max(
        0,
        globalThis.document.documentElement.scrollWidth - globalThis.innerWidth,
      ),
    );
    check(
      report.pageErrorCount === 0 && report.narrowHorizontalOverflow <= 1,
      'Browser errors or horizontal overflow were observed.',
    );
    report.status = ledger.state = 'passed';
    report.finishedAt = new Date().toISOString();
    await save();
    console.log(`UI demo passed. Private storyboard and report: ${output}`);
  } catch (error) {
    report.status = ledger.state = 'stopped';
    report.stoppedAtStage = stage;
    report.reason =
      error instanceof DemoError
        ? error.message
        : 'A browser or local operation failed; inspect the private storyboard and ledger.';
    await save().catch(() => {});
    if (signedIn)
      await snapshot(`流程停止：${stage}；保留记录，未自动重发任务`).catch(
        () => {},
      );
    throw new Error(
      `UI demo stopped at ${stage}. Inspect Camp and the private ledger; do not repeat uncertain paid prompts.`,
      { cause: error },
    );
  } finally {
    const video = page?.video();
    await recordingContext?.close();
    await browser?.close();
    if (video) {
      await rename(await video.path(), path.join(output, 'demo.webm'));
      report.video = 'demo.webm';
      await save();
    }
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    await main();
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
