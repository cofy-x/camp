import { useEffect, useId, useRef, useState } from 'react';
import {
  Button,
  Checkbox,
  Select,
  Tabs,
  type DesktopAPI,
} from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { APIError } from '../../shared/api/http';
import {
  receiptKey,
  readSessionReceipt,
  writeSessionReceipt,
  clearSessionReceipt,
} from '../../shared/sessionReceipt';
import { useTeam } from '../../team/TeamContext';
import type { Agent, Run } from '../workspace/api';
import { statusLabel, workspaceApi } from '../workspace/api';
import { tasksApi, taskStatus, type Task } from './api';
import { AssistantContent } from '../workspace/AssistantContent';

interface PendingAction {
  key: string;
  kind: 'input' | 'answer' | 'continue' | 'cancel';
  requestId: string;
  revision: number;
  content: string;
  agentId: string;
  questionId: string;
  addTurns: number;
  addModelRequests: number;
  fromLatest: boolean;
  participantAgentIds?: string[];
}

function isPendingAction(value: unknown): value is PendingAction {
  if (!value || typeof value !== 'object') return false;
  const record = value as Partial<PendingAction>;
  return (
    ['input', 'answer', 'continue', 'cancel'].includes(record.kind ?? '') &&
    ['key', 'requestId', 'content', 'agentId', 'questionId'].every(
      (key) => typeof record[key as keyof PendingAction] === 'string',
    ) &&
    Number.isInteger(record.revision) &&
    Number.isInteger(record.addTurns) &&
    Number.isInteger(record.addModelRequests) &&
    typeof record.fromLatest === 'boolean' &&
    (record.participantAgentIds === undefined ||
      (Array.isArray(record.participantAgentIds) &&
        record.participantAgentIds.every((id) => typeof id === 'string')))
  );
}

export function TaskDetail({
  id,
  desktop,
  onClose,
  onInspect,
}: {
  id: string;
  desktop: DesktopAPI;
  onClose: () => void;
  onInspect: (id: string) => void;
}) {
  const team = useTeam();
  const { user } = useAccount();
  const inputId = useId();
  const key = receiptKey(user.id, 'task-action', id);
  const [savedRequest] = useState(() =>
    readSessionReceipt(key, isPendingAction),
  );
  const [task, setTask] = useState<Task | null>(null);
  const [tab, setTab] = useState('overview');
  const [runs, setRuns] = useState<Run[]>([]);
  const [error, setError] = useState(
    savedRequest ? 'A previous action is awaiting confirmation.' : '',
  );
  const [loadError, setLoadError] = useState('');
  const [notice, setNotice] = useState('');
  const [pending, setPending] = useState(false);
  const [content, setContent] = useState(savedRequest?.content ?? '');
  const [agentId, setAgentId] = useState(savedRequest?.agentId ?? '');
  const [continueOpen, setContinueOpen] = useState(
    savedRequest?.kind === 'continue',
  );
  const [fromLatest, setFromLatest] = useState(
    savedRequest?.fromLatest ?? false,
  );
  const [refreshRoutes, setRefreshRoutes] = useState(
    Boolean(savedRequest?.participantAgentIds),
  );
  const [addTurns, setAddTurns] = useState(savedRequest?.addTurns ?? 0);
  const [addModelRequests, setAddModelRequests] = useState(
    savedRequest?.addModelRequests ?? 0,
  );
  const [retryAvailable, setRetryAvailable] = useState(Boolean(savedRequest));
  const request = useRef<PendingAction | null>(savedRequest);
  const [participantIds, setParticipantIds] = useState<string[] | null>(
    savedRequest?.participantAgentIds ?? null,
  );
  const [roomAgents, setRoomAgents] = useState<Agent[] | null>(null);
  const [membersError, setMembersError] = useState('');
  const [membersRevision, setMembersRevision] = useState(0);
  const roomId = task?.roomId;
  const activeRunId = task?.activeRunId;
  useEffect(() => {
    if (!continueOpen || !refreshRoutes || !roomId || activeRunId) return;
    const controller = new AbortController();
    void workspaceApi
      .members(roomId, controller.signal)
      .then(({ members }) => {
        if (controller.signal.aborted) return;
        setRoomAgents(members);
        setMembersError('');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setMembersError(
            error instanceof Error
              ? error.message
              : 'Could not load Room Agents.',
          );
      });
    return () => controller.abort();
  }, [continueOpen, refreshRoutes, roomId, activeRunId, membersRevision]);
  const selectedParticipantIds =
    participantIds ?? task?.participants.map(({ agent }) => agent.id) ?? [];
  const nextAgents = refreshRoutes
    ? (roomAgents ?? []).filter(
        (agent) => agent.enabled && selectedParticipantIds.includes(agent.id),
      )
    : (task?.participants.map(({ agent }) => agent) ?? []);
  const nextAgentId = agentId || task?.currentAgentId || '';
  const invalidParticipants =
    refreshRoutes &&
    (Boolean(activeRunId) ||
      !roomAgents ||
      Boolean(membersError) ||
      !selectedParticipantIds.length ||
      selectedParticipantIds.some(
        (id) => !roomAgents.some((agent) => agent.id === id && agent.enabled),
      ) ||
      !nextAgents.some((agent) => agent.id === nextAgentId));
  const participantOptions = [
    ...(roomAgents ?? []).filter((agent) => agent.enabled),
    ...(task?.participants
      .map(({ agent }) => agent)
      .filter(
        (agent) =>
          !roomAgents?.some(
            (current) => current.id === agent.id && current.enabled,
          ),
      ) ?? []),
  ];

  useEffect(() => {
    const controller = new AbortController();
    void tasksApi
      .get(id, controller.signal)
      .then((result) => {
        if (controller.signal.aborted) return;
        setTask((current) =>
          !current || result.task.revision >= current.revision
            ? result.task
            : current,
        );
        setRuns(result.runs ?? []);
        setLoadError('');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setLoadError(
            error instanceof Error
              ? error.message
              : 'Could not load this collaboration.',
          );
      });
    return () => controller.abort();
  }, [id, team.revision]);

  async function submit(
    kind: 'input' | 'answer' | 'continue' | 'cancel',
    retry = false,
  ) {
    if (
      !task ||
      pending ||
      (retryAvailable && !retry) ||
      (!retry && kind === 'continue' && invalidParticipants)
    )
      return;
    const actionKey = JSON.stringify({
      kind,
      questionId: kind === 'answer' ? task.question?.id : undefined,
      content: kind === 'cancel' ? undefined : content,
      agentId: kind === 'continue' ? agentId || task.currentAgentId : undefined,
      fromLatest: kind === 'continue' ? fromLatest : undefined,
      participantAgentIds:
        kind === 'continue' && refreshRoutes
          ? selectedParticipantIds
          : undefined,
      addTurns: kind === 'continue' ? addTurns : undefined,
      addModelRequests: kind === 'continue' ? addModelRequests : undefined,
    });
    // Preserve the original request and revision across uncertain responses,
    // including when live updates have already advanced the task.
    if (!retry && request.current?.key !== actionKey)
      request.current = {
        key: actionKey,
        kind,
        requestId: crypto.randomUUID(),
        revision: task.revision,
        content,
        agentId: agentId || task.currentAgentId,
        questionId: task.question?.id || '',
        addTurns,
        addModelRequests,
        fromLatest,
        participantAgentIds: refreshRoutes ? selectedParticipantIds : undefined,
      };
    const saved = request.current;
    if (!saved) return;
    kind = saved.kind;
    const common = { requestId: saved.requestId, revision: saved.revision };
    setPending(true);
    setError('');
    setNotice('');
    try {
      writeSessionReceipt(key, saved);
      const result =
        kind === 'answer'
          ? await tasksApi.answer(id, {
              ...common,
              questionId: saved.questionId,
              content: saved.content,
            })
          : kind === 'continue'
            ? await tasksApi.continue(id, {
                ...common,
                agentId: saved.agentId,
                content: saved.content,
                addTurns: saved.addTurns,
                addModelRequests: saved.addModelRequests,
                fromLatest: saved.fromLatest,
                participantAgentIds: saved.participantAgentIds,
              })
            : kind === 'cancel'
              ? await tasksApi.cancel(id, common)
              : await tasksApi.input(id, {
                  requestId: common.requestId,
                  content: saved.content,
                });
      setTask((current) =>
        !current || result.task.revision >= current.revision
          ? result.task
          : current,
      );
      setContent('');
      setContinueOpen(false);
      setAddTurns(0);
      setAddModelRequests(0);
      setFromLatest(false);
      setRefreshRoutes(false);
      setParticipantIds(null);
      setRoomAgents(null);
      clearSessionReceipt(key);
      request.current = null;
      setRetryAvailable(false);
      team.refresh();
      setNotice(
        kind === 'input'
          ? 'Input saved for the next turn.'
          : kind === 'answer'
            ? 'Answer saved.'
            : kind === 'cancel'
              ? 'Stop requested.'
              : 'Continuation requested.',
      );
    } catch (error) {
      const uncertain =
        !(error instanceof APIError) ||
        error.status === 0 ||
        error.status >= 500;
      setRetryAvailable(uncertain);
      if (!uncertain) {
        clearSessionReceipt(key);
        request.current = null;
      }
      if (
        error instanceof APIError &&
        ['revision_conflict', 'question_answered', 'task_finished'].includes(
          error.code,
        )
      )
        team.refresh();
      setError(
        error instanceof Error
          ? error.message
          : 'The action could not be saved.',
      );
    } finally {
      setPending(false);
    }
  }

  async function cancel() {
    const answer = await desktop.dialog({
      title: 'Stop collaboration',
      message:
        'Stop queued work and ask the active Runner to clean up. Saved task drafts remain available.',
      buttons: [
        { label: 'Keep Working', value: 'keep', cancel: true },
        { label: 'Stop', value: 'stop', default: true },
      ],
    });
    if (answer?.button === 'stop') await submit('cancel');
  }

  const mutationLocked = pending || retryAvailable;
  const currentAgent = task?.participants.find(
    ({ agent }) => agent.id === task.currentAgentId,
  )?.agent;
  const manage = Boolean(
    task && (user.role === 'admin' || task.requestedByUserId === user.id),
  );
  const stopped =
    task && ['completed', 'cancelled', 'interrupted'].includes(task.status);
  const canContinue =
    manage &&
    task &&
    ['needs_attention', 'waiting_for_person'].includes(task.status);
  const question =
    task?.status === 'waiting_for_person' &&
    task.question &&
    !task.question.answer
      ? task.question
      : null;
  const inputForm =
    task && (!stopped || continueOpen) ? (
      <form
        className="collaboration-form"
        onSubmit={(event) => {
          event.preventDefault();
          void submit(
            continueOpen ? 'continue' : question ? 'answer' : 'input',
          );
        }}
      >
        <div className="collaboration-input">
          <label htmlFor={inputId}>
            {continueOpen
              ? 'What should happen next?'
              : question
                ? 'Your answer'
                : 'Add input for the next turn'}
          </label>
          <textarea
            id={inputId}
            className="p9-field"
            rows={3}
            maxLength={16000}
            required
            value={content}
            disabled={mutationLocked}
            onChange={(event) => setContent(event.target.value)}
          />
        </div>
        <div className="app-actions">
          <Button
            type="submit"
            primary
            disabled={
              mutationLocked ||
              !content.trim() ||
              (continueOpen && invalidParticipants)
            }
          >
            {pending
              ? 'Saving…'
              : continueOpen
                ? 'Continue collaboration'
                : question
                  ? 'Answer and Continue'
                  : 'Add Input'}
          </Button>
          {continueOpen && (
            <Button
              type="button"
              disabled={mutationLocked}
              onClick={() => setContinueOpen(false)}
            >
              Cancel
            </Button>
          )}
        </div>
      </form>
    ) : null;
  return (
    <section
      className="workspace-panel collaboration-panel collaboration-detail"
      aria-label="Collaboration details"
    >
      <div className="workspace-panel-heading">
        <h2>Collaboration</h2>
        <Button onClick={onClose}>Back to Collaborations</Button>
      </div>
      {!task && !loadError && <p role="status">Loading collaboration…</p>}
      {loadError && (
        <p className="app-error" role="alert">
          {loadError} <Button onClick={team.refresh}>Refresh</Button>
        </p>
      )}
      {error && (
        <p className="app-error" role="alert">
          {error}
          {retryAvailable && (
            <>
              <span>
                Check the original action before making further changes.
              </span>
              <Button
                disabled={pending}
                onClick={() => void submit(request.current!.kind, true)}
              >
                Retry Save
              </Button>
            </>
          )}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {task && (
        <>
          <div className="collaboration-status">
            <strong>{taskStatus[task.status]}</strong>
            {currentAgent && ['queued', 'running'].includes(task.status) && (
              <span>
                {currentAgent.name}
                {task.status === 'running' ? ' · working now' : ' · next turn'}
              </span>
            )}
            <p className="collaboration-title">{task.goal}</p>
          </div>
          {task.error && (
            <p className="app-error" role="alert">
              {task.error}
            </p>
          )}
          <div className="app-actions collaboration-actions">
            {(task.publishedCommit || task.checkpointCommit) && (
              <Button
                primary={Boolean(task.publishedCommit)}
                onClick={() =>
                  void team.navigate({
                    app: 'library',
                    resourceId: task.resourceId,
                    commit: task.publishedCommit || task.checkpointCommit,
                    contextRoomId: task.roomId,
                  })
                }
              >
                {task.publishedCommit
                  ? 'Open Published Files'
                  : 'Open Task Draft'}
              </Button>
            )}
            {task.activeRunId && (
              <Button onClick={() => onInspect(task.activeRunId)}>
                View current work
              </Button>
            )}
            {canContinue && (
              <Button
                disabled={mutationLocked}
                onClick={() => setContinueOpen(!continueOpen)}
              >
                Continue…
              </Button>
            )}
            {manage && !stopped && (
              <Button
                disabled={mutationLocked || task.status === 'cancelling'}
                onClick={() => void cancel()}
              >
                Stop…
              </Button>
            )}
          </div>
          {task.checkpointCommit && !task.publishedCommit && (
            <p className="collaboration-draft-note">
              Task draft · Room files have not been changed by this
              collaboration.
            </p>
          )}
          {question && (
            <section
              className="collaboration-question"
              aria-label="Question for you"
            >
              <h3>{question.text}</h3>
              {Boolean(question.options?.length) && (
                <div className="app-actions">
                  {question.options.map((option) => (
                    <Button
                      key={option}
                      disabled={mutationLocked}
                      onClick={() => setContent(option)}
                    >
                      {option}
                    </Button>
                  ))}
                </div>
              )}
              {!continueOpen && inputForm}
            </section>
          )}
          {continueOpen && manage && (
            <fieldset className="collaboration-form" disabled={mutationLocked}>
              <legend>Continue collaboration</legend>
              <p className="collaboration-budget">
                Used {task.turnsUsed} of {task.maxTurns} turns and{' '}
                {task.modelRequestsUsed} of {task.maxModelRequests} model
                requests.
              </p>
              <Select
                label="Next Agent"
                value={
                  nextAgents.some((agent) => agent.id === nextAgentId)
                    ? nextAgentId
                    : ''
                }
                disabled={mutationLocked}
                onChange={(event) => setAgentId(event.target.value)}
              >
                <option value="" disabled>
                  Choose an Agent
                </option>
                {nextAgents.map((agent) => (
                  <option key={agent.id} value={agent.id}>
                    {agent.name}
                  </option>
                ))}
              </Select>
              <div className="collaboration-limits">
                <label>
                  Additional turns
                  <input
                    className="p9-field"
                    type="number"
                    min={0}
                    max={24}
                    value={addTurns}
                    onChange={(event) =>
                      setAddTurns(Number(event.target.value))
                    }
                  />
                </label>
                <label>
                  Additional model requests
                  <input
                    className="p9-field"
                    type="number"
                    min={0}
                    max={96}
                    value={addModelRequests}
                    onChange={(event) =>
                      setAddModelRequests(Number(event.target.value))
                    }
                  />
                </label>
              </div>
              <Checkbox
                label="Change participants or Runners"
                checked={refreshRoutes}
                disabled={mutationLocked || Boolean(task.activeRunId)}
                onChange={(event) => {
                  setRefreshRoutes(event.target.checked);
                  setRoomAgents(null);
                  setMembersError('');
                }}
              />
              {task.activeRunId && (
                <p>
                  Wait for the current turn to finish before changing
                  participants or Runners.
                </p>
              )}
              {refreshRoutes && (
                <div
                  className="collaboration-participants"
                  role="group"
                  aria-label="Participants for future turns"
                >
                  <p>
                    Choose the Agents for future turns. Continuing records their
                    current instructions and Runners. Saved task drafts stay
                    available.
                  </p>
                  {!roomAgents && !membersError && (
                    <p role="status">Loading Room Agents…</p>
                  )}
                  {membersError && (
                    <p className="app-error" role="alert">
                      {membersError}{' '}
                      <Button
                        type="button"
                        disabled={mutationLocked}
                        onClick={() => setMembersRevision((value) => value + 1)}
                      >
                        Retry Agents
                      </Button>
                    </p>
                  )}
                  {roomAgents &&
                    participantOptions.map((agent) => {
                      const available = roomAgents.some(
                        (current) => current.id === agent.id && current.enabled,
                      );
                      const selected = selectedParticipantIds.includes(
                        agent.id,
                      );
                      return (
                        <div
                          className="collaboration-participant"
                          key={agent.id}
                        >
                          <Checkbox
                            label={agent.name}
                            checked={selected}
                            disabled={
                              mutationLocked || (!available && !selected)
                            }
                            onChange={(event) => {
                              const next = event.target.checked
                                ? [...selectedParticipantIds, agent.id]
                                : selectedParticipantIds.filter(
                                    (id) => id !== agent.id,
                                  );
                              setParticipantIds(next);
                              if (!next.includes(nextAgentId))
                                setAgentId(
                                  next.find((id) =>
                                    roomAgents.some(
                                      (current) =>
                                        current.id === id && current.enabled,
                                    ),
                                  ) ?? '',
                                );
                            }}
                          />
                          {!available && (
                            <small>
                              No longer available in this Room. Remove this
                              participant to continue.
                            </small>
                          )}
                        </div>
                      );
                    })}
                  {roomAgents && invalidParticipants && (
                    <p>
                      Select available participants and a Next Agent from that
                      selection.
                    </p>
                  )}
                </div>
              )}
              <Checkbox
                label="Continue from latest Room files"
                checked={fromLatest}
                disabled={mutationLocked}
                onChange={(event) => setFromLatest(event.target.checked)}
              />
              {fromLatest && (
                <p>
                  Start a new draft from current Room files. The previous task
                  draft remains available as a reference; it will not overwrite
                  or merge into the latest files automatically.
                </p>
              )}
            </fieldset>
          )}
          {continueOpen && inputForm}
          <Tabs
            label="Collaboration details"
            value={tab}
            onChange={setTab}
            tabs={[
              { id: 'overview', label: 'Overview' },
              { id: 'activity', label: 'Activity' },
              { id: 'setup', label: 'Task setup' },
            ]}
          >
            <div
              className="collaboration-tab-content"
              hidden={tab !== 'overview'}
            >
              {task.summary && (
                <section
                  className="collaboration-summary"
                  aria-label="Collaboration summary"
                >
                  <h3>
                    {task.status === 'completed' ? 'Result' : 'Latest update'}
                  </h3>
                  <AssistantContent content={task.summary} />
                </section>
              )}
              {['cancelled', 'interrupted'].includes(task.status) && (
                <p>
                  Start a new collaboration to run again. Saved task drafts
                  remain available.
                </p>
              )}
              {!question && !continueOpen && inputForm}
              {stopped &&
                !task.summary &&
                !task.checkpointCommit &&
                !task.publishedCommit && (
                  <p>No files were saved by this collaboration.</p>
                )}
            </div>
            {tab === 'activity' && (
              <div className="collaboration-tab-content">
                <section
                  className="collaboration-history"
                  aria-label="Collaboration turns"
                >
                  <h3>Turns</h3>
                  {!runs.length && <p>No execution has started yet.</p>}
                  <ol>
                    {runs.map((run) => (
                      <li key={run.id}>
                        <Button onClick={() => onInspect(run.id)}>
                          {run.agentSnapshot.name} · {statusLabel[run.status]}
                        </Button>
                        <small>
                          {run.runnerName}
                          {run.checksDeferred
                            ? ' · Checks deferred until finish'
                            : ''}
                        </small>
                      </li>
                    ))}
                  </ol>
                </section>
                {task.inputs?.length > 0 && (
                  <section
                    className="collaboration-history"
                    aria-label="Team input"
                  >
                    <h3>Team input</h3>
                    {task.inputs.map((input) => (
                      <article key={input.id}>
                        <strong>{input.authorName}</strong>
                        <p>{input.content}</p>
                      </article>
                    ))}
                  </section>
                )}
              </div>
            )}
            {tab === 'setup' && (
              <div className="collaboration-tab-content">
                <section
                  className="collaboration-goal"
                  aria-label="Shared goal"
                >
                  <h3>Shared goal</h3>
                  <p>{task.goal}</p>
                </section>
                <section aria-label="Collaboration participants">
                  <h3>Participants</h3>
                  <ul className="collaboration-roster">
                    {task.participants.map(({ agent, runnerName }) => (
                      <li key={agent.id}>
                        <strong>{agent.name}</strong>
                        <span>{runnerName}</span>
                      </li>
                    ))}
                  </ul>
                </section>
                <section aria-label="Collaboration allowance">
                  <h3>Allowance</h3>
                  <dl className="collaboration-allowance">
                    <dt>Turns</dt>
                    <dd>
                      {task.turnsUsed} / {task.maxTurns}
                    </dd>
                    <dt>Model requests</dt>
                    <dd>
                      {task.modelRequestsUsed} / {task.maxModelRequests}
                    </dd>
                  </dl>
                </section>
                {task.previousDraftCommit && (
                  <Button
                    onClick={() =>
                      void team.navigate({
                        app: 'library',
                        resourceId: task.resourceId,
                        commit: task.previousDraftCommit,
                        contextRoomId: task.roomId,
                      })
                    }
                  >
                    Open Previous Task Draft
                  </Button>
                )}
              </div>
            )}
          </Tabs>
        </>
      )}
    </section>
  );
}
