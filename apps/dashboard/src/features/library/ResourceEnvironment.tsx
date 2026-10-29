import { useId, useState } from 'react';
import { Button, Select } from '@cofy-x/camp-platinum';
import type { Environment, Resource } from '../workspace/api';
import { workspaceApi } from '../workspace/api';
import {
  confirmEnvironmentSave,
  environmentChanged,
  initialEnvironment,
  observeEnvironment,
  reloadEnvironment,
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
  const [snapshot, setSnapshot] = useState(() =>
    initialEnvironment(resource.environment, resource.updatedAt),
  );
  const observed = observeEnvironment(
    snapshot,
    resource.environment,
    resource.updatedAt,
  );
  if (observed !== snapshot) setSnapshot(observed);
  const updatedElsewhere = environmentChanged(observed);
  const currentEnvironment = observed.current;
  const currentChecks = currentEnvironment?.checks ?? [];

  function changed() {
    setSnapshot((current) => ({ ...current, saved: false }));
    setError('');
  }

  function reload() {
    setKind(currentEnvironment?.kind ?? 'none');
    setManager(currentEnvironment?.packageManager ?? 'node');
    setChecks(JSON.stringify(currentEnvironment?.checks ?? [], null, 2));
    setSnapshot((current) => reloadEnvironment(current));
    setError('');
  }

  async function save() {
    if (updatedElsewhere) return;
    setError('');
    setSnapshot((current) => ({ ...current, saved: false }));
    setPending(true);
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
      const result = await workspaceApi.saveResource(resource.id, {
        environment,
      });
      setSnapshot((current) =>
        confirmEnvironmentSave(current, result.resource.environment),
      );
      onChange();
    } catch (error) {
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
            here. Reload the saved settings before saving; this replaces your
            current edits.
          </p>
          <Button disabled={pending} onClick={reload}>
            Reload Saved Settings
          </Button>
        </div>
      )}
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        <fieldset className="app-control-group" disabled={pending}>
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
          <Button type="submit" disabled={pending || updatedElsewhere}>
            {pending ? 'Saving…' : 'Save Environment'}
          </Button>
          {observed.saved && <span role="status">Environment saved.</span>}
        </div>
      </form>
    </section>
  );
}
