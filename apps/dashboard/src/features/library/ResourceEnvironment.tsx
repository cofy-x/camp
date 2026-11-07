import { useId, useRef, useState } from 'react';
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
import { configuredChecks, editableCheck } from './environmentCheckCommands';
import './ResourceEnvironment.css';

export function EnvironmentEditor({
  resource,
  onChange,
  onClose,
}: {
  resource: Resource;
  onChange: () => void;
  onClose: () => void;
}) {
  const headingId = useId();
  const [kind, setKind] = useState(resource.environment?.kind ?? 'none');
  const [manager, setManager] = useState(
    resource.environment?.packageManager ?? 'node',
  );
  const [checks, setChecks] = useState(() =>
    (resource.environment?.checks ?? []).map(editableCheck),
  );
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
          kind: kind as Environment['kind'],
          checks: configuredChecks(checks),
          ...(kind === 'node'
            ? { nodeVersion: '24.16.0', packageManager: manager }
            : {}),
        };
  const unsaved = !sameEnvironment(draftEnvironment, currentEnvironment);
  const saveUnconfirmed = needsRefresh || unconfirmed !== undefined;
  const saveStatus =
    pending || saveUnconfirmed
      ? ''
      : unsaved
        ? 'Unsaved changes'
        : observed.saved
          ? 'Environment saved.'
          : '';

  function changed() {
    editVersion.current++;
    setSnapshot((current) => ({ ...current, saved: false }));
    setError('');
    setNotice('');
  }

  function reload() {
    setKind(currentEnvironment?.kind ?? 'none');
    setManager(currentEnvironment?.packageManager ?? 'node');
    setChecks((currentEnvironment?.checks ?? []).map(editableCheck));
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
    if (
      updatedElsewhere ||
      needsRefresh ||
      unconfirmed !== undefined ||
      pending
    )
      return;
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
    <section className="library-environment" aria-labelledby={headingId}>
      <header className="library-environment-heading">
        <div>
          <h2 id={headingId}>Environment and checks</h2>
        </div>
        <Button disabled={pending} onClick={onClose}>
          Back to Files
        </Button>
      </header>
      <p className="library-environment-description">
        Applies to future work; queued work keeps its configuration.
      </p>
      {updatedElsewhere && (
        <div className="library-environment-notice" role="status">
          <p>
            Saved settings changed. Your edits are still here. Compare the saved
            configuration with your entries below. Keep your edits to continue
            reviewing, or reload to replace them.
          </p>
          <details open>
            <summary>Saved Configuration</summary>
            <pre>{JSON.stringify(currentEnvironment ?? {}, null, 2)}</pre>
          </details>
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
          <p>
            {unconfirmed !== undefined
              ? 'A save outcome is unconfirmed. Check Saved Settings before choosing a conflict action.'
              : 'Keeping edits does not save them. Review your entries, then choose Save Environment.'}
          </p>
        </div>
      )}
      {saveUnconfirmed && !updatedElsewhere && (
        <div className="library-environment-notice">
          <details open>
            <summary>Last Known Saved Configuration</summary>
            <pre>{JSON.stringify(currentEnvironment ?? {}, null, 2)}</pre>
          </details>
        </div>
      )}
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        <fieldset
          className="app-control-group"
          aria-label="Environment configuration"
          disabled={pending || unconfirmed !== undefined}
        >
          <Select
            disabled={pending}
            label="Environment"
            value={kind}
            onChange={(event) => {
              setKind(event.target.value);
              setChecks([]);
              changed();
            }}
          >
            <option value="none">None</option>
            <option value="command">Commands</option>
            <option value="node">Node 24.16.0</option>
          </Select>
          {kind === 'node' && (
            <Select
              disabled={pending}
              label="Package manager"
              value={manager}
              onChange={(event) => {
                setManager(event.target.value);
                changed();
              }}
            >
              <option value="node">No dependency installation</option>
              <option value="pnpm@12.11.2">pnpm 12.11.2</option>
            </Select>
          )}
          {kind !== 'none' ? (
            <EnvironmentChecks
              checks={checks}
              kind={kind}
              manager={manager}
              onChange={(value) => {
                setChecks(value);
                changed();
              }}
            />
          ) : (
            <p className="library-environment-hint">
              Checks: Not configured. Agents can still read and edit files.
            </p>
          )}
        </fieldset>
        {error && (
          <p className="app-error" role="alert">
            {error}
          </p>
        )}
        <div className="library-environment-actions">
          <Button
            type="submit"
            disabled={
              pending ||
              updatedElsewhere ||
              needsRefresh ||
              unconfirmed !== undefined
            }
          >
            {pending ? 'Saving…' : 'Save Environment'}
          </Button>
          {saveStatus && <span role="status">{saveStatus}</span>}
          {notice && !observed.saved && !updatedElsewhere && (
            <span role="status">{notice}</span>
          )}
          {(needsRefresh || unconfirmed !== undefined) && (
            <Button
              type="button"
              disabled={pending}
              onClick={() => void checkSaved()}
            >
              Check Saved Settings
            </Button>
          )}
        </div>
      </form>
    </section>
  );
}
