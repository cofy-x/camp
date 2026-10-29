import { useEffect, useRef, useState } from 'react';
import type { Reference } from './api';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import {
  readStored,
  referenceKey,
  subscribeStored,
  writeStored,
} from './conversationState';

const fields = [
  'draft',
  'recipients',
  'references',
  'handoff',
  'reference-note',
] as const;
type ConversationDraft = Record<(typeof fields)[number], string>;

function snapshot(key: string): ConversationDraft {
  return Object.fromEntries(
    fields.map((field) => [field, readStored(`${field}:${key}`)]),
  ) as ConversationDraft;
}

// Local state owns live attachment updates; the account draft owns recovery across
// browsers. The CAS writer preserves a conflicting local version for an explicit choice.
export function useConversationDraft(localKey: string, serverKey: string) {
  const initial = useRef(snapshot(localKey));
  const draft = usePrivateDraft('conversation', serverKey, initial.current);
  const restored = useRef(false);
  const [ready, setReady] = useState(false);
  const setValue = draft.setValue;
  useEffect(() => {
    if (!draft.loaded || restored.current) return;
    restored.current = true;
    let next = draft.value;
    const seedValue = readStored(`draft-seed:${localKey}`);
    if (seedValue) {
      try {
        const seed = JSON.parse(seedValue) as {
          references?: Reference[];
          handoff?: string;
          recipients?: string;
        };
        const references = JSON.parse(next.references || '[]') as Reference[];
        const seen = new Set(references.map(referenceKey));
        const added = (seed.references ?? []).filter(
          (item) => !seen.has(referenceKey(item)),
        );
        next = {
          ...next,
          references: JSON.stringify([...references, ...added].slice(0, 8)),
          ...(seed.handoff ? { handoff: seed.handoff } : {}),
          ...(seed.recipients !== undefined
            ? { recipients: seed.recipients }
            : {}),
        };
        setValue(next);
        writeStored(`draft-seed:${localKey}`, '');
      } catch {
        /* Preserve the restored draft if an interrupted seed is incomplete. */
      }
    }
    for (const field of fields)
      writeStored(`${field}:${localKey}`, next[field] ?? '');
    // Keep composition disabled until the loaded account draft is mirrored.
    // Otherwise fast input can arrive between the render and this effect.
    setReady(true);
  }, [draft.loaded, draft.value, localKey, setValue]);
  useEffect(() => {
    if (!draft.loaded) return;
    const update = () => setValue(snapshot(localKey));
    const unsubscribe = fields.map((field) =>
      subscribeStored(`${field}:${localKey}`, update),
    );
    return () => unsubscribe.forEach((stop) => stop());
  }, [localKey, draft.loaded, setValue]);
  return {
    ...draft,
    loaded: draft.loaded && ready,
    useRemote: () => {
      const next = draft.comparison?.value ?? initial.current;
      draft.useRemote();
      for (const field of fields)
        writeStored(`${field}:${localKey}`, next[field] ?? '');
    },
  };
}
