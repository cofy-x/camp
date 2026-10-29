import { describe, expect, it } from 'vitest';
import type { Run, RunEvent } from './api';
import { buildRunDiagnostics } from './runDiagnosticRecords';

function exampleRun(): Run {
  return {
    id: 'run-1',
    taskId: 'task-1',
    inputCommit: 'task-checkpoint-1',
    checksDeferred: true,
    roomId: 'room-1',
    threadRootId: '',
    originMessageId: 'request-1',
    agentId: 'agent-1',
    resourceId: 'resource-1',
    runnerId: 'runner-1',
    runnerName: 'Studio Linux',
    attemptId: 'attempt-1',
    prompt: 'Private task instructions',
    references: [],
    status: 'interrupted',
    createdAt: '2026-10-28T01:00:00Z',
    updatedAt: '2026-10-28T01:00:02Z',
    finishedAt: '2026-10-28T01:00:02Z',
    error: 'Model stream ended before completion.',
    output: 'Private complete answer',
    sessionId: 'session-1',
    sessionMode: 'resume',
    baselineCommit: 'baseline-1',
    resultCommit: '',
    agentSnapshot: {
      id: 'agent-1',
      name: 'Writer',
      handle: 'writer',
      icon: 'document',
      instructions: 'Private agent instructions',
      access: 'write',
      enabled: true,
      revision: 3,
      createdAt: '',
      updatedAt: '',
    },
    environment: null,
    checks: [
      {
        name: 'content',
        command: 'check-content',
        status: 'failed',
        exitCode: 2,
        output: 'Required section is missing.',
        startedAt: '2026-10-28T01:00:01Z',
        finishedAt: '2026-10-28T01:00:02Z',
      },
    ],
    files: [
      {
        path: 'brief.md',
        status: 'modified',
        before: 'Private original file',
        after: 'Private revised file',
      },
    ],
    exitCode: null,
    testExitCode: 2,
    maxTokens: 4096,
    maxSteps: 8,
    timeoutSeconds: 180,
    sessionGeneration: 4,
    published: false,
  };
}

const event: RunEvent = {
  runId: 'run-1',
  seq: 7,
  type: 'error',
  text: 'Connection closed during the model response.',
  createdAt: '2026-10-28T01:00:02Z',
};

describe('downloadable work diagnostics', () => {
  it('preserves execution identity, configured limits and original failure evidence', () => {
    const data = buildRunDiagnostics(exampleRun(), [event], true);
    expect(data.schema).toBe('camp.run-diagnostics');
    expect(data.schemaVersion).toBe(1);
    expect(data.run).toMatchObject({
      id: 'run-1',
      taskId: 'task-1',
      inputCommit: 'task-checkpoint-1',
      checksDeferred: true,
      runnerId: 'runner-1',
      attemptId: 'attempt-1',
      status: 'interrupted',
      error: 'Model stream ended before completion.',
      session: { id: 'session-1', mode: 'resume', generation: 4 },
      baselineCommit: 'baseline-1',
      exitCode: null,
      testExitCode: 2,
      configuredLimits: {
        maxOutputTokensPerRequest: 4096,
        maxObservedModelSteps: 8,
        timeoutSeconds: 180,
      },
    });
    expect(data.run).not.toHaveProperty('usage');
    expect(data.checks).toEqual(exampleRun().checks);
    expect(data.rawOutput).toBe(exampleRun().output);
    expect(data.activity).toEqual([event]);
    expect(data.activityComplete).toBe(true);
  });

  it('excludes snapshots and unrecognized fields at every exported object boundary', () => {
    const marker = 'must-not-appear-in-export';
    const run = {
      ...exampleRun(),
      prompt: marker,
      output: 'Recorded tool and model transcript.',
      references: [
        { kind: 'message' as const, messageId: 'message', title: marker },
      ],
      configuration: { apiKey: marker },
      agentSnapshot: {
        ...exampleRun().agentSnapshot,
        instructions: marker,
        credential: marker,
      },
      environment: { privateConfig: marker },
      files: [
        {
          path: 'private.txt',
          status: 'added' as const,
          before: marker,
          after: marker,
        },
      ],
      checks: exampleRun().checks!.map((check) => ({
        ...check,
        credential: marker,
      })),
    };
    const events = [{ ...event, credential: marker }];
    const data = buildRunDiagnostics(run as Run, events, true);
    expect(JSON.stringify(data)).not.toContain(marker);
    expect(data.run).not.toHaveProperty('prompt');
    expect(data.run).not.toHaveProperty('files');
    expect(data.run).not.toHaveProperty('agentSnapshot');
    expect(data.rawOutput).toBe('Recorded tool and model transcript.');
    expect(data.activity[0]).toEqual(event);
  });

  it('marks incomplete activity without discarding already fetched errors', () => {
    const data = buildRunDiagnostics(exampleRun(), [event], false);
    expect(data.activityComplete).toBe(false);
    expect(data.activity[0].text).toBe(event.text);
    expect(data.run.error).toBe(exampleRun().error);
  });
});
