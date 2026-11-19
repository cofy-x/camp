import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import {
  githubApi,
  type GitHubSource,
  type GitHubSourceVerification,
} from './api';

const ruleModes = { off: 'Off', observe: 'Observe', auto: 'Auto' };
const sampleOutcomes = {
  recorded_only: 'Recorded as evidence',
  off: 'Rule off',
  before_rule: 'Before the saved rule',
  sender_not_allowed: 'Sender outside the allowlist',
  eligible: 'Eligible for assessment',
};

type CheckState =
  | { status: 'checking' }
  | { status: 'ready'; result: GitHubSourceVerification }
  | { status: 'error'; message: string };

function deliveryText(
  delivery: NonNullable<GitHubSourceVerification['delivery']>,
) {
  switch (delivery.state) {
    case 'private':
      return 'Only the rule owner can check their delivery settings.';
    case 'room_files':
      return 'Room files';
    case 'blocked':
      return delivery.issue?.message;
    case 'stale':
      return 'Link the current rule again in Project settings to deliver to GitHub.';
    case 'saved':
      return [
        `${delivery.repository} → ${delivery.branch}`,
        delivery.ciWorkflow ? `Saved CI · ${delivery.ciWorkflow}` : '',
      ]
        .filter(Boolean)
        .join(' · ');
  }
}

export function GitHubVerification({
  source,
  ruleRevision,
  eventLabel,
  refreshPolicies,
  children,
}: {
  source?: GitHubSource;
  ruleRevision: number;
  eventLabel: (kind: string) => string;
  refreshPolicies: () => void;
  children: ReactNode;
}) {
  const scope = source ? `${source.id}:${source.revision}:${ruleRevision}` : '';
  const [snapshot, setSnapshot] = useState<{
    scope: string;
    state?: CheckState;
  }>({ scope });
  if (snapshot.scope !== scope) setSnapshot({ scope });
  const state = snapshot?.scope === scope ? snapshot.state : undefined;
  const check = state?.status === 'ready' ? state.result : undefined;
  const active = useRef<{ scope: string; controller: AbortController }>(null);
  useEffect(
    () => () => {
      if (active.current?.scope !== scope) return;
      active.current.controller.abort();
      active.current = null;
    },
    [scope],
  );

  async function verify() {
    if (!source || active.current?.scope === scope) return;
    active.current?.controller.abort();
    const read = new AbortController();
    active.current = { scope, controller: read };
    setSnapshot({ scope, state: { status: 'checking' } });
    try {
      const result = await githubApi.verify(
        source.roomId,
        source.id,
        read.signal,
      );
      if (read.signal.aborted) return;
      if (
        result.sourceRevision !== source.revision ||
        result.ruleRevision !== ruleRevision
      ) {
        setSnapshot({
          scope,
          state: {
            status: 'error',
            message:
              'The saved setup changed. Refresh policies and check again.',
          },
        });
        return;
      }
      setSnapshot({ scope, state: { status: 'ready', result } });
    } catch (err) {
      if (!read.signal.aborted)
        setSnapshot({
          scope,
          state: {
            status: 'error',
            message:
              err instanceof Error
                ? err.message
                : 'Cannot check the saved setup.',
          },
        });
    } finally {
      if (active.current?.controller === read) active.current = null;
    }
  }

  return (
    <section
      className="automation-verification"
      aria-label={source ? 'Saved GitHub setup' : undefined}
    >
      <div className="automation-view-controls">
        {children}
        <div className="app-actions">
          {source && (
            <Button
              aria-disabled={state?.status === 'checking' || undefined}
              aria-busy={state?.status === 'checking' || undefined}
              onClick={() => void verify()}
            >
              {state?.status === 'checking'
                ? 'Checking…'
                : 'Verify saved setup'}
            </Button>
          )}
          <Button onClick={refreshPolicies}>Refresh Policies</Button>
        </div>
      </div>
      {state?.status === 'error' && (
        <p className="app-error" role="alert">
          {state.message}
        </p>
      )}
      {check && (
        <dl className="automation-facts" role="status">
          <dt>Webhook</dt>
          <dd>
            {!check.sourceEnabled
              ? 'Disabled'
              : check.lastReceivedAt
                ? `Signed delivery received ${new Date(check.lastReceivedAt).toLocaleString(undefined, { dateStyle: 'short', timeStyle: 'short' })}`
                : 'Awaiting first delivery. Send a fresh ping from GitHub.'}
          </dd>
          <dt>Saved rule</dt>
          <dd>{ruleModes[check.mode]}</dd>
          {check.sourceEnabled && check.mode !== 'off' && (
            <>
              <dt>Runner access</dt>
              <dd>{check.runnerIssue?.message ?? 'Allowed for this rule'}</dd>
            </>
          )}
          {check.sourceEnabled && check.mode !== 'off' && check.sample && (
            <>
              <dt>Latest sample</dt>
              <dd>
                {sampleOutcomes[check.sample.outcome]}
                {' · '}
                {check.sample.outcome === 'sender_not_allowed'
                  ? `Actor ${check.sample.actorId}`
                  : eventLabel(check.sample.kind)}
              </dd>
            </>
          )}
          {check.delivery && (
            <>
              <dt>Delivery</dt>
              <dd>{deliveryText(check.delivery)}</dd>
            </>
          )}
        </dl>
      )}
    </section>
  );
}
