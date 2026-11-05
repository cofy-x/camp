import { useEffect, useId, useRef, useState } from 'react';
import { Button, PixelIcon, Select } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { flushPrivateDrafts } from '../../team/draftRegistry';
import { useRecordedOperation } from '../../shared/useRecordedOperation';
import { DraftRecovery } from '../reviews/DraftRecovery';
import { EvidenceChooser, EvidenceRecord } from '../workitems/WorkItemEvidence';
import type { Message } from '../workspace/api';
import {
  memoryApi,
  memorySourceLabel,
  type Memory,
  type MemoryInput,
  type MemoryProvenance,
} from './api';

type MemoryDraft = Omit<MemoryInput, 'requestId'>;

function MemoryForm({
  roomId,
  memory,
  sourceMessage,
  onSaved,
  onClose,
}: {
  roomId: string;
  memory?: Memory;
  sourceMessage?: Message;
  onSaved: () => void;
  onClose: () => void;
}) {
  const contentId = useId();
  const draft = usePrivateDraft<MemoryDraft | null>(
    'conversation',
    `memory:${roomId}:${memory?.id ?? `new${sourceMessage ? `:${sourceMessage.id}` : ''}`}`,
    null,
  );
  const values = draft.value ?? {
    revision: memory?.revision ?? 0,
    content: memory?.content ?? sourceMessage?.content ?? '',
    provenance:
      memory?.provenance ??
      (sourceMessage
        ? { kind: 'message' as const, messageId: sourceMessage.id }
        : { kind: 'human_note' as const }),
  };
  const operation = useRecordedOperation(
    `memory-save:${roomId}:${memory?.id ?? `new${sourceMessage ? `:${sourceMessage.id}` : ''}`}`,
    memoryApi,
    () => {
      draft.setValue(null);
      onSaved();
    },
  );
  const stale = values.revision !== (memory?.revision ?? 0);
  const disabled = operation.blocked || !draft.loaded || Boolean(draft.error);

  function edit(change: Partial<MemoryDraft>) {
    draft.setValue({ ...values, ...change });
  }

  return (
    <form
      className="work-item-form"
      onSubmit={(event) => {
        event.preventDefault();
        void operation.run((requestId) =>
          memoryApi.save(roomId, memory?.id ?? '', { ...values, requestId }),
        );
      }}
    >
      <div className="automation-heading">
        <h3>{memory ? 'Revise note' : 'New team note'}</h3>
        <Button onClick={onClose}>Back to Team notes</Button>
      </div>
      <DraftRecovery draft={draft} preview={(value) => value?.content ?? ''} />
      {stale && (
        <p className="app-error" role="alert">
          This memory changed. Your text is preserved. Review the current
          revision before saving.{' '}
          <Button
            disabled={disabled}
            onClick={() => edit({ revision: memory?.revision ?? 0 })}
          >
            Use Current Revision
          </Button>
        </p>
      )}
      <fieldset disabled={disabled}>
        <div className="automation-input">
          <label htmlFor={contentId}>Note</label>
          <textarea
            id={contentId}
            className="p9-field"
            required
            rows={6}
            value={values.content}
            onChange={(event) => edit({ content: event.target.value })}
          />
        </div>
        <details>
          <summary>Source</summary>
          <Select
            aria-label="Source"
            value={values.provenance.kind}
            onChange={(event) =>
              edit({
                provenance: {
                  kind: event.target.value as MemoryProvenance['kind'],
                },
              })
            }
          >
            <option value="human_note">Human note</option>
            <option value="source_event">Source event</option>
            {values.provenance.kind !== 'human_note' &&
              values.provenance.kind !== 'source_event' && (
                <option value={values.provenance.kind}>
                  {memorySourceLabel[values.provenance.kind]}
                </option>
              )}
          </Select>
          {values.provenance.kind === 'source_event' && (
            <EvidenceChooser
              roomId={roomId}
              selected={
                values.provenance.sourceEventId
                  ? [values.provenance.sourceEventId]
                  : []
              }
              onChange={(ids) =>
                edit({
                  provenance: {
                    kind: 'source_event',
                    sourceEventId: ids.at(-1) ?? '',
                  },
                })
              }
            />
          )}
          {values.provenance.kind === 'message' && (
            <p>Saved from a Room message.</p>
          )}
        </details>
      </fieldset>
      {operation.feedback}
      <Button
        primary
        type="submit"
        disabled={
          disabled ||
          stale ||
          !values.content.trim() ||
          (values.provenance.kind === 'source_event' &&
            !values.provenance.sourceEventId)
        }
      >
        {operation.busy ? 'Saving…' : 'Save note'}
      </Button>
    </form>
  );
}

function MemoryRecord({
  memory,
  onEdit,
  onChanged,
}: {
  memory: Memory;
  onEdit: () => void;
  onChanged: () => void;
}) {
  const { user } = useAccount();
  const team = useTeam();
  const [historical, setHistorical] = useState<Memory | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const visible = historical ?? memory;
  const canEdit = memory.authorUserId === user.id || user.role === 'admin';
  const operation = useRecordedOperation(
    `memory-retract:${memory.id}`,
    memoryApi,
    onChanged,
  );

  async function read(revision?: number) {
    setLoading(true);
    try {
      const { memory } = await memoryApi.get(visible.id, revision);
      setHistorical(revision ? memory : null);
      setError('');
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not read this revision.',
      );
    } finally {
      setLoading(false);
    }
  }

  return (
    <li className="work-item-record memory-record">
      {historical && (
        <strong>
          Historical revision {historical.revision}
          {historical.current ? ' · Current' : ''}
        </strong>
      )}
      <pre>{visible.content}</pre>
      <small>
        {memorySourceLabel[visible.provenance.kind]} ·{' '}
        {new Date(visible.createdAt).toLocaleString()}
        {!historical && ` · Revision ${visible.revision}`}
      </small>
      {visible.provenance.sourceEventId && (
        <EvidenceRecord id={visible.provenance.sourceEventId} />
      )}
      {visible.provenance.messageId && (
        <Button
          onClick={() =>
            void team.navigate({
              roomId: visible.roomId,
              messageId: visible.provenance.messageId,
            })
          }
        >
          Original message
        </Button>
      )}
      {visible.provenance.path && (
        <details>
          <summary>{visible.provenance.path}</summary>
          <dl className="automation-facts">
            <dt>File revision</dt>
            <dd>{visible.provenance.commit}</dd>
          </dl>
        </details>
      )}
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      <div className="app-actions">
        {visible.revision > 1 && (
          <Button
            disabled={loading}
            onClick={() => void read(visible.revision - 1)}
          >
            Previous Revision
          </Button>
        )}
        {historical && (
          <Button disabled={loading} onClick={() => void read()}>
            Current Revision
          </Button>
        )}
        {!historical && canEdit && (
          <>
            <Button disabled={operation.blocked} onClick={onEdit}>
              Revise…
            </Button>
            <Button
              disabled={operation.blocked}
              onClick={() =>
                void operation.run((requestId) =>
                  memoryApi.retract(memory.id, memory.revision, requestId),
                )
              }
            >
              Remove note
            </Button>
          </>
        )}
      </div>
      {operation.feedback}
    </li>
  );
}

export function RoomMemory({
  roomId,
  initialMessage,
  initialRequest,
}: {
  roomId: string;
  initialMessage?: Message;
  initialRequest?: number;
}) {
  const team = useTeam();
  const [query, setQuery] = useState('');
  const [search, setSearch] = useState('');
  const [memories, setMemories] = useState<Memory[] | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [selectedMemory, setSelectedMemory] = useState<Memory | undefined>();
  const [sourceMessage, setSourceMessage] = useState(initialMessage);
  useEffect(() => {
    setSourceMessage(initialMessage);
    setSelectedMemory(undefined);
    setEditing(initialMessage ? 'new' : null);
  }, [initialMessage, initialRequest]);
  const navigation = useRef(0);
  useEffect(
    () => () => {
      navigation.current++;
    },
    [],
  );
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    void memoryApi
      .list(roomId, search, controller.signal)
      .then(({ memories }) => {
        if (!controller.signal.aborted) {
          setMemories(memories);
          setError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not read room memory.',
          );
      });
    return () => controller.abort();
  }, [roomId, search, team.revision, retry]);

  async function select(value: string | null) {
    const expected = ++navigation.current;
    if ((await flushPrivateDrafts()) && expected === navigation.current) {
      setSelectedMemory(memories?.find((memory) => memory.id === value));
      setSourceMessage(undefined);
      setEditing(value);
    }
  }

  const memory =
    memories?.find((memory) => memory.id === editing) ?? selectedMemory;
  return (
    <section aria-label="Room memory" className="work-items">
      <div className="work-item-index" hidden={editing !== null}>
        <form
          className="automation-view-controls"
          onSubmit={(event) => {
            event.preventDefault();
            setSearch(query);
            setRetry((value) => value + 1);
          }}
        >
          <label>
            Find notes{' '}
            <input
              className="p9-field"
              type="search"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </label>
          <div className="app-actions">
            <Button type="submit">Search notes</Button>
            <Button onClick={() => void select('new')}>New note…</Button>
          </div>
        </form>
        {error && (
          <p className="app-error" role="alert">
            {error}{' '}
            <Button onClick={() => setRetry((value) => value + 1)}>
              Retry notes
            </Button>
          </p>
        )}
        {!memories && !error && <p role="status">Loading notes…</p>}
        {memories && !memories.length && !error && (
          <div className="camp-empty">
            <PixelIcon name="document" size={32} />
            <p>{search ? 'No notes match.' : 'No team notes yet.'}</p>
          </div>
        )}
        <ul className="work-item-list">
          {memories?.map((memory) => (
            <MemoryRecord
              key={memory.id}
              memory={memory}
              onEdit={() => void select(memory.id)}
              onChanged={() => {
                setRetry((value) => value + 1);
                team.refresh();
              }}
            />
          ))}
        </ul>
        {memories?.length === 20 && (
          <p>Showing up to 20 current notes. Search to narrow the results.</p>
        )}
      </div>
      {editing !== null && (editing === 'new' || memory) && (
        <MemoryForm
          key={`${editing}:${sourceMessage?.id ?? ''}`}
          roomId={roomId}
          memory={memory}
          sourceMessage={sourceMessage}
          onClose={() => void select(null)}
          onSaved={() => {
            setEditing(null);
            setRetry((value) => value + 1);
            team.refresh();
          }}
        />
      )}
    </section>
  );
}
