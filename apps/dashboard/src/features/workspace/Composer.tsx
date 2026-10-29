import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { Button, Checkbox, PixelIcon } from '@cofy-x/camp-platinum';
import type { Agent, Resource, Room } from './api';
import { workspaceApi } from './api';
import {
  acknowledgeDraft,
  draftReferences,
  referenceInputs,
  retryForIntent,
  unresolvedMentions,
  subscribeStored,
  conversationKey,
  mentionedAgents,
  readStored,
  requestForDraft,
  writeStored,
  updateDraftDocument,
  rememberReferenceIntent,
} from './conversationState';
import { References } from './References';
import { UploadAttachments, useDraftUploads } from './UploadAttachments';
import { uploadDrafts } from './uploadState';
import { documentFormat, documentPending } from './documents';
import { useTeam } from '../../team/TeamContext';
import { useConversationDraft } from './useConversationDraft';
import { APIError } from '../../shared/api/http';

export function Composer({
  room,
  members,
  threadId,
  onSent,
  available,
  resources,
  onSource,
  focusRequest = 0,
}: {
  room: Room;
  members: Agent[];
  threadId: string;
  onSent: () => void;
  available: boolean;
  resources: Resource[];
  onSource: (messageId: string) => void;
  focusRequest?: number;
}) {
  const key = conversationKey(room.id, threadId);
  const team = useTeam();
  const privateDraft = useConversationDraft(
    key,
    `${room.id}:${threadId || 'room'}`,
  );
  const content = useSyncExternalStore(
    useCallback((listener) => subscribeStored(`draft:${key}`, listener), [key]),
    () => readStored(`draft:${key}`),
    () => '',
  );
  const savedRecipients = useSyncExternalStore(
    useCallback(
      (listener) => subscribeStored(`recipients:${key}`, listener),
      [key],
    ),
    () => readStored(`recipients:${key}`),
    () => '',
  );
  let recipients: string[] | null = null;
  try {
    recipients = JSON.parse(savedRecipients) as string[] | null;
  } catch {
    /* Use room routing. */
  }
  const savedReferences = useSyncExternalStore(
    useCallback(
      (listener) => subscribeStored(`references:${key}`, listener),
      [key],
    ),
    () => readStored(`references:${key}`),
    () => '',
  );
  const references = savedReferences ? draftReferences(key) : [];
  const preparingDocuments = references.some(
    (reference) =>
      reference.kind === 'file' &&
      documentFormat(reference.path) &&
      (!reference.document || documentPending(reference.document.status)),
  );
  const uploads = useDraftUploads(key);
  const handoff = useSyncExternalStore(
    useCallback(
      (listener) => subscribeStored(`handoff:${key}`, listener),
      [key],
    ),
    () => readStored(`handoff:${key}`),
    () => '',
  );
  const needsRecipient = Boolean(handoff && recipients === null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [picking, setPicking] = useState(false);
  const [dragging, setDragging] = useState(false);
  const dragDepth = useRef(0);
  const filePicker = useRef<HTMLInputElement>(null);
  const [mention, setMention] = useState<{
    start: number;
    end: number;
    query: string;
  } | null>(null);
  const [suggestionIndex, setSuggestionIndex] = useState(0);
  const input = useRef<HTMLTextAreaElement>(null);
  const context = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (focusRequest) input.current?.focus();
  }, [focusRequest]);
  useEffect(() => {
    if (picking && context.current) context.current.scrollTop = 0;
  }, [picking]);
  const mentionMembers = [
    ...members
      .filter((member) => member.enabled)
      .map((member) => ({
        id: member.id,
        name: member.name,
        handle: member.handle,
        kind: 'agent',
      })),
    ...team.members
      .filter((member) => member.enabled)
      .map((member) => ({
        id: member.id,
        name: member.name,
        handle: member.username,
        kind: 'person',
      })),
  ];
  const suggestions = mention
    ? mentionMembers.filter((member) =>
        member.handle.toLowerCase().startsWith(mention.query.toLowerCase()),
      )
    : [];
  const mentioned = mentionedAgents(content, members);
  const effective =
    recipients === null
      ? mentioned
      : recipients.length
        ? [...new Set([...recipients, ...mentioned])]
        : [];
  const postOnly = recipients?.length === 0;
  const hasAgentWork = effective.length > 0;

  function change(value: string) {
    writeStored(`draft:${key}`, value);
  }

  function choose(value: string[] | null) {
    writeStored(`recipients:${key}`, JSON.stringify(value));
  }

  function attach(files: File[]) {
    if (pending || !files.length) return;
    try {
      uploadDrafts.add(key, room.id, files);
      setError('');
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not attach these files.',
      );
    }
  }

  function complete(agent: { handle: string }) {
    if (!mention) return;
    const next = `${content.slice(0, mention.start)}@${agent.handle} ${content.slice(mention.end)}`;
    const caret = mention.start + agent.handle.length + 2;
    change(next);
    setMention(null);
    requestAnimationFrame(() => {
      input.current?.focus();
      input.current?.setSelectionRange(caret, caret);
    });
  }

  const routingIntent = JSON.stringify(recipients);
  const submittedReferences = referenceInputs(references);
  const retry = retryForIntent(
    readStored(`request:${key}`),
    content.trim(),
    threadId,
    routingIntent,
    readStored(`routing:${key}`),
    submittedReferences,
    references,
    readStored(`reference-intent:${key}`),
  );

  async function send() {
    if (
      !privateDraft.loaded ||
      pending ||
      uploads.length > 0 ||
      (preparingDocuments && hasAgentWork && !retry) ||
      needsRecipient ||
      !content.trim() ||
      (!available && hasAgentWork && !retry)
    )
      return;
    const text = content.trim();
    const unknown = unresolvedMentions(text, members).filter(
      (handle) =>
        !team.members.some(
          (person) => person.enabled && person.username === handle,
        ),
    );
    if (!retry && recipients?.length !== 0 && unknown.length) {
      setError(
        `${unknown.map((handle) => `@${handle}`).join(', ')} is not a team member or an enabled agent in this room. Check the handle, or choose Post only.`,
      );
      return;
    }
    const request =
      retry ??
      requestForDraft('', {
        content: text,
        references: submittedReferences,
        threadRootId: threadId,
        ...(effective !== undefined ? { recipientAgentIds: effective } : {}),
        ...(postOnly ? { postOnly: true } : {}),
        ...(hasAgentWork ? { runnerRevision: room.runnerRevision ?? 0 } : {}),
      });
    writeStored(`sent-handoff:${key}`, handoff);
    writeStored(`routing:${key}`, routingIntent);
    writeStored(`request:${key}`, JSON.stringify(request));
    if (!retry) rememberReferenceIntent(key, request.requestId, references);
    setPending(true);
    setError('');
    try {
      await workspaceApi.send(room.id, request);
      acknowledgeDraft(key, request);
      void privateDraft.flush();
      setMention(null);
      onSent();
    } catch (error) {
      if (
        error instanceof APIError &&
        error.code === 'runner_revision_conflict'
      ) {
        writeStored(`request:${key}`, '');
        onSent();
      }
      setError(
        error instanceof Error
          ? error.message
          : 'Your message could not be sent. Your draft is preserved.',
      );
    } finally {
      setPending(false);
    }
  }

  const recipientLabel = needsRecipient
    ? 'Choose an agent'
    : postOnly
      ? 'Post only'
      : effective.length
        ? effective
            .map(
              (id) =>
                members.find((member) => member.id === id)?.name ??
                'Unavailable agent',
            )
            .join(', ')
        : 'Room discussion';

  return (
    <form
      className={`workspace-composer ${dragging ? 'is-file-drop' : ''}`}
      aria-label={threadId ? 'Thread composer' : 'Room composer'}
      onDragEnter={(event) => {
        if (!event.dataTransfer.types.includes('Files')) return;
        event.preventDefault();
        dragDepth.current += 1;
        if (!pending) setDragging(true);
      }}
      onDragOver={(event) => {
        if (!event.dataTransfer.types.includes('Files')) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = pending ? 'none' : 'copy';
      }}
      onDragLeave={(event) => {
        if (!event.dataTransfer.types.includes('Files')) return;
        dragDepth.current = Math.max(0, dragDepth.current - 1);
        if (!dragDepth.current) setDragging(false);
      }}
      onDrop={(event) => {
        if (!event.dataTransfer.types.includes('Files')) return;
        event.preventDefault();
        event.stopPropagation();
        dragDepth.current = 0;
        setDragging(false);
        if (
          Array.from(event.dataTransfer.items).some(
            (item) => item.webkitGetAsEntry?.()?.isDirectory,
          )
        ) {
          setError(
            'Attach individual files here. To use a registered Runner directory, open Library → Import from Runner.',
          );
          return;
        }
        attach(Array.from(event.dataTransfer.files));
      }}
      onSubmit={(event) => {
        event.preventDefault();
        void send();
      }}
    >
      {dragging && (
        <p className="workspace-drop-hint">
          Drop files to attach to this message
        </p>
      )}
      <div className="workspace-composer-routing">
        <Button
          type="button"
          onClick={() => setPicking(!picking)}
          aria-expanded={picking}
        >
          <PixelIcon name="agent" size={16} />
          To: {recipientLabel} ▾
        </Button>
        {(needsRecipient || postOnly) && (
          <span className="workspace-description" role="status">
            {needsRecipient
              ? 'Choose a recipient to continue'
              : 'Agent mentions will not start work'}
          </span>
        )}
      </div>
      <div className="workspace-composer-context" ref={context}>
        {picking && (
          <fieldset className="workspace-recipient-picker">
            <legend>Send to</legend>
            <div className="app-actions">
              <Button
                type="button"
                disabled={Boolean(handoff)}
                onClick={() => {
                  choose(null);
                  setPicking(false);
                }}
              >
                Room discussion
              </Button>
              <Button
                type="button"
                onClick={() => {
                  choose([]);
                  setPicking(false);
                }}
              >
                Post only
              </Button>
            </div>
            {members
              .filter((member) => member.enabled)
              .map((member) => (
                <Checkbox
                  key={member.id}
                  label={member.name}
                  checked={recipients?.includes(member.id) ?? false}
                  onChange={(event) =>
                    choose(
                      event.target.checked
                        ? [...(recipients ?? []), member.id]
                        : (recipients ?? []).filter((id) => id !== member.id),
                    )
                  }
                />
              ))}
          </fieldset>
        )}
        {readStored(`reference-note:${key}`) && (
          <p className="workspace-reference-note" role="status">
            {readStored(`reference-note:${key}`)}
          </p>
        )}
        <References
          references={references}
          resources={resources}
          onSource={onSource}
          onDocumentChange={(reference, document) =>
            updateDraftDocument(key, reference, document)
          }
          onRemove={(index) =>
            writeStored(
              `references:${key}`,
              JSON.stringify(
                references.filter((_, position) => position !== index),
              ),
            )
          }
        />
        <UploadAttachments
          entries={uploads}
          draftKey={key}
          onError={setError}
        />
      </div>
      <div className="workspace-composer-input">
        {suggestions.length > 0 && (
          <div
            className="workspace-mentions"
            id="agent-mentions"
            role="listbox"
            aria-label="Member mentions"
          >
            {suggestions.map((agent, index) => (
              <button
                id={`mention-${agent.id}`}
                role="option"
                aria-selected={index === suggestionIndex}
                key={agent.id}
                type="button"
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => complete(agent)}
              >
                <PixelIcon
                  name={agent.kind === 'agent' ? 'agent' : 'conversation'}
                  size={16}
                />
                <strong>{agent.name}</strong>
                <span>@{agent.handle}</span>
              </button>
            ))}
          </div>
        )}
        <textarea
          ref={input}
          disabled={!privateDraft.loaded}
          aria-label={threadId ? 'Message thread' : `Message ${room.name}`}
          placeholder={
            postOnly
              ? 'Write a message…'
              : hasAgentWork
                ? 'Describe what you need…'
                : '@mention an Agent to ask for help, or write a message…'
          }
          value={content}
          aria-controls={suggestions.length ? 'agent-mentions' : undefined}
          aria-autocomplete="list"
          aria-activedescendant={
            suggestions[suggestionIndex]
              ? `mention-${suggestions[suggestionIndex].id}`
              : undefined
          }
          onChange={(event) => {
            change(event.target.value);
            const caret = event.target.selectionStart;
            const match = /(?:^|\s)@([a-zA-Z0-9_-]*)$/.exec(
              event.target.value.slice(0, caret),
            );
            setMention(
              match
                ? {
                    start: caret - match[1].length - 1,
                    end: caret,
                    query: match[1],
                  }
                : null,
            );
            setSuggestionIndex(0);
          }}
          onKeyDown={(event) => {
            if (event.nativeEvent.isComposing) return;
            if (
              suggestions.length &&
              ['ArrowDown', 'ArrowUp', 'Enter', 'Escape'].includes(event.key)
            ) {
              event.preventDefault();
              if (event.key === 'Escape') setMention(null);
              else if (event.key === 'Enter')
                complete(suggestions[suggestionIndex] ?? suggestions[0]);
              else
                setSuggestionIndex(
                  (current) =>
                    (current +
                      (event.key === 'ArrowDown' ? 1 : -1) +
                      suggestions.length) %
                    suggestions.length,
                );
            } else if (
              event.key === 'Enter' &&
              (event.metaKey || event.ctrlKey)
            ) {
              event.preventDefault();
              void send();
            }
          }}
        />
      </div>
      {privateDraft.comparison && (
        <details open className="camp-draft-comparison">
          <summary>Private draft in the other session</summary>
          <pre>
            {privateDraft.comparison.value?.draft || 'No message text saved.'}
          </pre>
          <Button type="button" onClick={privateDraft.useRemote}>
            Use Other Draft
          </Button>
          <Button
            type="button"
            onClick={() => void privateDraft.resolveConflict()}
          >
            Save My Draft
          </Button>
        </details>
      )}
      {privateDraft.error && (
        <p className="app-error" role="alert">
          {privateDraft.error}{' '}
          <Button
            type="button"
            onClick={() => void privateDraft.inspectConflict()}
          >
            Compare Private Drafts
          </Button>
        </p>
      )}
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      <div className="workspace-composer-actions">
        <Button
          className="workspace-attach-button"
          type="button"
          title="Up to 8 files, 8 MiB each"
          disabled={pending || references.length + uploads.length >= 8}
          onClick={() => filePicker.current?.click()}
        >
          <PixelIcon name="document" size={16} />
          Attach files
        </Button>
        <input
          ref={filePicker}
          className="workspace-upload-input"
          type="file"
          multiple
          aria-label="Choose files to attach"
          onChange={(event) => {
            const files = Array.from(event.target.files ?? []);
            event.target.value = '';
            attach(files);
          }}
        />
        <span className="workspace-description">
          {pending
            ? 'Sending… You can keep writing.'
            : uploads.some((entry) => entry.state === 'error')
              ? 'Retry or remove unfinished attachments'
              : uploads.length
                ? 'Waiting for attachments…'
                : preparingDocuments && hasAgentWork && !retry
                  ? 'Preparing attachment text…'
                  : 'Ctrl / ⌘ + Return to send'}
        </span>
        <Button
          type="submit"
          disabled={
            !privateDraft.loaded ||
            pending ||
            uploads.length > 0 ||
            (preparingDocuments && hasAgentWork && !retry) ||
            needsRecipient ||
            !content.trim() ||
            (!available && hasAgentWork && !retry)
          }
        >
          {pending ? 'Sending…' : hasAgentWork ? 'Send to Agent' : 'Post'}
        </Button>
      </div>
      {references.some(
        (reference) =>
          reference.kind === 'file' &&
          /\.(pdf|docx?|png|jpe?g|gif|webp|svg|bmp|tiff?|avif|heic)$/i.test(
            reference.path ?? '',
          ),
      ) && (
        <p className="workspace-description workspace-attachment-note">
          Images and scanned text are not read.
        </p>
      )}
    </form>
  );
}
