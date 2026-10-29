import { describe, expect, it } from 'vitest';
import { mergeEvents } from './events';
import type { RunEvent } from './api';

function event(seq: number, text = `Event ${seq}`): RunEvent {
  return {
    runId: 'run-1',
    seq,
    type: 'output',
    text,
    createdAt: '2026-10-26T00:00:00Z',
  };
}

describe('incremental run events', () => {
  it('merges overlapping pages in order without duplicates or changing the input', () => {
    const current = [event(1), event(3)];
    expect(mergeEvents(current, [event(3), event(2)])).toEqual([
      event(1),
      event(2),
      event(3),
    ]);
    expect(current).toEqual([event(1), event(3)]);
  });
  it('preserves received progress when a page is empty', () => {
    expect(mergeEvents([event(1)], [])).toEqual([event(1)]);
  });
});
