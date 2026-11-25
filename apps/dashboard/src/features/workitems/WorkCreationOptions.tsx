import { useId, useState } from 'react';
import { Select } from '@cofy-x/camp-platinum';
import type { AutomationPolicy } from '../automation/api';
import { GitHubDeliveryChooser } from '../github-delivery/GitHubDeliveryChooser';
import { WorkBudgetEditor } from './WorkBudgetEditor';
import { EvidenceChooser } from './WorkItemEvidence';
import type { WorkItemDefaults, WorkItemInput } from './api';
import { initialWorkBudget, localDateTime } from './workSettings';

export type WorkCreationOptionValues = Pick<
  WorkItemInput,
  | 'budget'
  | 'completionCriteria'
  | 'completionMode'
  | 'deadline'
  | 'dueAt'
  | 'evidenceIds'
  | 'expiresAt'
  | 'githubDelivery'
  | 'policyId'
> & { expiryEdited?: boolean };

export function WorkCreationOptions({
  values,
  roomId,
  defaults,
  delegated,
  custom,
  disabled,
  useGitHub,
  onDeliveryDestinationChange,
  onChange,
}: {
  values: WorkCreationOptionValues;
  roomId: string;
  defaults: WorkItemDefaults | null;
  delegated: AutomationPolicy[];
  custom: boolean;
  disabled: boolean;
  useGitHub: boolean;
  onDeliveryDestinationChange: (github: boolean) => void;
  onChange: (change: Partial<WorkCreationOptionValues>) => void;
}) {
  const group = useId();
  const [detailsOpen, setDetailsOpen] = useState(false);
  const legacyCriteria =
    !values.completionMode && Boolean(values.completionCriteria?.trim());
  const completion =
    values.completionMode ?? (legacyCriteria ? 'criteria' : 'human');
  return (
    <fieldset
      className="work-creation-options"
      aria-label="Work options"
      disabled={disabled}
    >
      {custom && (
        <>
          <details name={group} className="work-creation-option-section">
            <summary>
              <span>Delivery</span>
              {!useGitHub && (
                <span className="work-creation-option-current">
                  {completion === 'automatic' && 'When the goal is met · '}
                  Room files
                </span>
              )}
            </summary>
            <div className="work-creation-option-fields">
              <div className="work-creation-option-pair">
                <Select
                  label="Completion"
                  value={completion}
                  onChange={(event) => {
                    const completionMode = event.target.value;
                    if (
                      completionMode === 'automatic' ||
                      completionMode === 'human'
                    )
                      onChange({ completionMode });
                  }}
                >
                  <option value="automatic">When the goal is met</option>
                  <option value="human">I'll confirm</option>
                  {legacyCriteria && (
                    <option value="criteria">Saved completion criteria</option>
                  )}
                </Select>
                <Select
                  label="Destination"
                  value={useGitHub ? 'github' : 'room'}
                  onChange={(event) =>
                    onDeliveryDestinationChange(event.target.value === 'github')
                  }
                >
                  <option value="room">Room files</option>
                  <option value="github">GitHub</option>
                </Select>
              </div>
              {useGitHub && (
                <GitHubDeliveryChooser
                  value={values.githubDelivery}
                  onChange={(githubDelivery) => onChange({ githubDelivery })}
                />
              )}
            </div>
          </details>
          <details name={group} className="work-creation-option-section">
            <summary>Allowance</summary>
            <div className="work-creation-option-fields">
              <WorkBudgetEditor
                value={values.budget ?? defaults?.budget ?? initialWorkBudget}
                onChange={(budget) => onChange({ budget })}
              />
              <label className="work-creation-option-date">
                Authorization expires
                <input
                  className="p9-field"
                  type="datetime-local"
                  value={
                    values.expiryEdited
                      ? (values.expiresAt ?? '')
                      : localDateTime(
                          values.expiresAt ??
                            values.deadline ??
                            defaults?.expiresAt ??
                            '',
                        )
                  }
                  onChange={(event) =>
                    onChange({
                      expiresAt: event.target.value,
                      expiryEdited: true,
                    })
                  }
                />
              </label>
              <p>
                Limits apply across all work sessions. Expiry stops new
                automatic work.
              </p>
            </div>
          </details>
        </>
      )}
      <details
        name={group}
        className="work-creation-option-section"
        onToggle={(event) => {
          if (event.currentTarget === event.target)
            setDetailsOpen(event.currentTarget.open);
        }}
      >
        <summary>Details</summary>
        <div className="work-creation-option-fields">
          <label className="work-creation-option-date">
            Due date
            <input
              className="p9-field"
              type="date"
              value={values.dueAt ?? ''}
              onChange={(event) => onChange({ dueAt: event.target.value })}
            />
          </label>
          <label>
            Complete when
            <textarea
              className="p9-field"
              rows={2}
              placeholder={
                values.completionMode === 'automatic'
                  ? 'Use the goal above'
                  : undefined
              }
              value={values.completionCriteria ?? ''}
              onChange={(event) =>
                onChange({ completionCriteria: event.target.value })
              }
            />
          </label>
          {detailsOpen && (
            <EvidenceChooser
              roomId={roomId}
              selected={values.evidenceIds ?? []}
              onChange={(evidenceIds) => onChange({ evidenceIds })}
            />
          )}
          {(delegated.length > 0 || values.policyId) && (
            <Select
              label="Future follow-up"
              value={values.policyId ?? ''}
              onChange={(event) => onChange({ policyId: event.target.value })}
            >
              <option value="">I'll decide</option>
              {delegated.map((policy) => (
                <option value={policy.id} key={policy.id}>
                  {policy.sourceId === 'room' ? 'Room rule' : policy.sourceId}
                </option>
              ))}
              {values.policyId &&
                !delegated.some((policy) => policy.id === values.policyId) && (
                  <option value={values.policyId}>
                    Saved rule unavailable
                  </option>
                )}
            </Select>
          )}
        </div>
      </details>
    </fieldset>
  );
}
