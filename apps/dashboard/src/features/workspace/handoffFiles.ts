import type { Message, Reference, Run } from './api';
import { referenceKey } from './conversationState';

// A collaborator needs both the delivered work and the files it was based on.
// These remain visible, removable selections in the next draft.
export function handoffFiles(message: Message, run?: Run): Reference[] {
  const results: Reference[] = run?.resultCommit
    ? (run.files ?? [])
        .filter((file) => file.status !== 'deleted')
        .map((file) => ({
          kind: 'file',
          resourceId: run.resourceId,
          commit: run.resultCommit,
          path: file.path,
          title: file.path,
          sourceRunId: run.id,
          sourceStatus: run.status,
          published: run.published,
        }))
    : [];
  const sources = (run?.references ?? message.references ?? []).filter(
    (reference) => reference.kind === 'file',
  );
  const unique = new Map<string, Reference>();
  for (const file of [...results, ...sources]) {
    const key = referenceKey(file);
    if (!unique.has(key)) unique.set(key, file);
  }
  return [...unique.values()];
}

export function selectHandoffReferences(
  existing: Reference[],
  message: Reference,
  files: Reference[],
) {
  const seen = new Set([...existing, message].map(referenceKey));
  const selected = [message];
  let omitted = 0;
  for (const file of files) {
    const key = referenceKey(file);
    if (seen.has(key)) continue;
    if (seen.size >= 8) {
      omitted++;
      continue;
    }
    seen.add(key);
    selected.push(file);
  }
  return { selected, omitted };
}
