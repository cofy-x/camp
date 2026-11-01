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
  const checksId = useId();
  const [kind, setKind] = useState(resource.environment?.kind ?? 'none');
  const [manager, setManager] = useState(
    resource.environment?.packageManager ?? 'node',
  );
  const [checks, setChecks] = useState(
    JSON.stringify(resource.environment?.checks ?? [], null, 2),
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
  const currentChecks = currentEnvironment?.checks ?? [];

  function changed() {
    editVersion.current++;
    setSnapshot((current) => ({ ...current, saved: false }));
    setError('');
    setNotice('');
  }

  function reload() {
    setKind(currentEnvironment?.kind ?? 'none');
    setManager(currentEnvironment?.packageManager ?? 'node');
    setChecks(JSON.stringify(currentEnvironment?.checks ?? [], null, 2));
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
      const configured = (
        kind === 'none' ? [] : JSON.parse(checks)
      ) as Environment['checks'];
      if (!Array.isArray(configured))
        throw new Error('Checks must be a JSON array.');
      const environment: Environment | null =
        kind === 'none'
          ? null
          : {
              kind: kind as Environment['kind'],
              checks: configured,
              ...(kind === 'node'
                ? { nodeVersion: '24.16.0', packageManager: manager }
                : {}),
            };
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
          <p>{resource.name}</p>
        </div>
        <Button disabled={pending} onClick={onClose}>
          Back to Files
        </Button>
      </header>
      <p className="library-environment-description">
        Optional tools and checks for future agent requests. Reading, writing
        and reviewing documents need no environment. Queued work keeps its
        original configuration.
      </p>
      <dl className="library-environment-summary">
        <dt>Saved environment</dt>
        <dd>
          {currentEnvironment?.kind === 'node'
            ? `Node ${currentEnvironment.nodeVersion ?? '24.16.0'} · ${currentEnvironment.packageManager === 'pnpm@12.11.2' ? 'pnpm 12.11.2' : 'No dependency installation'}`
            : currentEnvironment?.kind === 'command'
              ? 'Commands'
              : 'None'}
        </dd>
        <dt>Checks</dt>
        <dd>
          {currentChecks.length > 0
            ? currentChecks.map((check) => check.name).join(', ')
            : 'Not configured'}
        </dd>
      </dl>
      {updatedElsewhere && (
        <div className="library-environment-notice" role="status">
          <p>
            Saved settings changed in another session. Your edits are still
            here. Compare the saved configuration with your entries below. Keep
            your edits to continue reviewing, or reload to replace them.
          </p>
          <details open>
            <summary>Saved Configuration</summary>
            <pre>{JSON.stringify(currentEnvironment ?? {}, null, 2)}</pre>
          </details>
          <div className="library-environment-conflict-actions">
            <Button disabled={pending} onClick={keepEdits}>
              Keep My Edits
            </Button>
            <Button disabled={pending} onClick={reload}>
              Reload Saved Settings
            </Button>
          </div>
          <p>
            Keeping edits does not save them. Review your entries, then choose
            Save Environment.
          </p>
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
          disabled={pending || unconfirmed !== undefined}
        >
          <legend>Configuration</legend>
          <Select
            disabled={pending}
            label="Environment"
            value={kind}
            onChange={(event) => {
              setKind(event.target.value);
              setChecks('[]');
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
            <div className="library-environment-checks">
              <label htmlFor={checksId}>Checks (JSON array)</label>
              <p id={`${checksId}-help`}>
                Use an empty array for no checks. Saving configuration does not
                run checks or install tools.
              </p>
              <textarea
                id={checksId}
                aria-describedby={`${checksId}-help ${checksId}-example`}
                disabled={pending}
                rows={6}
                value={checks}
                spellCheck={false}
                onChange={(event) => {
                  setChecks(event.target.value);
                  changed();
                }}
              />
              <div
                id={`${checksId}-example`}
                className="library-environment-hint"
              >
                <span>Example</span>
                <code>
                  {kind === 'node'
                    ? '[{"name":"Tests","script":"test"}]'
                    : '[{"name":"Tests","arguments":["python3","-m","unittest"]}]'}
                </code>
                <p>
                  An optional directory is relative to the resource. Commands
                  run directly, without an implied shell; their tools must
                  already be available in the execution environment.
                </p>
              </div>
            </div>
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
          {observed.saved && <span role="status">Environment saved.</span>}
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
