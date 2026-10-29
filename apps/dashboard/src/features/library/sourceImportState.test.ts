import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  parseImportReceipt,
  readImportReceipt,
  saveImportReceipt,
  type ImportReceipt,
} from './sourceImportState';

const record: ImportReceipt = {
  input: {
    requestId: 'stable-request',
    runnerId: 'runner-a',
    sourceId: 'source-a',
    expectedSourceRevision: 'mapping-digest',
    roomName: 'Notes',
  },
};

afterEach(() => vi.unstubAllGlobals());

describe('recovering source import receipts', () => {
  it('retains the exact request identity and mapping revision before a receipt is known', () => {
    expect(parseImportReceipt(JSON.stringify(record))).toEqual(record);
    expect(
      parseImportReceipt(JSON.stringify({ ...record, importId: 'receipt' })),
    ).toEqual({ ...record, importId: 'receipt' });
    expect(
      parseImportReceipt(JSON.stringify({ importId: 'known-import' })),
    ).toEqual({ importId: 'known-import' });
  });
  it('rejects malformed persisted input without sending an unconstrained source path', () => {
    expect(parseImportReceipt('{')).toBeNull();
    for (const input of [
      undefined,
      { ...record.input, requestId: '' },
      { ...record.input, expectedSourceRevision: 3 },
      { ...record.input, sourceId: undefined, sourcePath: '/home/private' },
    ])
      expect(parseImportReceipt(JSON.stringify({ input }))).toBeNull();
  });
  it('keeps ambiguous requests through window remounts even when browser storage fails, isolated per person', () => {
    vi.stubGlobal('sessionStorage', {
      getItem: () => {
        throw new Error('Blocked');
      },
      setItem: () => {
        throw new Error('Full');
      },
      removeItem: () => {},
    });
    saveImportReceipt('owner', record);
    expect(readImportReceipt('owner')).toEqual(record);
    expect(readImportReceipt('other-member')).toBeNull();
    saveImportReceipt('owner', null);
    expect(readImportReceipt('owner')).toBeNull();
  });
});
