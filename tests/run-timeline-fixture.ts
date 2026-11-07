import type {
  Run,
  RunTimeline,
} from '../apps/dashboard/src/features/workspace/api';

// Match the public discussion projection. Full execution inputs, output and
// file contents belong only to the separately mocked Run details response.
export function timelineFixture(run: Run): RunTimeline {
  return {
    id: run.id,
    roomId: run.roomId,
    threadRootId: run.threadRootId,
    originMessageId: run.originMessageId,
    agentId: run.agentId,
    resourceId: run.resourceId,
    runnerId: run.runnerId,
    runnerName: run.runnerName,
    taskId: run.taskId,
    status: run.status,
    createdAt: run.createdAt,
    updatedAt: run.updatedAt,
    finishedAt: run.finishedAt,
    waitingReason: run.waitingReason,
    error: run.error,
    resultCommit: run.resultCommit,
    published: run.published,
    checksDeferred: run.checksDeferred,
    testExitCode: run.testExitCode,
    agentSnapshot: {
      id: run.agentSnapshot.id,
      name: run.agentSnapshot.name,
      avatar: run.agentSnapshot.avatar,
      access: run.agentSnapshot.access,
    },
    environment: run.environment
      ? {
          checks: run.environment.checks?.map(({ name }) => ({ name })),
        }
      : null,
    checks:
      run.checks?.map(({ name, status, exitCode }) => ({
        name,
        status,
        exitCode,
      })) ?? null,
    files: run.files.slice(0, 20).map(({ path, status }) => ({ path, status })),
    fileCount: run.files.length,
    filesLimited: run.files.length > 20,
  };
}
