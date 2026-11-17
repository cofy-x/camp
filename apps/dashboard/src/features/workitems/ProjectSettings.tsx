import { useRef, useState } from 'react';
import { Button, Checkbox, Select } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { useAccount } from '../../team/AuthContext';
import { DraftRecovery } from '../../shared/drafts/DraftRecovery';
import { request } from '../../shared/api/http';
import { useRecordedOperation } from '../../shared/useRecordedOperation';
import type { Agent } from '../workspace/api';
import type { AutomationPolicy } from '../automation/api';
import { GitHubDeliveryChooser } from '../github-delivery/GitHubDeliveryChooser';
import { GitHubDeliverySummary } from '../github-delivery/GitHubDeliverySummary';
import { WorkGrant } from './WorkGrant';
import { WorkBudgetEditor } from './WorkBudgetEditor';
import { useWorkDefaults } from './useWorkDefaults';
import type { ProjectSettings as Settings } from './api';

type Receipt = { kind: string; settings?: Settings };
const service = {
  receipt: (id: string) => request<Receipt>(`/project-setting-requests/${id}`),
  cancelReceipt: (id: string) =>
    request<Receipt>(`/project-setting-requests/${id}/cancel`, {
      method: 'POST',
      body: '{}',
    }),
};

export function ProjectSettings({
  roomId,
  members,
  policies,
  onClose,
}: {
  roomId: string;
  members: Agent[];
  policies: AutomationPolicy[];
  onClose: () => void;
}) {
  const { user } = useAccount();
  const [deliveryOpen, setDeliveryOpen] = useState(false);
  const read = useWorkDefaults(roomId);
  const draft = usePrivateDraft<Settings | null>(
    'conversation',
    `project-settings:${roomId}`,
    null,
  );
  const defaults = read.defaults;
  const saved = defaults?.project;
  const value =
    draft.value ??
    saved ??
    (defaults
      ? {
          roomId,
          revision: 0,
          enabled: true,
          budget: defaults.budget,
          participantAgentIds: defaults.participantAgentIds,
          firstAgentId: defaults.firstAgentId,
          completionMode: 'automatic' as const,
        }
      : null);
  const operation = useRecordedOperation(
    `project:${roomId}`,
    service,
    (receipt) => {
      if (
        receipt.settings &&
        JSON.stringify(draft.getValue()) === submitted.current
      )
        draft.setValue(null);
      void read.retry();
    },
  );
  const submitted = useRef<string | undefined>(undefined);
  const edit = (change: Partial<Settings>) => {
    if (value) draft.setValue({ ...value, ...change });
  };
  const rules = policies.filter(
    (p) => p.ownerUserId === user.id && p.enabled && p.mode === 'auto',
  );
  const selectedRule = rules.find((rule) => rule.id === value?.ruleId);
  if (!value || !draft.loaded)
    return <p role="status">Loading project settings…</p>;
  return (
    <form
      className="work-item-form project-settings"
      aria-label="Project settings"
      onSubmit={(event) => {
        event.preventDefault();
        if (operation.blocked || draft.error) return;
        const frozen = { ...value };
        submitted.current = JSON.stringify(draft.getValue());
        void operation.run((requestId) =>
          request<Receipt>(`/rooms/${roomId}/project-settings`, {
            method: 'POST',
            body: JSON.stringify({ ...frozen, requestId }),
          }),
        );
      }}
    >
      <DraftRecovery
        draft={draft}
        preview={(value) => JSON.stringify(value, null, 2)}
      />
      <Checkbox
        label="Use project defaults"
        checked={value.enabled}
        onChange={(e) => edit({ enabled: e.target.checked })}
      />
      <fieldset
        disabled={operation.blocked || !value.enabled}
        className="work-item-fields"
      >
        <WorkGrant
          members={members}
          selected={value.participantAgentIds}
          firstAgentId={value.firstAgentId}
          defaults={defaults}
          maxRequests={value.budget.maxModelRequests}
          onChange={(ids) =>
            edit({
              participantAgentIds: ids,
              firstAgentId: ids.includes(value.firstAgentId)
                ? value.firstAgentId
                : (ids[0] ?? ''),
            })
          }
          onFirstChange={(id) => edit({ firstAgentId: id })}
        />
        <details>
          <summary>
            {value.completionMode === 'human'
              ? "You'll confirm completion"
              : 'Completion'}
          </summary>
          <Select
            label="Completion"
            value={value.completionMode}
            onChange={(e) =>
              edit({
                completionMode: e.target.value as Settings['completionMode'],
              })
            }
          >
            <option value="automatic">When the goal is met</option>
            <option value="human">I'll confirm</option>
          </Select>
        </details>
        {value.githubDelivery && (
          <GitHubDeliverySummary
            value={value.githubDelivery}
            onReady={ignoreReady}
          />
        )}
        <details
          onToggle={(event) => setDeliveryOpen(event.currentTarget.open)}
        >
          <summary>
            {value.githubDelivery
              ? 'Delivery settings'
              : 'Delivery · Room files'}
          </summary>
          {deliveryOpen && (
            <GitHubDeliveryChooser
              value={value.githubDelivery}
              onChange={(githubDelivery) =>
                edit({
                  githubDelivery,
                  ...(!githubDelivery
                    ? { ruleId: '', ruleRevision: undefined }
                    : {}),
                })
              }
            />
          )}
        </details>
        <details>
          <summary>Total allowance</summary>
          <WorkBudgetEditor
            label=""
            value={value.budget}
            onChange={(budget) => edit({ budget })}
          />
        </details>
        {value.githubDelivery && (
          <Select
            label="Allow delivery from this rule"
            value={value.ruleId ?? ''}
            onChange={(e) => {
              const rule = rules.find((p) => p.id === e.target.value);
              edit({ ruleId: rule?.id ?? '', ruleRevision: rule?.revision });
            }}
          >
            <option value="">Only work I start</option>
            {rules.map((p) => (
              <option key={p.id} value={p.id}>
                {p.sourceId === 'room' ? 'Room discussion' : 'GitHub events'}
              </option>
            ))}
            {value.ruleId && !rules.some((p) => p.id === value.ruleId) && (
              <option value={value.ruleId}>Saved rule unavailable</option>
            )}
          </Select>
        )}
        {value.ruleId && (
          <p>
            Future matching events may execute and deliver within this rule's
            existing limits.
          </p>
        )}
        {selectedRule && (
          <details>
            <summary>Rule scope</summary>
            <p>
              {selectedRule.participantAgentIds
                .map(
                  (id) =>
                    members.find((member) => member.id === id)?.name ??
                    'Unavailable Agent',
                )
                .join(', ')}{' '}
              · {selectedRule.maxTurns} turns · {selectedRule.maxModelRequests}{' '}
              requests per task · {selectedRule.dailyTasks} tasks per day
            </p>
            <pre>{selectedRule.rules}</pre>
          </details>
        )}
      </fieldset>
      {draft.value && saved && draft.value.revision !== saved.revision && (
        <p className="app-error" role="alert">
          Project settings changed. Your draft is preserved.{' '}
          <Button onClick={() => draft.setValue(saved)}>
            Use current settings
          </Button>
        </p>
      )}
      {read.error && (
        <p className="app-error" role="alert">
          {read.error}
          <Button onClick={() => void read.retry()}>Retry</Button>
        </p>
      )}
      {operation.feedback}
      <div className="app-actions">
        <Button
          primary
          type="submit"
          disabled={operation.blocked || Boolean(draft.error)}
        >
          Save defaults
        </Button>
        <Button onClick={onClose}>Done</Button>
      </div>
    </form>
  );
}

const ignoreReady = () => {};
