import { describe, expect, it } from 'vitest';
import type { Message, Reference, Run } from './api';
import { handoffFiles, selectHandoffReferences } from './handoffFiles';

const upload: Reference = {
  kind: 'file',
  origin: 'upload',
  resourceId: 'upload',
  commit: 'original',
  path: 'brief.md',
  title: 'brief.md',
  size: 200,
};
const message = { references: [upload] } as Message;

describe('file evidence for explicit handoff', () => {
  it('keeps the sources that fit and reports omitted files instead of dropping every file', () => {
    const message: Reference = {
      kind: 'message',
      messageId: 'reply',
      title: 'Reply',
    };
    const files = Array.from({ length: 9 }, (_, index) => ({
      ...upload,
      path: `brief-${index}.md`,
    }));
    const result = selectHandoffReferences([], message, files);
    expect(result.selected).toEqual([message, ...files.slice(0, 7)]);
    expect(result.omitted).toBe(2);
    const withExisting = selectHandoffReferences([files[0]], message, files);
    expect(withExisting.selected).toEqual([message, ...files.slice(1, 7)]);
    expect(withExisting.omitted).toBe(2);
  });
  it('carries uploaded material from a posted message without creating work', () => {
    expect(handoffFiles(message)).toEqual([upload]);
  });
  it('keeps original input and retained output as different immutable sources', () => {
    const run = {
      id: 'run',
      resourceId: 'working',
      resultCommit: 'result',
      status: 'failed',
      published: false,
      files: [
        { path: 'brief.md', status: 'modified' },
        { path: 'old.md', status: 'deleted' },
      ],
      references: [
        upload,
        { kind: 'message', messageId: 'instructions', title: 'Instructions' },
      ],
    } as Run;
    const files = handoffFiles({ references: [] } as unknown as Message, run);
    expect(files).toHaveLength(2);
    expect(files[0]).toMatchObject({
      resourceId: 'working',
      commit: 'result',
      published: false,
    });
    expect(files[1]).toEqual(upload);
  });
  it('deduplicates identical files, while retaining different versions with the same name', () => {
    const selected = {
      ...upload,
      origin: undefined,
      resourceId: 'working',
      commit: 'result',
    };
    const run = {
      id: 'run',
      resourceId: 'working',
      resultCommit: 'result',
      status: 'succeeded',
      published: true,
      files: [{ path: 'brief.md', status: 'modified' }],
      references: [selected, upload],
    } as Run;
    expect(handoffFiles(message, run)).toHaveLength(2);
    expect(handoffFiles(message, run)[0].sourceRunId).toBe('run');
  });
});
