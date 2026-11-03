import { useEffect, useRef, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import { workspaceApi } from '../workspace/api';
import { runnersApi, type Runner, type RunnerSource } from './api';
import { runnerAdapters, runnerRuntime } from './presentation';
import { RunnerStorage } from './RunnerStorage';

function observedAt(value?: string) {
  return value ? new Date(value).toLocaleString() : 'Not reported';
}

export function RunnerDiagnostics({
  runner,
  getActivationRevision,
}: {
  runner: Runner;
  getActivationRevision: () => number;
}) {
  const team = useTeam();
  const { user } = useAccount();
  const manage = user.role === 'admin' || runner.ownerUserId === user.id;
  const [sources, setSources] = useState<RunnerSource[] | null>(null);
  const [error, setError] = useState('');
  const [revision, setRevision] = useState(0);
  const readiness = runner.capabilities.readiness;
  const adapters = runnerAdapters(runner);
  const observedRunId = runner.firstTaskRunId || runner.earlierTaskRunId;
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;

    return () => {
      mounted.current = false;
    };
  }, []);

  async function openObservedTask() {
    if (!observedRunId) return;
    const activation = getActivationRevision();
    try {
      const { run } = await workspaceApi.run(observedRunId);
      if (!mounted.current || getActivationRevision() !== activation) return;
      await team.navigate({
        app: 'workspace',
        roomId: run.roomId,
        runId: run.id,
      });
    } catch (error) {
      if (mounted.current && getActivationRevision() === activation)
        setError(
          error instanceof Error ? error.message : 'Could not open this task.',
        );
    }
  }

  useEffect(() => {
    if (!manage) return;
    const controller = new AbortController();
    void runnersApi
      .sources(runner.id, controller.signal)
      .then(({ sources }) => {
        if (controller.signal.aborted) return;
        setSources(sources ?? []);
        setError('');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error ? error.message : 'Could not load sources.',
          );
      });
    return () => controller.abort();
  }, [manage, runner.id, revision]);

  return (
    <div className="camp-runner-diagnostics">
      <RunnerStorage runner={runner} details />
      <dl className="camp-runner-facts">
        <dt>Runtime</dt>
        <dd>{runnerRuntime(runner)}</dd>
        {runner.capabilities.platform && (
          <>
            <dt>Platform</dt>
            <dd>{runner.capabilities.platform}</dd>
          </>
        )}
        {runner.capabilities.workspace && (
          <>
            <dt>Workspace</dt>
            <dd>{adapters.workspace}</dd>
          </>
        )}
        {runner.capabilities.sandbox && (
          <>
            <dt>Isolation</dt>
            <dd>{adapters.sandbox}</dd>
          </>
        )}
        {runner.capabilities.checkpoint && (
          <>
            <dt>Checkpoint</dt>
            <dd>{adapters.checkpoint}</dd>
          </>
        )}
        <dt>Execution slots</dt>
        <dd>{runner.capabilities.slots ?? runner.slots ?? 'Not reported'}</dd>
        <dt>Last connected</dt>
        <dd>{observedAt(runner.lastSeenAt)}</dd>
        <dt>Execution checked</dt>
        <dd>{observedAt(readiness?.checkedAt)}</dd>
        <dt>Model settings observed</dt>
        <dd>{observedAt(runner.modelCheckedAt)}</dd>
        {runner.capabilities.dshVersion && (
          <>
            <dt>DSH</dt>
            <dd>{runner.capabilities.dshVersion}</dd>
          </>
        )}
        {adapters.layerfsVersion && (
          <>
            <dt>LayerFS</dt>
            <dd>{adapters.layerfsVersion}</dd>
          </>
        )}
        {runner.capabilities.installation?.version && (
          <>
            <dt>Reported release</dt>
            <dd>{runner.capabilities.installation.version}</dd>
          </>
        )}
        {runner.capabilities.installation?.sourceCommit && (
          <>
            <dt>Source commit</dt>
            <dd>
              <code>{runner.capabilities.installation.sourceCommit}</code>
            </dd>
          </>
        )}
        {runner.capabilities.installation?.runtimeSHA256 && (
          <>
            <dt>Runtime digest</dt>
            <dd>
              <code>{runner.capabilities.installation.runtimeSHA256}</code>
            </dd>
          </>
        )}
      </dl>
      {readiness && (
        <section className="camp-runner-section" aria-label="Execution checks">
          <h3>Execution checks</h3>
          <ul className="camp-runner-checks">
            {readiness.checks.map((check) => (
              <li key={check.id}>
                <span>{check.label}</span>
                <strong>
                  {check.status === 'passed' ? 'Passed' : check.status}
                </strong>
                {check.detail && <small>{check.detail}</small>}
              </li>
            ))}
          </ul>
        </section>
      )}
      {runner.runtime === 'native' && (
        <section
          className="camp-runner-section"
          aria-label="First task verification"
        >
          <h3>First task</h3>
          <p>
            {runner.firstTaskSucceededAt
              ? `Native execution verified by completed work · ${observedAt(runner.firstTaskSucceededAt)}`
              : runner.earlierTaskRunId
                ? `Earlier success · runtime not recorded · ${observedAt(runner.earlierTaskSucceededAt)}`
                : 'Not yet verified by completed work.'}
          </p>
          {observedRunId && (
            <Button onClick={() => void openObservedTask()}>
              {runner.firstTaskRunId
                ? 'View Successful Task'
                : 'View Earlier Task'}
            </Button>
          )}
        </section>
      )}
      {manage && (
        <section className="camp-runner-section" aria-label="Directory sources">
          <h3>Directory sources</h3>
          {sources === null ? (
            !error && <p role="status">Loading sources…</p>
          ) : sources.length ? (
            <ul className="camp-runner-sources">
              {sources.map((source) => (
                <li key={source.id}>
                  <strong>{source.alias}</strong> ·{' '}
                  {source.available
                    ? 'Available for snapshot import'
                    : source.reason || 'Unavailable'}
                </li>
              ))}
            </ul>
          ) : (
            <p>No directory sources registered.</p>
          )}
        </section>
      )}
      {error && (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button onClick={() => setRevision((value) => value + 1)}>
            Retry
          </Button>
        </p>
      )}
    </div>
  );
}
