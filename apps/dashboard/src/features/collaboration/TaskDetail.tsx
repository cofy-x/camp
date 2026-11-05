import { useId, useLayoutEffect, useRef, useState } from 'react';
import {
  Button,
  Checkbox,
  Select,
  Tabs,
  type DesktopAPI,
} from '@cofy-x/camp-platinum';
import { taskStatus } from './api';
import { AssistantContent } from '../workspace/AssistantContent';
import { TaskHistory } from './TaskHistory';
import { useTaskController } from './useTaskController';
import { TaskAutomation } from '../automation/TaskAutomation';

function TaskGoal({ goal }: { goal: string }) {
  const id = useId();
  const heading = useRef<HTMLHeadingElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflow, setOverflow] = useState(false);
  useLayoutEffect(() => {
    const node = heading.current;
    if (!node || expanded) return;
    let active = true;
    const measure = () => {
      if (active) setOverflow(node.scrollHeight > node.clientHeight + 1);
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    void document.fonts.ready.then(measure);
    return () => {
      active = false;
      observer.disconnect();
    };
  }, [goal, expanded]);

  return (
    <div className="collaboration-task-goal">
      <h2
        ref={heading}
        id={id}
        className={`collaboration-title${expanded ? ' is-expanded' : ''}`}
      >
        {goal}
      </h2>
      {(overflow || expanded) && (
        <Button
          aria-controls={id}
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
        >
          {expanded ? 'Show less' : 'Show full goal'}
        </Button>
      )}
    </div>
  );
}

export function TaskDetail({
  id,
  desktop,
  onClose,
  onInspect,
  backLabel = 'Back to Collaborations',
}: {
  id: string;
  desktop: DesktopAPI;
  onClose: () => void;
  onInspect: (id: string) => void;
  backLabel?: string;
}) {
  const inputId = useId();
  const [tab, setTab] = useState('overview');
  const {
    team,
    user,
    task,
    error,
    loadError,
    notice,
    pending,
    content,
    setContent,
    setAgentId,
    continueOpen,
    setContinueOpen,
    fromLatest,
    setFromLatest,
    refreshRoutes,
    setRefreshRoutes,
    addTurns,
    setAddTurns,
    addModelRequests,
    setAddModelRequests,
    retryAvailable,
    setParticipantIds,
    roomAgents,
    setRoomAgents,
    membersError,
    setMembersError,
    setMembersRevision,
    selectedParticipantIds,
    nextAgents,
    nextAgentId,
    invalidParticipants,
    participantOptions,
    submit,
    retry,
    cancel,
  } = useTaskController(id, desktop);
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
        <Button onClick={onClose}>{backLabel}</Button>
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
              <Button disabled={pending} onClick={() => void retry()}>
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
            <TaskGoal goal={task.goal} />
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
            <TaskHistory
              task={task}
              active={tab === 'activity'}
              onInspect={onInspect}
            />
            {tab === 'setup' && (
              <div className="collaboration-tab-content">
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
                <TaskAutomation task={task} manage={manage} desktop={desktop} />
              </div>
            )}
          </Tabs>
        </>
      )}
    </section>
  );
}
