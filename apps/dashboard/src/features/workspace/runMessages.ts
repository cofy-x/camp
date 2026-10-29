import { workspaceApi, type Message, type Run } from './api';

export async function findRunReply(
  run: Pick<Run, 'id' | 'roomId' | 'threadRootId'>,
  signal?: AbortSignal,
): Promise<Message | null> {
  let after = 0;
  for (;;) {
    signal?.throwIfAborted();
    const { messages } = await workspaceApi.messages(
      run.roomId,
      run.threadRootId,
      after,
      signal,
    );
    signal?.throwIfAborted();
    const reply = messages.find(
      (message) => message.authorKind === 'agent' && message.runId === run.id,
    );
    if (reply) return reply;
    const next = messages.at(-1)?.seq ?? after;
    if (messages.length < 500 || next <= after) return null;
    after = next;
  }
}
