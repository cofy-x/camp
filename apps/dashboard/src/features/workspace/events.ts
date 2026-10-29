import type { RunEvent } from './api';

export function mergeEvents(current: RunEvent[], incoming: RunEvent[]) {
  const bySequence = new Map(current.map((event) => [event.seq, event]));
  for (const event of incoming) bySequence.set(event.seq, event);
  return [...bySequence.values()].sort((left, right) => left.seq - right.seq);
}
