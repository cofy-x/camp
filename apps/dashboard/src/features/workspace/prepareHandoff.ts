import type { Message, Run } from './api';
import {
  addDraftReferences,
  conversationKey,
  draftReferences,
  writeStored,
} from './conversationState';
import { handoffFiles, selectHandoffReferences } from './handoffFiles';

/** Prepare a cited draft; selecting recipients and sending remain explicit. */
export function prepareHandoff(
  roomId: string,
  message: Message,
  run?: Run,
  agentName?: string,
) {
  const key = conversationKey(roomId, message.threadRootId);
  const author =
    message.authorKind === 'user'
      ? message.authorName || 'Team member'
      : message.authorKind === 'system'
        ? 'Camp'
        : run?.agentSnapshot.name || agentName || message.authorName || 'Agent';
  const reference = {
    kind: 'message' as const,
    messageId: message.id,
    title: author === 'You' ? 'Your message' : `${author}'s message`,
    author,
    content: message.content,
    threadRootId: message.threadRootId,
    sourceRunId: run?.id,
    sourceStatus: run?.status,
    published: run?.published,
  };
  const { selected, omitted } = selectHandoffReferences(
    draftReferences(key),
    reference,
    handoffFiles(message, run),
  );
  addDraftReferences(key, selected);
  writeStored(
    `draft-seed:${key}`,
    JSON.stringify({
      references: selected,
      handoff: crypto.randomUUID(),
      recipients: 'null',
    }),
  );
  writeStored(
    `reference-note:${key}`,
    omitted
      ? `${omitted} ${omitted === 1 ? 'file was' : 'files were'} not added because a message allows 8 references. Remove a source to choose another file from Files or Details → Files.`
      : '',
  );
  writeStored(`handoff:${key}`, crypto.randomUUID());
  writeStored(`recipients:${key}`, 'null');
}
