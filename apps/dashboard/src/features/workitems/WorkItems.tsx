import { useEffect, useRef, useState } from 'react';
import { Button, PixelIcon, Select } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { flushPrivateDrafts } from '../../team/draftRegistry';
import type { Agent, Message, WorkspaceStatus } from '../workspace/api';
import {
  automationApi,
  type AutomationPolicy,
  type SourceEvent,
} from '../automation/api';
import { WorkItemDetail } from './WorkItemDetail';
import { WorkItemForm } from './WorkItemForm';
import { workItemsApi, type WorkItemPage } from './api';
import { workItemStatus } from './presentation';
import './workitems.css';

export function WorkItems({
  roomId,
  members,
  policies: providedPolicies,
  defaultAgentId,
  initialItemId,
  initialItemRequest,
  sourceEvent,
  sourceMessage,
  availability,
  onOpenTask,
}: {
  roomId: string;
  members: Agent[];
  policies?: AutomationPolicy[];
  defaultAgentId?: string;
  initialItemId?: string;
  initialItemRequest?: number;
  sourceEvent?: SourceEvent;
  sourceMessage?: Message;
  availability?: Pick<WorkspaceStatus, 'available' | 'reason'>;
  onOpenTask: (id: string, workItemId?: string) => void;
}) {
  const team = useTeam();
  const [selection, setSelection] = useState(
    sourceEvent || sourceMessage ? 'create' : (initialItemId ?? ''),
  );
  const [creationSource, setCreationSource] = useState(sourceEvent);
  const [creationMessage, setCreationMessage] = useState(sourceMessage);
  const navigation = useRef(0);
  const [state, setState] = useState('all');
  const [after, setAfter] = useState(0);
  const [previous, setPrevious] = useState<number[]>([]);
  const [page, setPage] = useState<WorkItemPage | null>(null);
  const [policies, setPolicies] = useState<AutomationPolicy[]>(
    providedPolicies ?? [],
  );
  const [error, setError] = useState('');
  const [policyError, setPolicyError] = useState('');
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (sourceMessage || sourceEvent) {
      setCreationSource(sourceEvent);
      setCreationMessage(sourceMessage);
      setSelection('create');
    } else if (initialItemId !== undefined) setSelection(initialItemId);
  }, [sourceEvent, sourceMessage, initialItemId, initialItemRequest]);
  useEffect(
    () => () => {
      navigation.current++;
    },
    [],
  );
  useEffect(() => {
    if (providedPolicies) return;
    const controller = new AbortController();
    void automationApi
      .policies(roomId, controller.signal)
      .then(({ policies }) => {
        if (!controller.signal.aborted) {
          setPolicies(policies);
          setPolicyError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setPolicyError(
            error instanceof Error
              ? error.message
              : 'Could not load automatic rules.',
          );
      });
    return () => controller.abort();
  }, [providedPolicies, roomId, team.revision, retry]);
  useEffect(() => {
    const controller = new AbortController();
    void workItemsApi
      .list(roomId, after, controller.signal, state)
      .then((page) => {
        if (!controller.signal.aborted) {
          setPage(page);
          setError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error ? error.message : 'Could not load work.',
          );
      });
    return () => controller.abort();
  }, [roomId, after, state, team.revision, retry]);

  async function select(next: string) {
    const expected = ++navigation.current;
    if ((await flushPrivateDrafts()) && expected === navigation.current)
      setSelection(next);
  }

  async function openTask(id: string) {
    const expected = ++navigation.current;
    if ((await flushPrivateDrafts()) && expected === navigation.current)
      onOpenTask(id, selection);
  }

  function reset() {
    setAfter(0);
    setPrevious([]);
    setPage(null);
  }

  return (
    <section className="work-items" aria-label="Room work items">
      <div className="work-item-index" hidden={Boolean(selection)}>
        <div className="work-item-toolbar">
          <Select
            label="Show"
            value={state}
            onChange={(event) => {
              setState(event.target.value);
              reset();
            }}
          >
            <option value="all">All work</option>
            <option value="needs_attention">Needs attention</option>
            <option value="active">In progress</option>
            <option value="done">Done</option>
          </Select>
          <div className="app-actions">
            <Button
              onClick={() => {
                reset();
                setRetry((value) => value + 1);
              }}
            >
              {error ? 'Retry work' : 'Refresh'}
            </Button>
            <Button
              primary
              onClick={() => {
                setCreationSource(undefined);
                setCreationMessage(undefined);
                void select('create');
              }}
            >
              New work…
            </Button>
          </div>
        </div>
        {error && (
          <p className="app-error" role="alert">
            {error}
          </p>
        )}
        {!page && !error && <p role="status">Loading work…</p>}
        {page && !page.items.length && !error && (
          <div className="camp-empty">
            <PixelIcon name="document" size={32} />
            <p>{state === 'all' ? 'No work yet.' : 'No work in this view.'}</p>
          </div>
        )}
        <ul className="work-item-list">
          {page?.items.map((item) => (
            <li key={item.id}>
              <button type="button" onClick={() => void select(item.id)}>
                <span className="work-item-row-title">{item.goal}</span>
                <span>
                  {item.execution?.question && !item.execution.question.answer
                    ? item.execution.question.text
                    : item.question || workItemStatus(item)}
                </span>
                {item.dueAt && (
                  <span>
                    Due{' '}
                    {item.dueAt.length === 10
                      ? item.dueAt
                      : new Date(item.dueAt).toLocaleDateString()}
                  </span>
                )}
              </button>
            </li>
          ))}
        </ul>
        <div className="app-actions">
          {previous.length > 0 && (
            <Button
              onClick={() => {
                setAfter(previous.at(-1)!);
                setPrevious(previous.slice(0, -1));
                setPage(null);
              }}
            >
              Previous work
            </Button>
          )}
          {page?.hasMore && (
            <Button
              onClick={() => {
                setPrevious([...previous, after]);
                setAfter(page.nextAfter);
                setPage(null);
              }}
            >
              More work
            </Button>
          )}
        </div>
      </div>
      {policyError && (
        <p className="app-error" role="alert">
          {policyError}{' '}
          <Button onClick={() => setRetry((value) => value + 1)}>
            Retry automatic rules
          </Button>
        </p>
      )}
      {selection === 'create' && (
        <WorkItemForm
          key={`create:${creationMessage?.id ?? creationSource?.id ?? 'manual'}`}
          roomId={roomId}
          members={members}
          policies={providedPolicies ?? policies}
          defaultAgentId={defaultAgentId}
          sourceEvent={creationSource}
          sourceMessage={creationMessage}
          availability={availability}
          onCreated={(item) => {
            setSelection(item.id);
            reset();
          }}
          onClose={() => void select('')}
        />
      )}
      {selection && selection !== 'create' && (
        <WorkItemDetail
          key={selection}
          id={selection}
          initialRequest={
            initialItemId === selection ? initialItemRequest : undefined
          }
          members={members}
          policies={providedPolicies ?? policies}
          defaultAgentId={defaultAgentId}
          availability={availability}
          onBack={() => void select('')}
          onOpenTask={(taskId) => void openTask(taskId)}
        />
      )}
    </section>
  );
}
