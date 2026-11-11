import { useState } from 'react';
import { Button, Checkbox } from '@cofy-x/camp-platinum';
import type { DesktopAPI } from '@cofy-x/camp-platinum';
import { useRead } from '../../shared/reads/useRead';
import type { Task } from '../collaboration/api';
import { automationApi, type Delivery, type Subscription } from './api';
import { useAutomationOperation } from './useAutomationOperation';
import './automation.css';

function FollowedConversation({
  observed,
  manage,
  active,
  onSaved,
}: {
  observed: Subscription;
  manage: boolean;
  active: boolean;
  onSaved: () => void;
}) {
  const [confirmed, setConfirmed] = useState<Subscription | null>(null);
  const subscription =
    confirmed && confirmed.revision > observed.revision ? confirmed : observed;
  const operation = useAutomationOperation(
    `subscription:${subscription.id}`,
    (receipt) => {
      if (receipt.subscription) setConfirmed(receipt.subscription);
      onSaved();
    },
  );
  return (
    <li>
      <span>
        {subscription.sourceId === 'room' ? 'Room' : 'GitHub'} ·{' '}
        {subscription.threadKey}
      </span>
      {manage && active ? (
        <Checkbox
          label="Follow conversation"
          checked={subscription.enabled}
          disabled={operation.blocked}
          onChange={(event) => {
            const enabled = event.target.checked;
            void operation.run((requestId) =>
              automationApi.saveSubscription(subscription.taskId, {
                requestId,
                revision: subscription.revision,
                sourceId: subscription.sourceId,
                threadKey: subscription.threadKey,
                enabled,
              }),
            );
          }}
        />
      ) : (
        <span>{active && subscription.enabled ? 'Following' : 'Inactive'}</span>
      )}
      {operation.feedback}
    </li>
  );
}

const deliveryLabels: Record<string, string> = {
  pending: 'Waiting to send',
  sending: 'Sending',
  sent: 'Sent',
  unknown: 'Delivery not confirmed',
  absent: 'Comment not found',
  not_sent: 'Comment not found',
  dropped: 'Not sent',
  failed: 'Could not send',
  blocked: 'Sending blocked',
  reconcile: 'Checking GitHub delivery',
  withdrawn: 'Withdrawn',
};

function OutboundComment({
  observed,
  manage,
  desktop,
  onSaved,
}: {
  observed: Delivery;
  manage: boolean;
  desktop: DesktopAPI;
  onSaved: () => void;
}) {
  const [confirmed, setConfirmed] = useState<Delivery | null>(null);
  const delivery =
    confirmed && confirmed.revision > observed.revision ? confirmed : observed;
  const withdrawn = delivery.withdrawn || delivery.status === 'withdrawn';
  const unconfirmedWithdrawal =
    withdrawn &&
    (delivery.status === 'unknown' ||
      delivery.attempts?.some((attempt) => attempt.outcome === 'unknown'));
  const checking =
    withdrawn && ['reconcile', 'sending'].includes(delivery.status);
  const operation = useAutomationOperation(
    `delivery:${delivery.id}`,
    (receipt) => {
      if (receipt.delivery) setConfirmed(receipt.delivery);
      onSaved();
    },
  );

  async function resend() {
    const result = await desktop.dialog({
      title: 'Resend GitHub Comment',
      message: `Send this comment again to ${delivery.repository} #${delivery.number}? If the earlier comment exists but could not be found, this can create a duplicate.`,
      buttons: [
        { label: 'Cancel', value: 'cancel' },
        { label: 'Resend Comment', value: 'resend' },
      ],
    });
    if (result?.button !== 'resend') return;
    void operation.run((requestId) =>
      automationApi.deliveryAction(delivery.id, 'resend', {
        requestId,
        revision: delivery.revision,
      }),
    );
  }

  return (
    <li>
      <div className="automation-heading">
        <strong>
          {checking
            ? deliveryLabels.reconcile
            : withdrawn
              ? deliveryLabels.withdrawn
              : (deliveryLabels[delivery.status] ?? delivery.status)}
        </strong>
        <span>
          {delivery.repository} #{delivery.number}
        </span>
      </div>
      {delivery.reason && <p>{delivery.reason}</p>}
      {unconfirmedWithdrawal && <p>An earlier send is still unconfirmed.</p>}
      <details>
        <summary>
          {delivery.questionId ? 'Task question' : 'Task result'}
        </summary>
        <pre className="automation-delivery-body">{delivery.body}</pre>
      </details>
      {delivery.url && (
        <a href={delivery.url} target="_blank" rel="noreferrer">
          Open GitHub Comment
        </a>
      )}
      {Boolean(
        delivery.attempts?.length ||
        delivery.receipts?.length ||
        delivery.receiptScanLimited,
      ) && (
        <details className="automation-delivery-diagnostics">
          <summary>Delivery details</summary>
          {Boolean(delivery.attempts?.length) && (
            <>
              <h4>Attempts</h4>
              <p>Attempts do not confirm delivery.</p>
              <ol>
                {delivery.attempts?.map((attempt, index) => (
                  <li key={`${attempt.revision ?? index}:${attempt.startedAt}`}>
                    {attempt.startedAt ? (
                      <time dateTime={attempt.startedAt}>
                        {new Date(attempt.startedAt).toLocaleString()}
                      </time>
                    ) : (
                      'Time not recorded'
                    )}
                    {' · '}GitHub account {attempt.actorId}
                    {attempt.outcome && (
                      <>
                        {' · '}
                        {deliveryLabels[attempt.outcome] ?? attempt.outcome}
                      </>
                    )}
                  </li>
                ))}
              </ol>
            </>
          )}
          {Boolean(delivery.receipts?.length) && (
            <>
              <h4>Confirmed comments</h4>
              <ul>
                {delivery.receipts?.map((receipt) => (
                  <li key={receipt.commentId}>
                    <a href={receipt.url} target="_blank" rel="noreferrer">
                      Comment {receipt.commentId}
                    </a>
                    {' · '}GitHub account {receipt.actorId}
                  </li>
                ))}
              </ul>
            </>
          )}
          {delivery.receiptScanLimited && (
            <p>Some matching comments may not be listed.</p>
          )}
        </details>
      )}
      {manage &&
        !checking &&
        (delivery.status === 'unknown' || unconfirmedWithdrawal) && (
          <div className="app-actions">
            <Button
              disabled={operation.blocked}
              onClick={() =>
                void operation.run((requestId) =>
                  automationApi.deliveryAction(delivery.id, 'reconcile', {
                    requestId,
                    revision: delivery.revision,
                  }),
                )
              }
            >
              Check GitHub Delivery
            </Button>
          </div>
        )}
      {manage && !withdrawn && delivery.status === 'unknown' && (
        <Button disabled={operation.blocked} onClick={() => void resend()}>
          Resend Comment…
        </Button>
      )}
      {manage &&
        !withdrawn &&
        ['failed', 'blocked'].includes(delivery.status) && (
          <Button
            disabled={operation.blocked}
            onClick={() =>
              void operation.run((requestId) =>
                automationApi.deliveryAction(delivery.id, 'resend', {
                  requestId,
                  revision: delivery.revision,
                }),
              )
            }
          >
            Retry Comment
          </Button>
        )}
      {operation.feedback}
    </li>
  );
}

export function TaskAutomation({
  task,
  manage,
  desktop,
}: {
  task: Task;
  manage: boolean;
  desktop: DesktopAPI;
}) {
  const [deliveryCursor, setDeliveryCursor] = useState('');
  const [previousDeliveries, setPreviousDeliveries] = useState<string[]>([]);
  const subscriptionRead = useRead<{ subscriptions: Subscription[] }>(
    `/tasks/${encodeURIComponent(task.id)}/subscriptions`,
  );
  const deliveryRead = useRead<{
    deliveries: Delivery[];
    nextCursor: string;
  }>(
    `/tasks/${encodeURIComponent(task.id)}/deliveries?${new URLSearchParams({ cursor: deliveryCursor, limit: '20' })}`,
  );
  const subscriptions = subscriptionRead.data?.subscriptions ?? null;
  const subscriptionError = subscriptionRead.error;
  const deliveryPage = deliveryRead.data
    ? { cursor: deliveryCursor, ...deliveryRead.data }
    : null;
  const deliveries = deliveryPage?.deliveries ?? null;
  const deliveryError = deliveryRead.error;
  const active = ![
    'completed',
    'cancelled',
    'interrupted',
    'cancelling',
  ].includes(task.status);
  if (
    !task.automationPolicyId &&
    !subscriptions?.length &&
    !deliveries?.length &&
    !previousDeliveries.length &&
    !subscriptionError &&
    !deliveryError
  )
    return null;
  return (
    <section
      className="automation-task-context"
      aria-label="Conversation tracking"
    >
      <h3>Conversation tracking</h3>
      {task.automationPolicyId && (
        <details>
          <summary>Automation authorization</summary>
          <dl className="automation-facts">
            <dt>Policy</dt>
            <dd>
              {task.automationPolicyId} · revision{' '}
              {task.automationPolicyRevision}
            </dd>
            <dt>Scope</dt>
            <dd>{task.automationScope || task.goal}</dd>
          </dl>
        </details>
      )}
      {subscriptionError && (
        <p role="alert" className="app-error">
          {subscriptionError}{' '}
          <Button onClick={() => void subscriptionRead.refresh()}>
            Retry Conversations
          </Button>
        </p>
      )}
      {!subscriptions && !subscriptionError && (
        <p role="status">Loading followed conversations…</p>
      )}
      {Boolean(subscriptions?.length) && (
        <>
          {active && (
            <p>Following adds context within the existing allowance.</p>
          )}
          <ul className="automation-subscriptions">
            {subscriptions?.map((subscription) => (
              <FollowedConversation
                key={subscription.id}
                observed={subscription}
                manage={manage}
                active={active}
                onSaved={() => void subscriptionRead.refresh()}
              />
            ))}
          </ul>
        </>
      )}
      {(Boolean(deliveries?.length) ||
        previousDeliveries.length > 0 ||
        deliveryError) && (
        <section aria-label="GitHub comments">
          <h3>GitHub comments</h3>
          {deliveryError && (
            <p className="app-error" role="alert">
              {deliveryError}{' '}
              <Button onClick={() => void deliveryRead.refresh()}>
                Retry Comments
              </Button>
            </p>
          )}
          {!deliveries && !deliveryError && (
            <p role="status">Loading comments…</p>
          )}
          <ul className="automation-deliveries">
            {deliveries?.map((delivery) => (
              <OutboundComment
                key={delivery.id}
                observed={delivery}
                manage={manage}
                desktop={desktop}
                onSaved={() => void deliveryRead.refresh()}
              />
            ))}
          </ul>
          <div className="app-actions">
            {previousDeliveries.length > 0 && (
              <Button
                onClick={() => {
                  setDeliveryCursor(previousDeliveries.at(-1)!);
                  setPreviousDeliveries(previousDeliveries.slice(0, -1));
                }}
              >
                Previous Comments
              </Button>
            )}
            {deliveryPage?.cursor === deliveryCursor &&
              deliveryPage.nextCursor && (
                <Button
                  onClick={() => {
                    setPreviousDeliveries([
                      ...previousDeliveries,
                      deliveryCursor,
                    ]);
                    setDeliveryCursor(deliveryPage.nextCursor);
                  }}
                >
                  Older Comments
                </Button>
              )}
          </div>
        </section>
      )}
    </section>
  );
}
