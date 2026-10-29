import type {
  Agent,
  DocumentSnapshot,
  Reference,
  ReferenceInput,
  SendInput,
} from './api';
import { documentPending } from './documents';

let prefix = 'camp:rooms:anonymous:';
const memory = new Map<string, string>();
const listeners = new Map<string, Set<() => void>>();

export function setConversationOwner(userId: string) {
  const next = `camp:rooms:${userId}:`;
  if (next === prefix) return;
  prefix = next;
  memory.clear();
  listeners.clear();
}

export function subscribeStored(key: string, listener: () => void) {
  const group = listeners.get(key) ?? new Set<() => void>();
  group.add(listener);
  listeners.set(key, group);
  return () => {
    group.delete(listener);
    if (!group.size) listeners.delete(key);
  };
}

function storageKey(key: string) {
  const marker = key.indexOf('camp:rooms:');
  return marker < 0 ? prefix + key : key;
}

export function readStored(key: string) {
  if (memory.has(key)) return memory.get(key)!;
  try {
    return sessionStorage.getItem(storageKey(key)) ?? '';
  } catch {
    return '';
  }
}

export function writeStored(key: string, value: string) {
  memory.set(key, value);
  listeners.get(key)?.forEach((listener) => listener());
  try {
    if (value) sessionStorage.setItem(storageKey(key), value);
    else sessionStorage.removeItem(storageKey(key));
  } catch {
    // Keep mounted drafts when browser storage is unavailable or full.
  }
}

export function mergeMessages<T extends { id: string; seq: number }>(
  current: T[],
  incoming: T[],
) {
  const byId = new Map(current.map((item) => [item.id, item]));
  for (const item of incoming) byId.set(item.id, item);
  return [...byId.values()].sort((a, b) => a.seq - b.seq);
}

export function requestForDraft(
  saved: string,
  input: Omit<SendInput, 'requestId'>,
  createId: () => string = () => crypto.randomUUID(),
): SendInput {
  try {
    const previous = JSON.parse(saved) as SendInput;
    if (
      previous.requestId &&
      previous.content === input.content &&
      previous.threadRootId === input.threadRootId &&
      Boolean(previous.postOnly) === Boolean(input.postOnly) &&
      JSON.stringify(previous.recipientAgentIds) ===
        JSON.stringify(input.recipientAgentIds) &&
      JSON.stringify(previous.references ?? []) ===
        JSON.stringify(input.references ?? [])
    )
      return previous;
  } catch {
    /* A damaged retry record must not prevent composing. */
  }
  return { ...input, requestId: createId() };
}

export function mentionedAgents(content: string, members: Agent[]) {
  const handles = [...content.matchAll(/(?:^|\s)@([a-zA-Z0-9_-]+)\b/g)].map(
    (match) => match[1].toLowerCase(),
  );
  return members
    .filter(
      (agent) => agent.enabled && handles.includes(agent.handle.toLowerCase()),
    )
    .map((agent) => agent.id);
}

export function conversationKey(roomId: string, threadRootId = '') {
  return `${prefix}@${roomId}:${threadRootId || 'room'}`;
}

export function acknowledgeDraft(key: string, request: SendInput) {
  try {
    const current = JSON.parse(readStored(`request:${key}`)) as SendInput;
    if (current.requestId !== request.requestId) return;
    writeStored(`request:${key}`, '');
    const matchesDraft = readStored(`draft:${key}`).trim() === request.content;
    if (
      matchesDraft &&
      (readStored(`recipients:${key}`) || 'null') ===
        (readStored(`routing:${key}`) || 'null') &&
      (!readStored(`handoff:${key}`) ||
        readStored(`sent-handoff:${key}`) === readStored(`handoff:${key}`)) &&
      matchesRequestReferences(
        draftReferences(key),
        request,
        readStored(`reference-intent:${key}`),
      )
    ) {
      writeStored(`draft:${key}`, '');
      writeStored(`references:${key}`, '');
      writeStored(`reference-note:${key}`, '');
      if (readStored(`sent-handoff:${key}`) === readStored(`handoff:${key}`))
        writeStored(`handoff:${key}`, '');
    }
    writeStored(`reference-intent:${key}`, '');
  } catch {
    /* A newer or missing retry record belongs to its current composer. */
  }
}

export function unresolvedMentions(content: string, members: Agent[]) {
  const handles = [...content.matchAll(/(?:^|\s)@([a-zA-Z0-9_-]+)\b/g)].map(
    (match) => match[1].toLowerCase(),
  );
  return [...new Set(handles)].filter(
    (handle) =>
      !members.some(
        (member) => member.enabled && member.handle.toLowerCase() === handle,
      ),
  );
}

export function retryForIntent(
  saved: string,
  content: string,
  threadRootId: string,
  intent: string,
  previousIntent: string,
  references: ReferenceInput[] = [],
  currentReferences?: Reference[],
  savedReferenceIntent = '',
): SendInput | null {
  try {
    const request = JSON.parse(saved) as SendInput;
    if (
      request.requestId &&
      request.content === content &&
      request.threadRootId === threadRootId &&
      intent === previousIntent &&
      (currentReferences
        ? matchesRequestReferences(
            currentReferences,
            request,
            savedReferenceIntent,
          )
        : JSON.stringify(request.references ?? []) ===
          JSON.stringify(references))
    )
      return request;
  } catch {
    /* Only complete pending requests can be replayed. */
  }
  return null;
}

export function referenceInputs(references: Reference[]): ReferenceInput[] {
  return references.map((reference) =>
    reference.kind === 'message'
      ? { kind: 'message', messageId: reference.messageId }
      : {
          kind: 'file',
          resourceId: reference.resourceId,
          commit: reference.commit,
          path: reference.path,
          ...(reference.document && !documentPending(reference.document.status)
            ? { documentId: reference.document.id }
            : !reference.document && reference.documentId
              ? { documentId: reference.documentId }
              : {}),
        },
  );
}

export function draftReferences(key: string): Reference[] {
  try {
    const value: unknown = JSON.parse(readStored(`references:${key}`));
    return Array.isArray(value) ? value : [];
  } catch {
    return [];
  }
}

export function referenceKey(reference: ReferenceInput): string {
  return JSON.stringify(
    reference.kind === 'message'
      ? { kind: reference.kind, messageId: reference.messageId }
      : {
          kind: reference.kind,
          resourceId: reference.resourceId,
          commit: reference.commit,
          path: reference.path,
        },
  );
}

export function updateDraftDocument(
  key: string,
  reference: Reference,
  document: DocumentSnapshot,
) {
  const references = draftReferences(key);
  const index = references.findIndex(
    (current) => referenceKey(current) === referenceKey(reference),
  );
  if (index < 0) return;
  const current = references[index];
  if (
    (current.document?.id ?? current.documentId) !==
      (reference.document?.id ?? reference.documentId) ||
    (current.document?.id === document.id &&
      !documentPending(current.document.status) &&
      documentPending(document.status)) ||
    JSON.stringify(current.document) === JSON.stringify(document)
  )
    return;
  references[index] = {
    ...current,
    document,
    ...(current.document?.id && current.document.id !== document.id
      ? { documentId: document.id }
      : {}),
  };
  writeStored(`references:${key}`, JSON.stringify(references));
}

export function addDraftReferences(key: string, references: Reference[]) {
  const current = draftReferences(key);
  const seen = new Set(current.map(referenceKey));
  const added = references.filter((reference) => {
    const identity = referenceKey(reference);
    if (seen.has(identity)) return false;
    seen.add(identity);
    return true;
  });
  if (current.length + added.length > 8)
    throw new Error(
      'A message can include up to 8 references. Remove a reference before adding another.',
    );
  writeStored(`references:${key}`, JSON.stringify([...current, ...added]));
}

// An explicit file selection may replace its extraction, without duplicating
// the original. Batch handoffs keep the user's existing draft choices.
export function selectDraftReference(key: string, reference: Reference) {
  const references = draftReferences(key);
  const index = references.findIndex(
    (item) => referenceKey(item) === referenceKey(reference),
  );
  const selected = reference.document
    ? { ...reference, documentId: reference.document.id }
    : reference;
  if (index >= 0 && reference.document) {
    references[index] = { ...references[index], ...selected };
    writeStored(`references:${key}`, JSON.stringify(references));
  } else addDraftReferences(key, [selected]);
}

function referenceIntent(reference: Reference) {
  return {
    source: referenceKey(reference),
    artifact: reference.document?.id ?? reference.documentId,
    selection: reference.documentId,
  };
}

export function rememberReferenceIntent(
  key: string,
  requestId: string,
  references: Reference[],
) {
  writeStored(
    `reference-intent:${key}`,
    JSON.stringify({
      requestId,
      references: references.map(referenceIntent),
    }),
  );
}

function matchesRequestReferences(
  references: Reference[],
  request: SendInput,
  savedIntent: string,
) {
  try {
    const intent = JSON.parse(savedIntent) as {
      requestId: string;
      references: ReturnType<typeof referenceIntent>[];
    };
    if (
      intent.requestId === request.requestId &&
      Array.isArray(intent.references)
    ) {
      return (
        intent.references.length === references.length &&
        references.every((reference, index) => {
          const before = intent.references[index];
          const current = referenceIntent(reference);
          return (
            before.source === current.source &&
            before.selection === current.selection &&
            (!before.artifact || before.artifact === current.artifact)
          );
        })
      );
    }
  } catch {
    // A missing intent record still requires the complete request to match.
  }
  return (
    JSON.stringify(referenceInputs(references)) ===
    JSON.stringify(request.references ?? [])
  );
}
