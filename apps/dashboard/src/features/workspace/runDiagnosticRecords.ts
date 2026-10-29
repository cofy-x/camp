import type { Run, RunEvent } from './api';

// Keep this export allowlisted as the API grows. Diagnostics include recorded
// activity and check output, but not private configuration or input snapshots.
export function buildRunDiagnostics(
  run: Run,
  events: RunEvent[],
  complete: boolean,
) {
  return {
    schema: 'camp.run-diagnostics',
    schemaVersion: 1,
    exportedAt: new Date().toISOString(),
    run: {
      id: run.id,
      taskId: run.taskId ?? null,
      inputCommit: run.inputCommit ?? null,
      checksDeferred: run.checksDeferred ?? false,
      roomId: run.roomId,
      threadRootId: run.threadRootId,
      originMessageId: run.originMessageId,
      agentId: run.agentId,
      resourceId: run.resourceId,
      runnerId: run.runnerId ?? null,
      runnerName: run.runnerName ?? null,
      attemptId: run.attemptId ?? null,
      access: run.agentSnapshot.access,
      status: run.status,
      waitingReason: run.waitingReason ?? null,
      error: run.error,
      createdAt: run.createdAt,
      updatedAt: run.updatedAt,
      finishedAt: run.finishedAt,
      session: {
        id: run.sessionId,
        mode: run.sessionMode,
        generation: run.sessionGeneration,
      },
      baselineCommit: run.baselineCommit,
      resultCommit: run.resultCommit,
      published: run.published,
      exitCode: run.exitCode,
      testExitCode: run.testExitCode,
      configuredLimits: {
        maxOutputTokensPerRequest: run.maxTokens,
        maxObservedModelSteps: run.maxSteps,
        timeoutSeconds: run.timeoutSeconds,
      },
    },
    checks: (run.checks ?? []).map((check) => ({
      name: check.name,
      command: check.command,
      status: check.status,
      exitCode: check.exitCode,
      output: check.output,
      startedAt: check.startedAt,
      finishedAt: check.finishedAt,
    })),
    rawOutput: run.output,
    activityComplete: complete,
    activity: events.map((event) => ({
      runId: event.runId,
      seq: event.seq,
      type: event.type,
      text: event.text,
      createdAt: event.createdAt,
    })),
  };
}
