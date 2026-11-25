import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { Button, Checkbox, Select } from '@cofy-x/camp-platinum';
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
import './project-settings.css';

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
}: {
  roomId: string;
  members: Agent[];
  policies: AutomationPolicy[];
}) {
  const { user } = useAccount();
  const sectionGroup = useId();
  const scopeId = useId();
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
      <div className="project-settings-placeholder">
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
  const unsaved =
    draft.value !== null && JSON.stringify(value) !== JSON.stringify(saved);
  const canSave = !saveBlocked && (!saved || unsaved);
  const showScope = canSave && value.enabled;
  const saveStatus = showScope
    ? 'Applies to new Work in this Room.'
    : !operation.blocked &&
        !unsaved &&
        confirmed &&
        confirmed.revision === saved?.revision
      ? 'Defaults saved.'
      : '';
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
        const field = event.target;
        if (revealingInvalid.current || !(field instanceof HTMLElement)) return;
        const section = field.closest<HTMLDetailsElement>(
          '.project-setting-section',
        );
        if (!section) return;
        revealingInvalid.current = true;
        section.open = true;
        // WebKit can skip focusing a field that was hidden during validation.
        requestAnimationFrame(() => {
          revealingInvalid.current = false;
          if (field.isConnected && section.open) field.focus();
        });
      }}
      onSubmit={(event) => {
        event.preventDefault();
        if (!canSave) return;
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
      <div className="project-settings-content">
        <div className="project-settings-sheet">
          {draftRecovery}
          {readError}
          <fieldset
            disabled={operation.blocked}
            className="project-settings-fields"
          >
            <Checkbox
              label="Use my defaults"
              checked={value.enabled}
              disabled={Boolean(draft.error)}
              onChange={(event) => edit({ enabled: event.target.checked })}
            />
            {value.enabled ? (
              <div className="project-settings-sections">
                <DefaultSection
                  group={sectionGroup}
                  title="Execution"
                  keepValue
                  summary={
                    <>
                      <span>
                        {workAgentSummary(agents)}
                        {unavailable > 0 && ` · ${unavailable} unavailable`}
                      </span>
                      <span className="project-setting-context">
                        {defaults &&
                          `Up to ${defaults.execution.maxTurns} turns per collaboration · `}
                        Team-visible draft
                      </span>
                    </>
                  }
                >
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
                </DefaultSection>
                <DefaultSection
                  group={sectionGroup}
                  title="Delivery"
                  onOpenChange={setCompletionOpen}
                  summary={
                    <>
                      <span>
                        {completionLabels[value.completionMode]} ·{' '}
                        {github
                          ? deliverySummary(github, repository)
                          : useGitHub
                            ? 'Choose a GitHub repository'
                            : 'Room files'}
                      </span>
                      {selectedRule && currentRule && (
                        <span className="project-setting-context">
                          Automatic delivery from{' '}
                          {ruleSourceLabel(selectedRule, sources.data?.sources)}
                        </span>
                      )}
                    </>
                  }
                >
                  <div className="project-settings-completion-fields">
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
                      <option value="automatic">
                        {completionLabels.automatic}
                      </option>
                      <option value="human">{completionLabels.human}</option>
                    </Select>
                    <Select
                      label="Destination"
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
                  </div>
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
                    <details className="p9-disclosure">
                      <summary>Rule scope</summary>
                      <p>
                        {selectedRule.participantAgentIds
                          .map(
                            (id) =>
                              members.find((member) => member.id === id)
                                ?.name ?? 'Unavailable Agent',
                          )
                          .join(', ')}{' '}
                        · {selectedRule.maxTurns} turns ·{' '}
                        {selectedRule.maxModelRequests} requests per task ·{' '}
                        {selectedRule.dailyTasks} tasks per day
                      </p>
                      <pre>{selectedRule.rules}</pre>
                    </details>
                  )}
                </DefaultSection>
                <div className="project-settings-delivery-issues">
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
                      Review the repository connection under Delivery.
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
                </div>
                <DefaultSection
                  group={sectionGroup}
                  title="Allowance"
                  summary={`${value.budget.maxModelRequests} model requests total per Work`}
                >
                  <WorkBudgetEditor
                    label="Total per Work"
                    value={value.budget}
                    onChange={(budget) => edit({ budget })}
                  />
                </DefaultSection>
              </div>
            ) : (
              <p className="project-settings-muted" role="status">
                {saved?.enabled === false
                  ? 'New Work uses Room defaults.'
                  : 'Save to use Room defaults for new Work.'}
              </p>
            )}
          </fieldset>
          {draft.value && saved && draft.value.revision !== saved.revision && (
            <p className="app-error" role="alert">
              Work defaults changed. Your draft is preserved.{' '}
              <Button onClick={() => draft.setValue(saved)}>
                Use current settings
              </Button>
            </p>
          )}
        </div>
      </div>
      <footer className="project-settings-footer">
        <div className="project-settings-sheet">
          {operation.feedback}
          <div className="project-settings-save-row">
            <p id={scopeId} className="project-settings-muted" role="status">
              {saveStatus}
            </p>
            <Button
              primary={canSave}
              type="submit"
              disabled={!canSave}
              aria-describedby={showScope ? scopeId : undefined}
            >
              Save defaults
            </Button>
          </div>
        </div>
      </footer>
    </form>
  );
}

function DefaultSection({
  group,
  title,
  summary,
  children,
  keepValue = false,
  onOpenChange,
}: {
  group: string;
  title: string;
  summary: ReactNode;
  children: ReactNode;
  keepValue?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  return (
    <details
      name={group}
      className="project-setting-section"
      data-keep-value={keepValue}
      onToggle={(event) => {
        if (event.target === event.currentTarget)
          onOpenChange?.(event.currentTarget.open);
      }}
    >
      <summary>
        <span className="project-setting-title">{title}</span>
        <span className="project-setting-value">{summary}</span>
      </summary>
      <div className="project-setting-controls">{children}</div>
    </details>
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
