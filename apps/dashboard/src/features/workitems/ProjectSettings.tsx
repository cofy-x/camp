import { useEffect, useId, useRef, useState } from 'react';
import { Button, Select, type DesktopAPI } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { useAccount } from '../../team/AuthContext';
import { DraftRecovery } from '../../shared/drafts/DraftRecovery';
import { request } from '../../shared/api/http';
import { useRead } from '../../shared/reads/useRead';
import { useRecordedOperation } from '../../shared/useRecordedOperation';
import type { Agent } from '../workspace/api';
import type { AutomationPolicy } from '../automation/api';
import type { GitHubSource } from '../github/api';
import type {
  GitHubDeliverySpec,
  GitHubRepository,
} from '../github-delivery/api';
import { GitHubDeliveryChooser } from '../github-delivery/GitHubDeliveryChooser';
import { workAgentSummary, WorkParticipants } from './WorkGrant';
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
  desktop,
}: {
  roomId: string;
  members: Agent[];
  policies: AutomationPolicy[];
  desktop: DesktopAPI;
}) {
  const { user } = useAccount();
  const sectionGroup = useId();
  const [completionOpen, setCompletionOpen] = useState(false);
  const [choosingGitHub, setChoosingGitHub] = useState(false);
  const [confirmed, setConfirmed] = useState<Settings>();
  const read = useWorkDefaults(roomId);
  const draft = usePrivateDraft<Settings | null>(
    'conversation',
    `project-settings:${roomId}`,
    null,
  );
  const defaults = read.defaults;
  const observed = defaults?.project;
  const saved =
    defaults && confirmed && confirmed.revision > (observed?.revision ?? 0)
      ? confirmed
      : observed;
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
  useEffect(() => {
    setCompletionOpen(false);
  }, [value?.enabled]);
  const operation = useRecordedOperation(
    `project:${roomId}`,
    service,
    (receipt) => {
      const accepted = receipt.settings;
      if (accepted?.roomId === roomId) {
        setConfirmed((current) =>
          !current || accepted.revision > current.revision ? accepted : current,
        );
        if (JSON.stringify(draft.getValue()) === submitted.current)
          draft.setValue(null);
      }
      void read.retry();
    },
  );
  const submitted = useRef<string | undefined>(undefined);
  const revealingInvalid = useRef(false);
  const edit = (change: Partial<Settings>) => {
    if (value) draft.setValue({ ...value, ...change });
  };
  const rules = policies.filter(
    (p) => p.ownerUserId === user.id && p.enabled && p.mode === 'auto',
  );
  const github = value?.enabled ? value.githubDelivery : undefined;
  const repositories = useRead<{ repositories: GitHubRepository[] }>(
    github ? '/github-repositories' : null,
  );
  const sources = useRead<{ sources: GitHubSource[] }>(
    github && rules.some((rule) => rule.sourceId !== 'room')
      ? `/rooms/${encodeURIComponent(roomId)}/github-sources`
      : null,
  );
  const selectedRule = rules.find((rule) => rule.id === value?.ruleId);
  const draftRecovery = (
    <DraftRecovery
      draft={draft}
      preview={(value) => JSON.stringify(value, null, 2)}
    />
  );
  const readError = read.error && (
    <p className="app-error" role="alert">
      {read.error}{' '}
      <Button onClick={() => void read.retry()}>Retry defaults</Button>
    </p>
  );
  if (!value || !draft.loaded)
    return (
      <div className="work-item-form project-settings">
        {draftRecovery}
        {readError}
        {operation.feedback}
        {!read.error && !draft.error && (
          <p role="status">Loading your Work defaults…</p>
        )}
      </div>
    );

  const useGitHub = Boolean(github || choosingGitHub);
  const repository = repositories.data?.repositories.find(
    (item) => item.id === github?.connectionId,
  );
  const repositoryReady =
    repository?.enabled &&
    repository.revision === github?.connectionRevision &&
    (github?.mode === 'pr' || Boolean(repository.ciWorkflowId));
  const currentRule = Boolean(
    selectedRule && selectedRule.revision === value.ruleRevision,
  );
  const ruleChanged = Boolean(value.ruleId && !currentRule);
  const deliveryIssue =
    useGitHub && value.completionMode === 'human'
      ? 'GitHub delivery requires automatic completion.'
      : useGitHub && !github
        ? 'Choose a repository or switch delivery to Room files.'
        : '';
  const savedConnectionIssue = github && repositories.data && !repositoryReady;
  const saveBlocked =
    operation.blocked ||
    Boolean(draft.error) ||
    !defaults ||
    (value.enabled &&
      (Boolean(deliveryIssue) ||
        Boolean(github && !repositoryReady) ||
        ruleChanged));
  const agents = value.participantAgentIds.flatMap((id) => {
    const agent = members.find((item) => item.id === id && item.enabled);
    return agent ? [agent] : [];
  });
  const unavailable = value.participantAgentIds.length - agents.length;
  return (
    <form
      className="work-item-form project-settings"
      aria-label="My Work defaults"
      onInvalid={(event) => {
        if (revealingInvalid.current || !(event.target instanceof HTMLElement))
          return;
        const section = event.target.closest<HTMLDetailsElement>(
          '.project-setting-section',
        );
        if (!section) return;
        revealingInvalid.current = true;
        section.open = true;
        setTimeout(() => {
          revealingInvalid.current = false;
        }, 0);
      }}
      onSubmit={(event) => {
        event.preventDefault();
        if (saveBlocked) return;
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
      <div className="project-settings-header">
        <p>Used for new Work in this Room.</p>
        <Button
          aria-label="More default actions"
          aria-haspopup="menu"
          disabled={operation.blocked || Boolean(draft.error)}
          onClick={(event) => {
            const rect = event.currentTarget.getBoundingClientRect();
            desktop.contextMenu(
              [
                {
                  label: value.enabled ? 'Disable defaults' : 'Enable defaults',
                  action: () => edit({ enabled: !value.enabled }),
                },
              ],
              { x: rect.left, y: rect.bottom },
            );
          }}
        >
          More
        </Button>
      </div>
      {draftRecovery}
      {value.enabled ? (
        <fieldset disabled={operation.blocked} className="work-item-fields">
          <details name={sectionGroup} className="project-setting-section">
            <summary>
              <span>Execution</span>{' '}
              <span className="project-setting-value">
                {workAgentSummary(agents)}
                {unavailable > 0 && ` · ${unavailable} unavailable`}
                {defaults &&
                  ` · Up to ${defaults.execution.maxTurns} turns per collaboration`}
                {' · Team-visible draft'}
              </span>
            </summary>
            <div className="project-setting-controls">
              <WorkParticipants
                members={members}
                selected={value.participantAgentIds}
                firstAgentId={value.firstAgentId}
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
              {value.participantAgentIds.length > 8 && (
                <p className="app-error" role="alert">
                  Choose at most eight Agents.
                </p>
              )}
            </div>
          </details>
          <details
            name={sectionGroup}
            className="project-setting-section"
            onToggle={(event) => {
              if (event.currentTarget === event.target)
                setCompletionOpen(event.currentTarget.open);
            }}
          >
            <summary>
              <span>Completion &amp; delivery</span>{' '}
              <span className="project-setting-value">
                {completionLabels[value.completionMode]} ·{' '}
                {github
                  ? deliverySummary(github, repository)
                  : useGitHub
                    ? 'Choose a GitHub repository'
                    : 'Room files'}
                {selectedRule &&
                  currentRule &&
                  ` · Automatic delivery from ${ruleSourceLabel(selectedRule, sources.data?.sources)}`}
              </span>
            </summary>
            <div className="project-setting-controls">
              <Select
                label="Completion"
                value={value.completionMode}
                onChange={(event) =>
                  edit({
                    completionMode: event.target
                      .value as Settings['completionMode'],
                  })
                }
              >
                <option value="automatic">{completionLabels.automatic}</option>
                <option value="human">{completionLabels.human}</option>
              </Select>
              <Select
                label="Delivery"
                value={useGitHub ? 'github' : 'room'}
                onChange={(event) => {
                  const nextGitHub = event.target.value === 'github';
                  setChoosingGitHub(nextGitHub);
                  if (!nextGitHub && (github || value.ruleId))
                    edit({
                      githubDelivery: undefined,
                      ruleId: '',
                      ruleRevision: undefined,
                    });
                }}
              >
                <option value="room">Room files</option>
                <option value="github">GitHub</option>
              </Select>
              {useGitHub && (completionOpen || github) && (
                <GitHubDeliveryChooser
                  value={github}
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
              {github && (rules.length > 0 || value.ruleId) && (
                <Select
                  label="Allow delivery from this rule"
                  value={ruleChanged ? 'saved-rule' : (value.ruleId ?? '')}
                  onChange={(event) => {
                    const rule = rules.find(
                      (item) => item.id === event.target.value,
                    );
                    edit({
                      ruleId: rule?.id ?? '',
                      ruleRevision: rule?.revision,
                    });
                  }}
                >
                  <option value="">Only work I start</option>
                  {rules.map((rule) => (
                    <option
                      key={rule.id}
                      value={rule.id}
                      disabled={rule.sourceId !== 'room' && !sources.data}
                    >
                      {ruleSourceLabel(rule, sources.data?.sources)}
                    </option>
                  ))}
                  {ruleChanged && (
                    <option value="saved-rule" disabled>
                      {selectedRule
                        ? 'Saved rule changed'
                        : 'Saved rule unavailable'}
                    </option>
                  )}
                </Select>
              )}
              {value.ruleId && (
                <p>
                  Future matching events may execute and deliver within this
                  rule’s existing limits.
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
                    · {selectedRule.maxTurns} turns ·{' '}
                    {selectedRule.maxModelRequests} requests per task ·{' '}
                    {selectedRule.dailyTasks} tasks per day
                  </p>
                  <pre>{selectedRule.rules}</pre>
                </details>
              )}
            </div>
          </details>
          {deliveryIssue && (
            <p className="app-error" role="alert">
              {deliveryIssue}
            </p>
          )}
          {repositories.error ? (
            <p className="app-error" role="alert">
              {repositories.error}{' '}
              <Button onClick={() => void repositories.refresh()}>
                Retry repositories
              </Button>
            </p>
          ) : savedConnectionIssue ? (
            <p className="app-error" role="alert">
              Review the current repository connection under Completion &amp;
              delivery.
            </p>
          ) : github && !repositories.data ? (
            <p role="status">Checking delivery settings…</p>
          ) : null}
          {sources.error && (
            <p className="app-error" role="alert">
              {sources.error}{' '}
              <Button onClick={() => void sources.refresh()}>
                Retry source names
              </Button>
            </p>
          )}
          {ruleChanged && (
            <p className="app-error" role="alert">
              Review the saved rule or choose Only work I start.
            </p>
          )}
          <details name={sectionGroup} className="project-setting-section">
            <summary>
              <span>Allowance</span>{' '}
              <span className="project-setting-value">
                {value.budget.maxModelRequests} model requests total per Work
              </span>
            </summary>
            <div className="project-setting-controls">
              <WorkBudgetEditor
                label=""
                value={value.budget}
                onChange={(budget) => edit({ budget })}
              />
            </div>
          </details>
        </fieldset>
      ) : (
        <p role="status">
          {saved?.enabled === false
            ? 'Personal defaults are off. New Work uses Room defaults.'
            : 'Save to use Room defaults for new Work.'}
        </p>
      )}
      {draft.value && saved && draft.value.revision !== saved.revision && (
        <p className="app-error" role="alert">
          Work defaults changed. Your draft is preserved.{' '}
          <Button onClick={() => draft.setValue(saved)}>
            Use current settings
          </Button>
        </p>
      )}
      {readError}
      {operation.feedback}
      <div className="app-actions">
        <Button primary type="submit" disabled={saveBlocked}>
          Save defaults
        </Button>
      </div>
    </form>
  );
}

const completionLabels = {
  automatic: 'When the goal is met',
  human: "I'll confirm",
};

function deliverySummary(
  value: GitHubDeliverySpec,
  repository?: GitHubRepository,
) {
  const destination = repository
    ? `${repository.private ? 'Private' : 'Public'} ${repository.repository}`
    : 'GitHub repository';
  const action =
    value.mode === 'pr'
      ? 'Open pull request'
      : value.mode === 'merge'
        ? 'Merge after CI'
        : `Merge and release ${value.versionLine ? `${value.versionLine}.x from package.json` : (value.tag ?? '')} after CI`;
  return `${destination} · ${value.baseBranch} · ${action}${value.maxRepairs ? ` · Up to ${value.maxRepairs} automatic repair${value.maxRepairs === 1 ? '' : 's'}` : ''}`;
}

function ruleSourceLabel(rule: AutomationPolicy, sources: GitHubSource[] = []) {
  if (rule.sourceId === 'room') return 'Room discussion';
  const source = sources.find((item) => item.id === rule.sourceId);
  if (!source) return 'External event source';
  return source.enabled ? source.repository : `${source.repository} (disabled)`;
}
