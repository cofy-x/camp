import { useId, useLayoutEffect, useRef, useState } from 'react';
import { Button, Select } from '@cofy-x/camp-platinum';
import type { Environment, Resource } from '../workspace/api';
import { workspaceApi } from '../workspace/api';
import { APIError } from '../../shared/api/http';
import {
  confirmEnvironmentSave,
  environmentChanged,
  initialEnvironment,
  observeEnvironment,
  reloadEnvironment,
  sameEnvironment,
} from './environmentState';
import { EnvironmentChecks } from './EnvironmentChecks';
import {
  configuredChecks,
  editableCheck,
  type EditableCheck,
} from './environmentCheckCommands';
import './ResourceEnvironment.css';

type EnvironmentKind = NonNullable<Environment['kind']>;

type EnvironmentDraft = {
  kind: EnvironmentKind | 'none';
  manager: string;
  checks: Record<EnvironmentKind, EditableCheck[]>;
};

function editEnvironment(environment: Environment | null): EnvironmentDraft {
  const kind = environment?.kind ?? 'none';
  const checks = (environment?.checks ?? []).map(editableCheck);
  return {
    kind,
    manager: environment?.packageManager ?? 'node',
    checks: {
      command: kind === 'command' ? checks : [],
      node: kind === 'node' ? checks : [],
    },
  };
}

export function EnvironmentEditor({
  resource,
  onChange,
  onClose,
}: {
  resource: Resource;
  onChange: () => void;
  onClose: () => void;
}) {
  const scopeId = useId();
  const back = useRef<HTMLButtonElement>(null);
  useLayoutEffect(() => back.current?.focus({ preventScroll: true }), []);
  const [draft, setDraft] = useState(() =>
    editEnvironment(resource.environment),
  );
  const { kind, manager } = draft;
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [needsRefresh, setNeedsRefresh] = useState(false);
  const [unconfirmed, setUnconfirmed] = useState<
    Environment | null | undefined
  >();
  const editVersion = useRef(0);
  const [snapshot, setSnapshot] = useState(() =>
    initialEnvironment(resource.environment, resource.revision),
  );
  const observed = observeEnvironment(
    snapshot,
    resource.environment,
    resource.revision,
  );
  if (observed !== snapshot) setSnapshot(observed);
  const updatedElsewhere = environmentChanged(observed);
  const currentEnvironment = observed.current;
  const draftEnvironment: Environment | null =
    kind === 'none'
      ? null
      : {
          kind,
          checks: configuredChecks(draft.checks[kind]),
          ...(kind === 'node'
            ? { nodeVersion: '24.16.0', packageManager: manager }
            : {}),
        };
  const unsaved = !sameEnvironment(draftEnvironment, currentEnvironment);
  const saveUnconfirmed = needsRefresh || unconfirmed !== undefined;
  const canSave = unsaved && !pending && !updatedElsewhere && !saveUnconfirmed;
  const showScope = unsaved && !updatedElsewhere && !saveUnconfirmed;
  const saveStatus = showScope
    ? 'Applies to new work only.'
    : observed.saved && !unsaved && !pending && !saveUnconfirmed
      ? 'Environment saved.'
      : '';

  function changeDraft(change: Partial<EnvironmentDraft>) {
    setDraft((current) => ({ ...current, ...change }));
    editVersion.current++;
    setSnapshot((current) => ({ ...current, saved: false }));
    setError('');
    setNotice('');
  }

  function reload() {
    setDraft(editEnvironment(currentEnvironment));
    setSnapshot((current) => reloadEnvironment(current));
    setError('');
    setNotice('');
  }

  function keepEdits() {
    setSnapshot((current) => reloadEnvironment(current));
    setError('');
    setNotice('');
  }

  async function readSaved(
    desired?: Environment | null,
    editing = editVersion.current,
  ) {
    const { resource: latest } = await workspaceApi.resource(resource.id);
    const matched =
      desired !== undefined && sameEnvironment(latest.environment, desired);
    setSnapshot((current) => {
      if (matched) {
        const confirmed = confirmEnvironmentSave(
          current,
          latest.environment,
          latest.revision,
        );
        return {
          ...confirmed,
          saved: confirmed.saved && editVersion.current === editing,
        };
      }
      return observeEnvironment(current, latest.environment, latest.revision);
    });
    setUnconfirmed(undefined);
    setNeedsRefresh(false);
    setError('');
    onChange();
    return matched;
  }

  async function checkSaved() {
    setPending(true);
    try {
      await readSaved(unconfirmed);
    } catch {
      setError(
        'Could not read the saved settings. Your edits are kept. Check the connection and try again.',
      );
    } finally {
      setPending(false);
    }
  }

  async function save() {
    if (!canSave) return;
    setError('');
    setNotice('');
    setSnapshot((current) => ({ ...current, saved: false }));
    setPending(true);
    let desired: Environment | null | undefined;
    const editing = editVersion.current;
    try {
      const environment = draftEnvironment;
      desired = environment;
      const result = await workspaceApi.saveResource(resource.id, {
        expectedRevision: observed.baselineRevision,
        environment,
      });
      setSnapshot((current) => {
        const confirmed = confirmEnvironmentSave(
          current,
          result.resource.environment,
          result.resource.revision,
        );
        return {
          ...confirmed,
          saved: confirmed.saved && editVersion.current === editing,
        };
      });
      onChange();
    } catch (error) {
      if (
        error instanceof APIError &&
        desired !== undefined &&
        (error.code === 'resource_conflict' ||
          error.status === 0 ||
          error.status >= 500)
      ) {
        if (error.code === 'resource_conflict') setNeedsRefresh(true);
        else setUnconfirmed(desired);
        try {
          const matched = await readSaved(
            error.code === 'resource_conflict' ? undefined : desired,
            editing,
          );
          if (!matched)
            setNotice(
              'Saved settings were refreshed. Your edits are kept; review them before saving again.',
            );
        } catch {
          setError(
            'Could not confirm the saved settings. Your edits are kept. Check saved settings before trying to save again.',
          );
        }
        return;
      }
      setError(
        error instanceof Error
          ? error.message
          : 'Could not save this environment.',
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="library-environment" aria-label="Environment">
      <header className="app-toolbar library-environment-toolbar">
        <Button ref={back} disabled={pending} onClick={onClose}>
          Back to Files
        </Button>
      </header>
      <form
        className="library-environment-form"
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        <div className="library-environment-body">
          <div className="library-environment-sheet">
            {(updatedElsewhere || saveUnconfirmed) && (
              <aside className="library-environment-notice">
                {updatedElsewhere && (
                  <p role="status">
                    <strong>Saved settings changed.</strong> Your edits are
                    still here.
                  </p>
                )}
                <details className="p9-disclosure" open={updatedElsewhere}>
                  <summary>
                    {updatedElsewhere
                      ? 'Saved Configuration'
                      : 'Last Known Saved Configuration'}
                  </summary>
                  <pre>
                    {currentEnvironment
                      ? JSON.stringify(currentEnvironment, null, 2)
                      : 'No environment configured.'}
                  </pre>
                </details>
                {updatedElsewhere && (
                  <>
                    <p>
                      {unconfirmed !== undefined
                        ? 'Check Saved Settings to confirm the last save.'
                        : 'Reload replaces your edits with the saved settings.'}
                    </p>
                    <div className="library-environment-conflict-actions">
                      <Button
                        disabled={pending || unconfirmed !== undefined}
                        onClick={keepEdits}
                      >
                        Keep My Edits
                      </Button>
                      <Button
                        disabled={pending || unconfirmed !== undefined}
                        onClick={reload}
                      >
                        Reload Saved Settings
                      </Button>
                    </div>
                  </>
                )}
              </aside>
            )}
            <fieldset
              className="library-environment-fields"
              aria-label="Environment configuration"
              disabled={pending || unconfirmed !== undefined}
            >
              <div className="library-environment-runtime">
                <Select
                  label="Runtime"
                  value={kind}
                  onChange={(event) =>
                    changeDraft({
                      kind: event.target.value as EnvironmentDraft['kind'],
                    })
                  }
                >
                  <option value="none">None</option>
                  <option value="command">Commands</option>
                  <option value="node">Node 24.16.0</option>
                </Select>
                {kind === 'node' && (
                  <Select
                    className="library-package-manager"
                    label="Package manager"
                    value={manager}
                    onChange={(event) =>
                      changeDraft({ manager: event.target.value })
                    }
                  >
                    <option value="node">No dependency installation</option>
                    <option value="pnpm@12.11.2">pnpm 12.11.2</option>
                  </Select>
                )}
              </div>
              {kind !== 'none' ? (
                <EnvironmentChecks
                  key={kind}
                  checks={draft.checks[kind]}
                  kind={kind}
                  manager={manager}
                  onChange={(checks) =>
                    changeDraft({ checks: { ...draft.checks, [kind]: checks } })
                  }
                />
              ) : (
                <section
                  className="library-environment-checks"
                  aria-label="Checks"
                >
                  <h3>Checks</h3>
                  <p className="library-check-empty">
                    Checks are off. Agents can still read and edit files.
                  </p>
                </section>
              )}
            </fieldset>
          </div>
        </div>
        <footer className="library-environment-footer">
          <div className="library-environment-sheet">
            {error && (
              <p className="app-error" role="alert">
                {error}
              </p>
            )}
            {notice && !observed.saved && !updatedElsewhere && (
              <p className="library-environment-hint" role="status">
                {notice}
              </p>
            )}
            <div className="library-environment-actions">
              <p
                id={scopeId}
                className="library-environment-hint library-environment-save-text"
                role="status"
              >
                {saveStatus}
              </p>
              <div className="library-environment-save-controls">
                {saveUnconfirmed && (
                  <Button
                    type="button"
                    primary={!pending}
                    disabled={pending}
                    onClick={() => void checkSaved()}
                  >
                    {pending ? 'Checking…' : 'Check Saved Settings'}
                  </Button>
                )}
                <Button
                  type="submit"
                  primary={canSave}
                  disabled={!canSave}
                  aria-describedby={showScope ? scopeId : undefined}
                >
                  {pending && !saveUnconfirmed ? 'Saving…' : 'Save'}
                </Button>
              </div>
            </div>
          </div>
        </footer>
      </form>
    </section>
  );
}
