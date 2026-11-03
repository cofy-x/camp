import { useEffect, useState } from 'react';
import { Button, Select } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import {
  clearSessionReceipt,
  readSessionReceipt,
  receiptKey,
  writeSessionReceipt,
} from '../../shared/sessionReceipt';
import { tasksApi, type Task } from '../collaboration/api';
import { automationApi, type SourceEvent, type Subscription } from './api';
import { useAutomationOperation } from './useAutomationOperation';

export function FollowEvent({ event }: { event: SourceEvent }) {
  const { user } = useAccount();
  const team = useTeam();
  const targetKey = receiptKey(user.id, 'automation-follow-target', event.id);
  const [target, setTarget] = useState(
    () =>
      readSessionReceipt(
        targetKey,
        (value): value is string => typeof value === 'string',
      ) ?? '',
  );
  const [open, setOpen] = useState(Boolean(target));
  const [tasks, setTasks] = useState<Task[] | null>(null);
  const [subscription, setSubscription] = useState<{
    taskId: string;
    current?: Subscription;
  } | null>(null);
  const [error, setError] = useState('');
  const [tasksError, setTasksError] = useState('');
  const [subscriptionError, setSubscriptionError] = useState('');
  const [retry, setRetry] = useState(0);
  const [saved, setSaved] = useState('');
  const operation = useAutomationOperation(`follow:${event.id}`, (receipt) => {
    setSaved(receipt.subscription?.taskId ?? target);
    clearSessionReceipt(targetKey);
    setTarget('');
    setOpen(false);
  });
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    void tasksApi
      .list(event.roomId, controller.signal)
      .then(({ tasks }) => {
        if (!controller.signal.aborted) {
          setTasks(
            tasks.filter(
              (task) =>
                ![
                  'completed',
                  'cancelled',
                  'interrupted',
                  'cancelling',
                ].includes(task.status) &&
                (user.role === 'admin' || task.requestedByUserId === user.id),
            ),
          );
          setTasksError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setTasksError(
            error instanceof Error
              ? error.message
              : 'Could not load active collaborations.',
          );
      });
    return () => controller.abort();
  }, [event.roomId, open, retry, team.revision, user.id, user.role]);
  useEffect(() => {
    if (!target || !open) return;
    const controller = new AbortController();
    void automationApi
      .subscriptions(target, controller.signal)
      .then(({ subscriptions }) => {
        if (!controller.signal.aborted) {
          setSubscription({
            taskId: target,
            current: subscriptions.find(
              (subscription) =>
                subscription.sourceId === event.sourceId &&
                subscription.threadKey === event.threadKey,
            ),
          });
          setSubscriptionError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setSubscriptionError(
            error instanceof Error
              ? error.message
              : 'Could not check followed conversations.',
          );
      });
    return () => controller.abort();
  }, [event.sourceId, event.threadKey, target, open, retry, team.revision]);
  const observed = subscription?.taskId === target ? subscription : null;
  const validTarget = tasks?.some((task) => task.id === target);
  const readError = tasksError || subscriptionError;
  return (
    <div className="automation-follow">
      {!open ? (
        <Button onClick={() => setOpen(true)}>Follow in Collaboration…</Button>
      ) : (
        <>
          <p>Adds context without starting work or increasing its allowance.</p>
          {(error || readError) && (
            <p className="app-error" role="alert">
              {error || readError}{' '}
              <Button onClick={() => setRetry((value) => value + 1)}>
                Retry Collaborations
              </Button>
            </p>
          )}
          {!tasks && !readError && (
            <p role="status">Loading active collaborations…</p>
          )}
          {tasks && (
            <>
              {!tasks.length && (
                <p>No active collaborations that you manage.</p>
              )}
              <Select
                label="Collaboration"
                value={target}
                disabled={operation.blocked}
                onChange={(event) => {
                  try {
                    writeSessionReceipt(targetKey, event.target.value);
                    setTarget(event.target.value);
                    setSaved('');
                    setError('');
                  } catch (error) {
                    setError(
                      error instanceof Error
                        ? error.message
                        : 'Could not retain this selection.',
                    );
                  }
                }}
              >
                <option value="">Choose a collaboration</option>
                {tasks.map((task) => (
                  <option value={task.id} key={task.id}>
                    {task.goal}
                  </option>
                ))}
              </Select>
              {observed?.current?.enabled && (
                <p>Already following this conversation.</p>
              )}
              <div className="app-actions">
                <Button
                  disabled={
                    operation.blocked ||
                    !observed ||
                    !validTarget ||
                    Boolean(error || readError) ||
                    Boolean(observed.current?.enabled)
                  }
                  onClick={() =>
                    void operation.run(async (requestId) => {
                      const receipt = await automationApi.saveSubscription(
                        target,
                        {
                          requestId,
                          revision: observed?.current?.revision ?? 0,
                          sourceId: event.sourceId,
                          threadKey: event.threadKey,
                          enabled: true,
                        },
                      );
                      return receipt;
                    })
                  }
                >
                  Follow Conversation
                </Button>
                <Button
                  disabled={operation.blocked}
                  onClick={() => setOpen(false)}
                >
                  Cancel
                </Button>
              </div>
            </>
          )}
        </>
      )}
      {saved && (
        <p role="status">
          Conversation followed in{' '}
          {tasks?.find((task) => task.id === saved)?.goal ??
            'the collaboration'}
          .
        </p>
      )}
      {operation.feedback}
    </div>
  );
}
