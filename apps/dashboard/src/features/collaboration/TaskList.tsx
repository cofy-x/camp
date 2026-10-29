import { Button } from '@cofy-x/camp-platinum';
import { taskStatus, type Task } from './api';

export function TaskList({
  tasks,
  loading,
  error,
  onOpen,
  onStart,
  onClose,
  onRetry,
}: {
  tasks: Task[];
  loading: boolean;
  error: string;
  onOpen: (id: string) => void;
  onStart: () => void;
  onClose: () => void;
  onRetry: () => void;
}) {
  return (
    <section
      className="workspace-panel collaboration-panel"
      aria-label="Collaborations"
    >
      <div className="workspace-panel-heading">
        <h2>Collaborations</h2>
        <div className="app-actions">
          <Button primary onClick={onStart}>
            Start collaboration…
          </Button>
          <Button onClick={onClose}>Back to Chat</Button>
        </div>
      </div>
      {error && (
        <p className="app-error" role="alert">
          {error} <Button onClick={onRetry}>Retry</Button>
        </p>
      )}
      {loading && <p role="status">Loading collaborations…</p>}
      {!loading && !tasks.length && (
        <div className="collaboration-empty">
          <h3>Give your Agents a shared goal</h3>
          <p>
            Choose the participants and let them take turns on a task draft.
            They can ask for your input and continue while you are away.
            Finished work returns here for review.
          </p>
          <p>Ordinary @ messages still request one Agent response.</p>
        </div>
      )}
      <ul className="collaboration-list">
        {tasks.map((task) => (
          <li key={task.id}>
            <button
              className="collaboration-task-link"
              onClick={() => onOpen(task.id)}
            >
              <strong>{task.goal}</strong>
              <span>{taskStatus[task.status]}</span>
              <small>
                {task.participants.map(({ agent }) => agent.name).join(' · ')}
              </small>
              {task.question && !task.question.answer && (
                <p>{task.question.text}</p>
              )}
              {task.error && <p>{task.error}</p>}
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

export function TaskAttention({
  tasks,
  onOpen,
  onAll,
}: {
  tasks: Task[];
  onOpen: (id: string) => void;
  onAll: () => void;
}) {
  const attention = tasks.filter((task) =>
    ['waiting_for_person', 'needs_attention', 'interrupted'].includes(
      task.status,
    ),
  );
  if (!tasks.length) return null;
  return (
    <section
      className="collaboration-attention"
      aria-label="Collaboration updates"
    >
      {attention.length ? (
        <>
          <strong>
            {attention.length} collaboration{attention.length === 1 ? '' : 's'}{' '}
            need attention
          </strong>
          <Button onClick={() => onOpen(attention[0].id)}>Open next</Button>
        </>
      ) : (
        <span>
          {tasks.filter((task) =>
            ['queued', 'running', 'publishing', 'cancelling'].includes(
              task.status,
            ),
          ).length
            ? `${tasks.filter((task) => ['queued', 'running', 'publishing', 'cancelling'].includes(task.status)).length} collaborations in progress`
            : tasks.some((task) => task.status === 'completed')
              ? 'Completed collaborations'
              : 'Saved collaborations'}
        </span>
      )}
      <Button onClick={onAll}>View collaborations</Button>
    </section>
  );
}
