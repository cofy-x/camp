import { describe, expect, it } from 'vitest';
import type { Runner, SourceImport } from './api';
import {
  importPending,
  mergeImportStatus,
  runnerQueueMessage,
  runnerSetupCommand,
  runnerRuntime,
  runnerStatus,
} from './presentation';

const runner: Runner = {
  id: 'runner-a',
  name: 'Build machine',
  enabled: true,
  revoked: false,
  online: true,
  lastSeenAt: '',
  runtime: 'fixture',
  capabilities: { executor: 'fixture', ready: true, slots: 1 },
};
const sourceImport: SourceImport = {
  id: 'import-a',
  runnerId: 'runner-a',
  sourceId: 'source-a',
  sourceAlias: 'Notes',
  roomName: 'Working notes',
  status: 'capturing',
  createdAt: '',
  updatedAt: '2026-10-28T10:00:00Z',
};

describe('remote execution facts', () => {
  it('includes the required Runner name as one literal shell argument', () => {
    expect(
      runnerSetupCommand('https://camp.example', "Studio's $(touch unsafe)"),
    ).toBe(
      "./camp runner setup --server 'https://camp.example' --name 'Studio'\\''s $(touch unsafe)'",
    );
  });
  it('labels a test executor independently from connection and readiness', () => {
    expect(runnerRuntime(runner)).toBe('Test executor');
    expect(runnerStatus(runner)).toBe('Online');
    expect(runnerRuntime({ ...runner, runtime: 'native' })).toBe(
      'Test executor',
    );
    expect(runnerStatus({ ...runner, online: false })).toBe('Offline');
    expect(
      runnerStatus({
        ...runner,
        capabilities: { ...runner.capabilities, ready: false },
      }),
    ).toBe('Needs attention');
  });
  it('keeps revoked access distinct from an offline node that can receive queued work', () => {
    expect(runnerQueueMessage({ ...runner, online: false })).toContain(
      'queues here',
    );
    expect(
      runnerQueueMessage({ ...runner, revoked: true, online: false }),
    ).toContain('Choose another Runner');
    expect(
      runnerStatus({ ...runner, revoked: true, enabled: false, online: false }),
    ).toBe('Access revoked');
    expect(runnerQueueMessage(undefined)).toContain('Choose a Runner');
  });
  it('uses adapter facts instead of a machine name or assumed LayerFS version', () => {
    const native: Runner = {
      ...runner,
      runtime: 'native',
      capabilities: {
        executor: 'native',
        ready: true,
        layerfsVersion: 'legacy',
      },
    };
    expect(runnerRuntime(native)).toBe('Native execution');
    expect(
      runnerRuntime({
        ...native,
        capabilities: {
          ...native.capabilities,
          workspace: 'native-local',
          sandbox: 'seatbelt',
        },
      }),
    ).toBe('Mac native');
    expect(
      runnerRuntime({
        ...native,
        capabilities: {
          ...native.capabilities,
          workspace: 'layerfs',
          sandbox: 'linux-container',
        },
      }),
    ).toBe('Linux native');
  });
});

describe('import response ordering', () => {
  it('cannot roll a cancellation or published import back to active after a late poll', () => {
    for (const status of ['cancelled', 'succeeded', 'failed'] as const) {
      const final = { ...sourceImport, status };
      expect(
        mergeImportStatus(final, {
          ...sourceImport,
          updatedAt: '2026-10-28T10:00:10Z',
        }),
      ).toBe(final);
      expect(importPending(final)).toBe(false);
    }
  });
  it('keeps newer progress while allowing a different explicitly requested import', () => {
    const upload = {
      ...sourceImport,
      status: 'uploading' as const,
      updatedAt: '2026-10-28T10:00:05Z',
    };
    expect(mergeImportStatus(upload, sourceImport)).toBe(upload);
    expect(
      mergeImportStatus(upload, { ...sourceImport, id: 'new-import' }).id,
    ).toBe('new-import');
  });
});
