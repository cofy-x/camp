// Explicit paid acceptance: one durable authorization, at most 6 execution
// rounds and 24 model requests. No automatic resubmission or budget expansion.
import { randomUUID, createHash } from 'node:crypto';
import { mkdir, rename } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { setTimeout as pause } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import process from 'node:process';
import {
  client,
  normalizeOrigin,
  privateJSON,
  readAccess,
  runSummary,
  syncDirectory,
  validatePreflight,
} from './verify-remote-real.mjs';

function check(value, message) {
  if (!value) throw new Error(message);
}

export async function main(args = process.argv.slice(2)) {
  const [flag, address, accessFile, runnerA, runnerB, reportPath] = args;
  check(
    flag === '--allow-one-real-task' && args.length === 6,
    'Usage: node scripts/verify-collaboration-real.mjs --allow-one-real-task ORIGIN ACCESS_JSON RUNNER_A RUNNER_B NEW_REPORT_DIR',
  );
  check(
    runnerA !== runnerB &&
      [runnerA, runnerB].every((id) => /^[a-zA-Z0-9_-]{1,128}$/.test(id)),
    'Choose two distinct native Runner IDs.',
  );
  const origin = normalizeOrigin(address);
  const access = await readAccess(accessFile, origin);
  const reportDir = resolve(reportPath);
  await mkdir(reportDir, { mode: 0o700 });
  const reportFile = join(reportDir, 'report.json');
  const report = {
    schema: 1,
    origin,
    startedAt: new Date().toISOString(),
    status: 'preparing',
    maxTasks: 1,
    maxTurns: 6,
    maxModelRequests: 24,
    runnerIds: [runnerA, runnerB],
    checks: [],
  };
  await privateJSON(reportFile, report);

  async function save() {
    const temporary = `${reportFile}.next`;
    await privateJSON(temporary, report);
    await rename(temporary, reportFile);
    await syncDirectory(reportDir);
  }

  const api = client(origin);
  try {
    const { user } = await api('/api/auth/login', 'POST', access);
    check(
      user.role === 'admin' && user.enabled,
      'Use an enabled administrator.',
    );
    const settings = await api('/api/settings/models');
    const { runners } = await api('/api/runners');
    for (const id of [runnerA, runnerB]) {
      validatePreflight(
        settings,
        runners.find((r) => r.id === id),
      );
    }
    report.modelRevision = settings.revision;
    const tag = randomUUID().slice(0, 8);
    const { room } = await api('/api/rooms', 'POST', {
      name: `Native collaboration ${tag}`,
      description:
        'One explicitly bounded real-model collaboration across two Linux Runners.',
    });
    report.roomId = room.id;
    report.roomName = room.name;
    report.resourceId = room.resourceId;
    await save();
    const expected =
      '# Camp Collaboration\nStatus: reviewed\nExecution: two native runners\n';
    await api(`/api/resources/${room.resourceId}`, 'PATCH', {
      environment: {
        kind: 'command',
        checks: [
          {
            name: 'collaboration-output',
            arguments: [
              'bash',
              '-c',
              "test -f collaboration.md && test \"$(wc -l < collaboration.md)\" -eq 3 && grep -qxF '# Camp Collaboration' collaboration.md && grep -qxF 'Status: reviewed' collaboration.md && grep -qxF 'Execution: two native runners' collaboration.md",
            ],
          },
        ],
      },
    });
    const { agent: writer } = await api(
      `/api/rooms/${room.id}/agents`,
      'POST',
      {
        name: 'Collaboration Writer',
        handle: `writer-${tag}`,
        access: 'write',
        enabled: true,
        preferredRunnerId: runnerA,
        instructions:
          'Read the current handoff to determine the stage. On the first round write and read back the requested document, then hand off to the approved Reviewer. After the Reviewer returns the task, preserve the reviewed document, read it back, and finish with camp_finish. Never repeat a completed stage or change other files.',
      },
    );
    const { agent: reviewer } = await api(
      `/api/rooms/${room.id}/agents`,
      'POST',
      {
        name: 'Collaboration Reviewer',
        handle: `reviewer-${tag}`,
        access: 'read',
        enabled: true,
        preferredRunnerId: runnerB,
        instructions:
          'Read collaboration.md with a tool and verify the exact requested three lines. Do not write files. Hand the result back to the approved Writer with a short instruction to finish the checked document. Do not call camp_finish yourself for a changed task draft.',
      },
    );
    report.agentIds = [writer.id, reviewer.id];
    report.request = {
      requestId: randomUUID(),
      goal: `Create only collaboration.md with exactly these three lines and a final newline:\n${expected}\nThe Writer must write and read back the file, then use camp_handoff to ${reviewer.id}. The read-only Reviewer must read the actual file and hand the checked result back to ${writer.id}. The Writer must then use camp_finish. Use the current handoff to identify the stage; do not loop or ask a person. Each terminal tool should list collaboration.md as the artifact.`,
      participantAgentIds: [writer.id, reviewer.id],
      firstAgentId: writer.id,
    };
    report.status = 'authorized-outcome-unknown';
    // The durable reservation precedes the only work-creating network request.
    await save();
    let { task } = await api(
      `/api/rooms/${room.id}/tasks`,
      'POST',
      report.request,
    );
    report.taskId = task.id;
    check(
      task.maxTurns === 6 && task.maxModelRequests === 24,
      'Unexpected execution grant; inspect the recorded task.',
    );
    report.status = 'executing';
    await save();
    const deadline = Date.now() + 15 * 60 * 1000;
    let runs = [];
    while (
      ['queued', 'running', 'publishing', 'cancelling'].includes(task.status) &&
      Date.now() < deadline
    ) {
      await pause(1000);
      ({ task, runs } = await api(`/api/tasks/${task.id}`));
    }
    report.task = task;
    report.runs = runs.map(runSummary);
    await save();
    check(
      task.status === 'completed',
      'Collaboration did not complete. Inspect its saved task; no work or model request was retried.',
    );
    check(
      runs.length === 3 && runs.every((r) => r.status === 'succeeded'),
      'Expected exactly Writer → Reviewer → Writer.',
    );
    check(
      runs[0].runnerId === runnerA &&
        runs[1].runnerId === runnerB &&
        runs[2].runnerId === runnerA,
      'Cross-Runner routing differs from the authorized snapshot.',
    );
    check(
      runs[1].agentSnapshot.access === 'read' &&
        runs[0].checksDeferred &&
        runs[1].checksDeferred &&
        !runs[2].checksDeferred,
      'Draft and final-check stages differ from the plan.',
    );
    check(
      task.modelRequestsUsed <= 24 && task.turnsUsed === 3,
      'Execution exceeded its finite authorization.',
    );
    const actual = await api(
      `/api/resources/${room.resourceId}/file?${new URLSearchParams({ path: 'collaboration.md', commit: task.publishedCommit })}`,
      'GET',
      undefined,
      true,
    );
    check(
      actual === expected,
      'The published file does not match its requested exact contents.',
    );
    report.fileSHA256 = createHash('sha256').update(actual).digest('hex');
    report.checks.push(
      'native-cross-runner-handoff',
      'read-only-review',
      'draft-then-final-checks',
      'finite-model-authorization',
      'exact-published-file',
    );
    let { review } = await api(`/api/rooms/${room.id}/reviews`, 'POST', {
      requestId: randomUUID(),
      title: 'Native collaboration acceptance',
      resourceId: room.resourceId,
      path: 'collaboration.md',
      commit: task.publishedCommit,
      reviewerUserId: user.id,
    });
    for (const action of ['approve', 'deliver']) {
      ({ review } = await api(`/api/reviews/${review.id}/actions`, 'POST', {
        requestId: randomUUID(),
        revision: review.revision,
        action,
      }));
    }
    check(
      review.deliveredCommit === task.publishedCommit &&
        review.status === 'delivered',
      'Human delivery differs from the published task.',
    );
    report.reviewId = review.id;
    report.deliveredCommit = review.deliveredCommit;
    report.checks.push('explicit-human-review-and-immutable-delivery');
    report.status = 'passed';
    report.finishedAt = new Date().toISOString();
    await save();
    console.log(
      JSON.stringify({
        status: report.status,
        taskId: task.id,
        roomId: room.id,
        turns: task.turnsUsed,
        modelRequests: task.modelRequestsUsed,
        report: reportFile,
      }),
    );
  } catch (error) {
    report.status = 'failed';
    report.finishedAt = new Date().toISOString();
    report.error =
      error instanceof Error ? error.message : 'Acceptance failed.';
    await save();
    throw error;
  }
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
