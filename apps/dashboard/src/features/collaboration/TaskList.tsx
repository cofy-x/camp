import { Button, PixelIcon } from '@cofy-x/camp-platinum';
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
      {!loading && !error && !tasks.length && (
        <div className="camp-empty">
          <PixelIcon name="document" size={32} />
          <p>No collaborations yet.</p>
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
  const linked = tasks.length > 0 && tasks.every((task) => task.workItemId);
  if (!tasks.length) return null;
  return (
    <section
      className="collaboration-attention"
      aria-label={linked ? 'Work updates' : 'Collaboration updates'}
    >
      {attention.length ? (
        <>
          <strong>
            {linked
              ? 'Needs your attention'
              : `${attention.length} collaboration${attention.length === 1 ? '' : 's'} need attention`}
          </strong>
          <Button onClick={() => onOpen(attention[0].id)}>
            {linked ? 'Open work' : 'Open next'}
          </Button>
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
      <Button onClick={onAll}>
        {linked ? 'View work' : 'View collaborations'}
      </Button>
    </section>
  );
}
