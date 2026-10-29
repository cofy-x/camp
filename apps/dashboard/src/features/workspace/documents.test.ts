import { describe, expect, it } from 'vitest';
import type { DocumentSnapshot, Reference } from './api';
import {
  addDraftReferences,
  acknowledgeDraft,
  draftReferences,
  referenceInputs,
  referenceKey,
  readStored,
  requestForDraft,
  retryForIntent,
  selectDraftReference,
  rememberReferenceIntent,
  updateDraftDocument,
  writeStored,
} from './conversationState';
import {
  documentFormat,
  documentPending,
  documentStatusLabel,
} from './documents';

const queued: DocumentSnapshot = {
  id: 'extraction-one',
  format: 'pdf',
  status: 'queued',
  sectionCount: 0,
  warnings: [],
};
const ready: DocumentSnapshot = { ...queued, status: 'ready', sectionCount: 2 };
const reference: Reference = {
  kind: 'file',
  resourceId: 'original',
  commit: 'a'.repeat(40),
  path: 'brief.pdf',
  title: 'brief.pdf',
  document: queued,
};

describe('document references', () => {
  it('pins terminal extraction identities but never queues an incomplete artifact as evidence', () => {
    expect(referenceInputs([reference])[0]).not.toHaveProperty('documentId');
    for (const status of [
      'ready',
      'failed',
      'unavailable',
      'unreadable',
    ] as const)
      expect(
        referenceInputs([{ ...reference, document: { ...queued, status } }])[0],
      ).toHaveProperty('documentId', queued.id);
    expect(documentPending('extracting')).toBe(true);
    expect(documentFormat('Report.PDF')).toBe('pdf');
    expect(documentFormat('old.doc')).toBe(null);
  });

  it('updates only the existing matching draft and leaves text and routing untouched', () => {
    const key = 'document-metadata-update';
    writeStored(`draft:${key}`, 'Review this brief.');
    writeStored(`routing:${key}`, '["reviewer"]');
    addDraftReferences(key, [reference]);
    updateDraftDocument(key, reference, ready);
    expect(draftReferences(key)).toEqual([{ ...reference, document: ready }]);
    expect(readStored(`draft:${key}`)).toBe('Review this brief.');
    expect(readStored(`routing:${key}`)).toBe('["reviewer"]');
    updateDraftDocument(key, reference, queued);
    expect(draftReferences(key)[0].document).toEqual(ready);
    writeStored(`references:${key}`, '[]');
    updateDraftDocument(key, reference, ready);
    expect(draftReferences(key)).toEqual([]);
  });

  it('ignores a late old extraction after an explicit retry and keeps one original reference', () => {
    const key = 'document-retry-ordering';
    const failed: DocumentSnapshot = { ...queued, status: 'failed' };
    const original = { ...reference, document: failed };
    addDraftReferences(key, [original]);
    const newer = { ...queued, id: 'extraction-two' };
    updateDraftDocument(key, original, newer);
    updateDraftDocument(key, original, ready);
    expect(draftReferences(key)[0].document).toEqual(newer);
    addDraftReferences(key, [original]);
    expect(draftReferences(key)).toHaveLength(1);
    expect(referenceKey(original)).toEqual(
      referenceKey({ ...original, documentId: newer.id }),
    );
  });

  it('does not reuse a pending request after its pinned extraction changes', () => {
    const content = 'Review the exact source.';
    const previous = requestForDraft(
      '',
      {
        content,
        references: referenceInputs([{ ...reference, document: ready }]),
      },
      () => 'first-request',
    );
    const next = requestForDraft(
      JSON.stringify(previous),
      {
        content,
        references: referenceInputs([
          { ...reference, document: { ...ready, id: 'extraction-two' } },
        ]),
      },
      () => 'second-request',
    );
    expect(next.requestId).toBe('second-request');
    expect(documentStatusLabel({ ...queued, status: 'unreadable' })).toBe(
      'Original only',
    );
    expect(documentStatusLabel(ready)).toBe('2 pages · Text ready');
  });

  it('keeps a Post only receipt retryable when extraction finishes in the background', () => {
    const key = 'document-post-only-receipt';
    const content = 'Keep this source for later.';
    const request = {
      requestId: 'post-with-pending-text',
      content,
      postOnly: true,
      threadRootId: '',
      recipientAgentIds: [],
      references: referenceInputs([reference]),
    };
    addDraftReferences(key, [reference]);
    writeStored(`draft:${key}`, content);
    writeStored(`request:${key}`, JSON.stringify(request));
    writeStored(`routing:${key}`, '[]');
    writeStored(`recipients:${key}`, '[]');
    rememberReferenceIntent(key, request.requestId, [reference]);
    updateDraftDocument(key, reference, ready);
    expect(
      retryForIntent(
        JSON.stringify(request),
        content,
        '',
        '[]',
        '[]',
        referenceInputs(draftReferences(key)),
        draftReferences(key),
        readStored(`reference-intent:${key}`),
      )?.requestId,
    ).toBe(request.requestId);
    acknowledgeDraft(key, request);
    expect(readStored(`draft:${key}`)).toBe('');
    expect(draftReferences(key)).toEqual([]);
    expect(readStored(`reference-intent:${key}`)).toBe('');
  });

  it('replaces an explicitly selected extraction while batch handoffs preserve draft choices', () => {
    const key = 'document-explicit-selection';
    const previous = {
      ...reference,
      document: { ...queued, status: 'failed' as const },
    };
    const selected = {
      ...reference,
      document: { ...ready, id: 'selected-extraction' },
    };
    addDraftReferences(key, [previous]);
    addDraftReferences(key, [selected]);
    expect(draftReferences(key)).toEqual([previous]);
    selectDraftReference(key, selected);
    expect(draftReferences(key)).toEqual([
      { ...selected, documentId: selected.document.id },
    ]);
    updateDraftDocument(key, previous, ready);
    expect(draftReferences(key)).toEqual([
      { ...selected, documentId: selected.document.id },
    ]);
  });

  it('keeps explicit historical Post only evidence and preserves a replacement after an uncertain send', () => {
    const key = 'document-post-only-explicit';
    const old = { ...reference, document: ready };
    selectDraftReference(key, old);
    const sources = draftReferences(key);
    const request = {
      requestId: 'post-old-extraction',
      content: 'Keep the selected evidence.',
      postOnly: true,
      threadRootId: '',
      recipientAgentIds: [],
      references: referenceInputs(sources),
    };
    expect(request.references[0].documentId).toBe(ready.id);
    writeStored(`draft:${key}`, request.content);
    writeStored(`request:${key}`, JSON.stringify(request));
    writeStored(`routing:${key}`, '[]');
    writeStored(`recipients:${key}`, '[]');
    rememberReferenceIntent(key, request.requestId, sources);
    selectDraftReference(key, {
      ...old,
      document: { ...ready, id: 'chosen-replacement' },
    });
    const current = draftReferences(key);
    expect(
      retryForIntent(
        JSON.stringify(request),
        request.content,
        '',
        '[]',
        '[]',
        referenceInputs(current),
        current,
        readStored(`reference-intent:${key}`),
      ),
    ).toBeNull();
    acknowledgeDraft(key, request);
    expect(readStored(`draft:${key}`)).toBe(request.content);
    expect(draftReferences(key)[0].document?.id).toBe('chosen-replacement');
  });

  it('retains explicit selection after a request with no artifact and keeps an uncertain agent request replayable', () => {
    const key = 'document-selection-after-pending';
    const request = {
      requestId: 'post-pending',
      content: 'Keep this file.',
      postOnly: true,
      threadRootId: '',
      references: referenceInputs([reference]),
    };
    addDraftReferences(key, [reference]);
    rememberReferenceIntent(key, request.requestId, [reference]);
    selectDraftReference(key, { ...reference, document: ready });
    const current = draftReferences(key);
    expect(
      retryForIntent(
        JSON.stringify(request),
        request.content,
        '',
        'null',
        'null',
        referenceInputs(current),
        current,
        readStored(`reference-intent:${key}`),
      ),
    ).toBeNull();
    const agentRequest = {
      ...request,
      requestId: 'agent-receipt',
      postOnly: false,
      references: referenceInputs(current),
    };
    rememberReferenceIntent(key, agentRequest.requestId, current);
    // Removing the Room default may change the current UI to Post only, but
    // it must not rewrite the saved agent request or its selected artifact.
    expect(
      retryForIntent(
        JSON.stringify(agentRequest),
        agentRequest.content,
        '',
        'null',
        'null',
        referenceInputs(current),
        current,
        readStored(`reference-intent:${key}`),
      ),
    ).toEqual(agentRequest);
  });
});
