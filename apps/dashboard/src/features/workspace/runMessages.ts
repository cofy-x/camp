import { workspaceApi, type Message, type Run } from './api';

export async function findRunReply(
  run: Pick<Run, 'id' | 'roomId' | 'threadRootId'>,
  signal?: AbortSignal,
): Promise<Message | null> {
  signal?.throwIfAborted();
  const { message } = await workspaceApi.runReply(run.id, signal);
  signal?.throwIfAborted();
  if (
    message &&
    (message.authorKind !== 'agent' ||
      message.runId !== run.id ||
      message.roomId !== run.roomId ||
      message.threadRootId !== run.threadRootId)
  )
    throw new Error('Could not verify this Agent reply.');
  return message;
}
