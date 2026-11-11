import { useState } from 'react';
import { Button, Select } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { useRead } from '../../shared/reads/useRead';
import {
  clearSessionReceipt,
  readSessionReceipt,
  receiptKey,
  writeSessionReceipt,
} from '../../shared/sessionReceipt';
import type { Task } from '../collaboration/api';
import { automationApi, type SourceEvent, type Subscription } from './api';
import { useAutomationOperation } from './useAutomationOperation';

export function FollowEvent({ event }: { event: SourceEvent }) {
  const { user } = useAccount();
  const targetKey = receiptKey(user.id, 'automation-follow-target', event.id);
  const [target, setTarget] = useState(
    () =>
      readSessionReceipt(
        targetKey,
        (value): value is string => typeof value === 'string',
      ) ?? '',
  );
  const [open, setOpen] = useState(Boolean(target));
  const [error, setError] = useState('');
  const [saved, setSaved] = useState('');
  const operation = useAutomationOperation(`follow:${event.id}`, (receipt) => {
    setSaved(receipt.subscription?.taskId ?? target);
    clearSessionReceipt(targetKey);
    setTarget('');
    setOpen(false);
  });
  const tasksRead = useRead<{ tasks: Task[] }>(
    `/rooms/${encodeURIComponent(event.roomId)}/tasks?view=summary`,
    { enabled: open },
  );
  const subscriptionRead = useRead<{ subscriptions: Subscription[] }>(
    target ? `/tasks/${encodeURIComponent(target)}/subscriptions` : null,
    { enabled: open },
  );
  const tasks = tasksRead.data
    ? tasksRead.data.tasks.filter(
        (task) =>
          !['completed', 'cancelled', 'interrupted', 'cancelling'].includes(
            task.status,
          ) &&
          (user.role === 'admin' || task.requestedByUserId === user.id),
      )
    : null;
  const observed = subscriptionRead.data
    ? {
        taskId: target,
        current: subscriptionRead.data.subscriptions.find(
          (subscription) =>
            subscription.sourceId === event.sourceId &&
            subscription.threadKey === event.threadKey,
        ),
      }
    : null;
  const validTarget = tasks?.some((task) => task.id === target);
  const readError = tasksRead.error || subscriptionRead.error;
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
              <Button
                onClick={() => {
                  void tasksRead.refresh();
                  void subscriptionRead.refresh();
                }}
              >
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
