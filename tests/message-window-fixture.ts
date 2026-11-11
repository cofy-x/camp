import type { Message } from '../apps/dashboard/src/features/workspace/api';

// Presentation fixtures implement the public window contract; indexed storage,
// byte bounds and authority are qualified against the real Server separately.
export function messageWindowFixture(messages: Message[], url: URL) {
  const query = url.searchParams;
  const thread = query.get('threadRootId') ?? '';
  const rows = messages
    .filter((message) => message.threadRootId === thread)
    .sort((a, b) => a.seq - b.seq);
  let window: Message[];
  if (query.has('around')) {
    const index = rows.findIndex(
      (message) => message.id === query.get('around'),
    );
    window =
      index < 0
        ? rows.slice(-50)
        : rows.slice(Math.max(0, index - 25), Math.max(0, index - 25) + 50);
  } else if (query.has('after'))
    window = rows
      .filter((message) => message.seq > Number(query.get('after')))
      .slice(0, 50);
  else
    window = rows
      .filter(
        (message) => message.seq < Number(query.get('before') ?? Infinity),
      )
      .slice(-50);
  const root = thread
    ? messages.find((message) => message.id === thread)
    : undefined;
  return {
    messages: root ? [root, ...window] : window,
    through: Math.max(0, ...messages.map((message) => message.seq)),
    hasOlder: rows.some(
      (message) => message.seq < (window[0]?.seq ?? Infinity),
    ),
    hasNewer: rows.some((message) => message.seq > (window.at(-1)?.seq ?? 0)),
  };
}
