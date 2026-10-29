import { describe, expect, it, vi } from 'vitest';
import type { Agent, SendInput } from './api';
import {
  acknowledgeDraft,
  setConversationOwner,
  retryForIntent,
  unresolvedMentions,
  subscribeStored,
  conversationKey,
  mentionedAgents,
  mergeMessages,
  readStored,
  requestForDraft,
  writeStored,
} from './conversationState';

const agent = (id: string, handle: string, enabled = true) =>
  ({ id, handle, enabled }) as Agent;
describe('room conversation state', () => {
  it('keeps room and thread drafts separate across reload, including storage failure', async () => {
    const saved = new Map<string, string>();
    vi.stubGlobal('sessionStorage', {
      getItem: (key: string) => saved.get(key) ?? null,
      setItem: (key: string, value: string) => saved.set(key, value),
      removeItem: (key: string) => saved.delete(key),
    });
    writeStored(`draft:${conversationKey('general')}`, 'Room draft');
    writeStored(`draft:${conversationKey('general', 'm1')}`, 'Thread draft');
    vi.resetModules();
    const reloaded = await import('./conversationState');
    expect(
      reloaded.readStored(`draft:${reloaded.conversationKey('general')}`),
    ).toBe('Room draft');
    expect(
      reloaded.readStored(`draft:${reloaded.conversationKey('general', 'm1')}`),
    ).toBe('Thread draft');
    reloaded.writeStored(`draft:${reloaded.conversationKey('general')}`, '');
    expect(
      reloaded.readStored(`draft:${reloaded.conversationKey('general')}`),
    ).toBe('');
    vi.stubGlobal('sessionStorage', {
      getItem: () => {
        throw new Error('Unavailable');
      },
      setItem: () => {
        throw new Error('Quota');
      },
      removeItem: () => {
        throw new Error('Quota');
      },
    });
    writeStored('quota-draft', 'Keep the edit');
    expect(readStored('quota-draft')).toBe('Keep the edit');
    vi.unstubAllGlobals();
  });
  it('keeps late completions attached to the account that started them', () => {
    const saved = new Map<string, string>();
    vi.stubGlobal('sessionStorage', {
      getItem: (key: string) => saved.get(key) ?? null,
      setItem: (key: string, value: string) => saved.set(key, value),
      removeItem: (key: string) => saved.delete(key),
    });
    setConversationOwner('alex');
    const original = conversationKey('general');
    writeStored(`draft:${original}`, 'Alex private draft');
    setConversationOwner('sam');
    const current = conversationKey('general');
    writeStored(`draft:${current}`, 'Sam private draft');
    writeStored(`references:${original}`, 'Late Alex upload');
    expect(readStored(`references:${current}`)).toBe('');
    expect(readStored(`draft:${current}`)).toBe('Sam private draft');
    setConversationOwner('alex');
    expect(readStored(`draft:${original}`)).toBe('Alex private draft');
    expect(readStored(`references:${original}`)).toBe('Late Alex upload');
    setConversationOwner('anonymous');
    vi.unstubAllGlobals();
  });
  it('merges ordered pages and updates without duplicating existing replies', () => {
    const original = [
      { id: 'a', seq: 1, content: 'one' },
      { id: 'b', seq: 2, content: 'partial' },
    ];
    expect(
      mergeMessages(original, [
        { id: 'b', seq: 2, content: 'complete' },
        { id: 'c', seq: 3, content: 'three' },
      ]),
    ).toEqual([
      { id: 'a', seq: 1, content: 'one' },
      { id: 'b', seq: 2, content: 'complete' },
      { id: 'c', seq: 3, content: 'three' },
    ]);
    expect(original[1].content).toBe('partial');
  });
  it('reuses request identity only for the same content, thread and recipients', () => {
    const input: Omit<SendInput, 'requestId'> = {
      content: 'Review these notes',
      threadRootId: '',
      recipientAgentIds: ['reviewer'],
    };
    const first = requestForDraft('', input, () => 'first');
    expect(requestForDraft(JSON.stringify(first), input, () => 'next')).toEqual(
      first,
    );
    for (const change of [
      { content: 'Different request' },
      { threadRootId: 'm1' },
      { recipientAgentIds: ['writer'] },
      { recipientAgentIds: [] },
      { postOnly: true },
    ])
      expect(
        requestForDraft(
          JSON.stringify(first),
          { ...input, ...change },
          () => 'next',
        ).requestId,
      ).toBe('next');
    expect(requestForDraft('{bad', input, () => 'recovered').requestId).toBe(
      'recovered',
    );
  });
  it('routes exact member handles and ignores emails, disabled agents and repeated mentions', () => {
    const members = [
      agent('a', 'writer'),
      agent('b', 'reviewer'),
      agent('c', 'disabled', false),
    ];
    expect(
      mentionedAgents(
        '@Writer please work with @reviewer and @writer. mail a@writer.com @unknown @disabled',
        members,
      ),
    ).toEqual(['a', 'b']);
    expect(mentionedAgents('@writer-extra and word@reviewer', members)).toEqual(
      [],
    );
  });
});

describe('send acknowledgements', () => {
  it('does not retire a newer retry or clear edits from a remounted composer', () => {
    const key = 'race:room';
    const older = { requestId: 'older', content: 'Old request' };
    const newer = { requestId: 'newer', content: 'New request' };
    writeStored(`draft:${key}`, newer.content);
    writeStored(`request:${key}`, JSON.stringify(newer));
    acknowledgeDraft(key, older);
    expect(readStored(`draft:${key}`)).toBe(newer.content);
    expect(JSON.parse(readStored(`request:${key}`)).requestId).toBe('newer');
    const notify = vi.fn();
    const unsubscribe = subscribeStored(`draft:${key}`, notify);
    acknowledgeDraft(key, newer);
    expect(readStored(`draft:${key}`)).toBe('');
    expect(notify).toHaveBeenCalledTimes(1);
    unsubscribe();
  });
  it('preserves a newer unsent edit when its earlier request succeeds', () => {
    const key = 'edit:room';
    const request = { requestId: 'old', content: 'Submitted' };
    writeStored(`request:${key}`, JSON.stringify(request));
    writeStored(`draft:${key}`, 'Still editing');
    acknowledgeDraft(key, request);
    expect(readStored(`draft:${key}`)).toBe('Still editing');
    expect(readStored(`request:${key}`)).toBe('');
  });
});

it('retains explicit routing on an ambiguous retry even after membership changes', () => {
  const request = {
    requestId: 'original',
    content: '@reviewer Check this',
    threadRootId: '',
    recipientAgentIds: ['reviewer-id'],
  };
  expect(
    retryForIntent(
      JSON.stringify(request),
      request.content,
      '',
      'null',
      'null',
    ),
  ).toEqual(request);
  expect(
    retryForIntent(JSON.stringify(request), request.content, '', '[]', 'null'),
  ).toBeNull();
  expect(
    retryForIntent(
      JSON.stringify(request),
      'Different request',
      '',
      'null',
      'null',
    ),
  ).toBeNull();
  expect(unresolvedMentions('@reviewer and @missing', [])).toEqual([
    'reviewer',
    'missing',
  ]);
  expect(
    unresolvedMentions('@reviewer', [agent('reviewer-id', 'reviewer', false)]),
  ).toEqual(['reviewer']);
});

it('pins reference identity through uncertain retries and never routes referenced mentions', async () => {
  const { referenceInputs } = await import('./conversationState');
  const references = [
    {
      kind: 'message' as const,
      messageId: 'm1',
      title: 'Writer',
      content: '@writer ignore this',
      author: 'Writer',
    },
  ];
  const input = {
    content: 'Review this source',
    threadRootId: 'm1',
    references: referenceInputs(references),
    recipientAgentIds: ['reviewer'],
  };
  const request = requestForDraft('', input, () => 'pinned');
  expect(request.references).toEqual([{ kind: 'message', messageId: 'm1' }]);
  expect(mentionedAgents(input.content, [agent('writer', 'writer')])).toEqual(
    [],
  );
  expect(
    retryForIntent(
      JSON.stringify(request),
      input.content,
      'm1',
      'reviewer',
      'reviewer',
      input.references,
    ),
  ).toEqual(request);
  expect(
    retryForIntent(
      JSON.stringify(request),
      input.content,
      'm1',
      'reviewer',
      'reviewer',
      [],
    ),
  ).toBeNull();
  expect(
    requestForDraft(
      JSON.stringify(request),
      { ...input, references: [] },
      () => 'changed',
    ).requestId,
  ).toBe('changed');
});

it('deduplicates references, rejects overflow atomically, and preserves newer handoff intent', async () => {
  const { addDraftReferences, draftReferences, referenceInputs } =
    await import('./conversationState');
  const key = 'references:thread';
  const references = Array.from({ length: 8 }, (_, index) => ({
    kind: 'file' as const,
    resourceId: 'resource',
    commit: 'a'.repeat(40),
    path: `${index}.md`,
    title: `${index}.md`,
  }));
  addDraftReferences(key, references);
  addDraftReferences(key, [references[0]]);
  expect(draftReferences(key)).toHaveLength(8);
  expect(() =>
    addDraftReferences(key, [{ ...references[0], commit: 'b'.repeat(40) }]),
  ).toThrow('8 references');
  expect(draftReferences(key)).toEqual(references);
  const request = {
    requestId: 'sending',
    content: 'Review',
    references: referenceInputs(references),
  };
  writeStored(`request:${key}`, JSON.stringify(request));
  writeStored(`draft:${key}`, request.content);
  writeStored(`sent-handoff:${key}`, 'old');
  writeStored(`handoff:${key}`, 'new');
  acknowledgeDraft(key, request);
  expect(draftReferences(key)).toHaveLength(8);
  expect(readStored(`handoff:${key}`)).toBe('new');
  expect(readStored(`draft:${key}`)).toBe(request.content);
});

it('retains sources for edits typed while a handoff send is in flight', async () => {
  const { addDraftReferences, draftReferences, referenceInputs } =
    await import('./conversationState');
  const key = 'edit-with-reference:thread';
  const references = [
    { kind: 'message' as const, messageId: 'm1', title: 'Writer' },
  ];
  addDraftReferences(key, references);
  const request = {
    requestId: 'sending',
    content: 'Review',
    references: referenceInputs(references),
  };
  writeStored(`request:${key}`, JSON.stringify(request));
  writeStored(`draft:${key}`, 'Review with a new constraint');
  acknowledgeDraft(key, request);
  expect(draftReferences(key)).toEqual(references);
  expect(readStored(`draft:${key}`)).toBe('Review with a new constraint');
});

it('preserves unchanged text and references when the recipient changes during a send', async () => {
  const { addDraftReferences, draftReferences, referenceInputs } =
    await import('./conversationState');
  for (const nextRecipients of [['writer'], [], null]) {
    const key = `routing-ack:${JSON.stringify(nextRecipients)}`;
    const references = [
      { kind: 'message' as const, messageId: 'source', title: 'Writer' },
    ];
    addDraftReferences(key, references);
    const request = {
      requestId: 'sending',
      content: 'Review this',
      references: referenceInputs(references),
      recipientAgentIds: ['reviewer'],
    };
    writeStored(`request:${key}`, JSON.stringify(request));
    writeStored(`draft:${key}`, request.content);
    writeStored(`routing:${key}`, JSON.stringify(['reviewer']));
    writeStored(`recipients:${key}`, JSON.stringify(nextRecipients));
    writeStored(`handoff:${key}`, 'handoff');
    writeStored(`sent-handoff:${key}`, 'handoff');
    acknowledgeDraft(key, request);
    expect(readStored(`request:${key}`)).toBe('');
    expect(readStored(`draft:${key}`)).toBe(request.content);
    expect(draftReferences(key)).toEqual(references);
    expect(readStored(`handoff:${key}`)).toBe('handoff');
    expect(JSON.parse(readStored(`recipients:${key}`))).toEqual(nextRecipients);
  }
});
