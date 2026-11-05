import { Button } from '@cofy-x/camp-platinum';
import type { Readiness } from './api';

type Component = Readiness['components'][number];

function observationTime(value?: string) {
  return value ? new Date(value).toLocaleString() : '';
}

function statusLabel(item: Component) {
  if (item.status === 'ready') return 'Ready';
  if (item.status === 'unknown') return 'Not checked';
  if (item.status === 'missing') return 'Unavailable';
  return 'Failed';
}

export function GettingReady({
  readiness,
  loading,
  error,
  onRefresh,
  onSection,
  onRunners,
  onWorkspace,
}: {
  readiness: Readiness | null;
  loading: boolean;
  error: string;
  onRefresh: () => void;
  onSection: (section: 'models') => void;
  onRunners: (connect?: boolean) => void;
  onWorkspace: () => void;
}) {
  const component = (id: string) =>
    readiness?.components.find((item) => item.id === id);
  const model = component('model');
  const connection = component('connection');
  const runtime = component('execution');
  const firstTask = component('first-task');
  const next =
    model?.status !== 'ready'
      ? {
          destination: 'models',
          title: 'Connect a model',
          detail:
            'Save your model settings. Your provider key stays on the Server.',
          action: 'Configure Model…',
          run: () => onSection('models'),
        }
      : runtime?.status !== 'ready' || !readiness?.ready
        ? {
            destination: 'connect',
            title: 'Connect a Runner',
            detail: 'Connect a supported Mac or Linux machine for Agent work.',
            action: 'Connect Runner…',
            run: () => onRunners(true),
          }
        : firstTask?.status !== 'ready'
          ? {
              destination: 'runners',
              title: 'Try your first Agent task',
              detail:
                'Choose your Runner and run a sample file task. This makes a model request.',
              action: 'Choose Runner…',
              run: () => onRunners(),
            }
          : {
              destination: 'workspace',
              title: 'Start working',
              detail: 'A native Agent task succeeded previously.',
              action: 'Start Working',
              run: onWorkspace,
            };
  const issues =
    readiness?.components.filter(
      (item) => item.status === 'missing' || item.status === 'error',
    ) ?? [];
  return (
    <section className="camp-getting-ready" aria-label="Getting Ready">
      {(!readiness || next.destination !== 'workspace') && (
        <div className="camp-preparation-heading">
          <Button onClick={onWorkspace}>Open Workspace</Button>
        </div>
      )}
      {error && (
        <div className="app-error" role="alert">
          <p>
            {error}
            {readiness ? ' Previous observations are shown below.' : ''}
          </p>
        </div>
      )}
      {loading && !readiness && <p role="status">Loading setup status…</p>}
      {readiness && (
        <section className="camp-preparation-next" aria-label="Next setup step">
          <div>
            <h3>{next.title}</h3>
            <p>{next.detail}</p>
          </div>
          <Button primary onClick={next.run}>
            {next.action}
          </Button>
        </section>
      )}
      {readiness && (
        <dl className="camp-preparation-status" aria-label="Setup status">
          <div>
            <dt>Model settings</dt>
            <dd>
              <strong className={model?.status}>
                {model?.status === 'ready'
                  ? 'Configured'
                  : model?.status === 'unknown' || !model
                    ? 'Not checked'
                    : 'Setup required'}
              </strong>
              {model && ['missing', 'error'].includes(model.status) && (
                <p>{model.detail}</p>
              )}
            </dd>
            {next.destination !== 'models' && (
              <dd className="camp-preparation-action">
                <Button onClick={() => onSection('models')}>
                  Model Settings
                </Button>
              </dd>
            )}
          </div>
          <div>
            <dt>Model connection</dt>
            <dd>
              <strong className={connection?.status}>
                {connection?.status === 'ready'
                  ? 'Response observed'
                  : !connection || connection.status === 'unknown'
                    ? 'Not verified'
                    : 'Last connection failed'}
              </strong>
              {connection &&
                ['missing', 'error'].includes(connection.status) && (
                  <p>{connection.detail}</p>
                )}
            </dd>
          </div>
          <div>
            <dt>Agent work</dt>
            <dd>
              <strong className={runtime?.status}>
                {readiness.ready ? 'Available' : 'Unavailable'}
              </strong>
              {!readiness.ready && (
                <p>
                  {runtime?.detail ||
                    'Open Diagnostic details to review setup.'}
                </p>
              )}
            </dd>
            {next.destination !== 'runners' && (
              <dd className="camp-preparation-action">
                <Button onClick={() => onRunners()}>Open Runners</Button>
              </dd>
            )}
          </div>
          <div>
            <dt>Native task</dt>
            <dd>
              <strong>
                {firstTask?.status === 'ready'
                  ? 'Succeeded previously'
                  : 'Not yet verified'}
              </strong>
            </dd>
          </div>
        </dl>
      )}
      <div className="camp-preparation-check">
        <Button disabled={loading} onClick={onRefresh}>
          {loading
            ? 'Refreshing Status…'
            : error
              ? 'Retry Status'
              : 'Refresh Status'}
        </Button>
      </div>
      {readiness && (
        <details className="camp-preparation-diagnostics">
          <summary>
            Diagnostic details
            {issues.length ? ` · ${issues.length} unavailable or failed` : ''}
          </summary>
          {readiness.checkedAt && (
            <p>Status refreshed: {observationTime(readiness.checkedAt)}</p>
          )}
          <ul className="camp-readiness">
            {readiness.components.map((item) => (
              <li key={item.id} className={item.status}>
                <div className="camp-diagnostic-heading">
                  <strong>{item.label}</strong>
                  <span>{statusLabel(item)}</span>
                </div>
                <p>{item.detail}</p>
                {item.checkedAt && (
                  <small>Observed {observationTime(item.checkedAt)}</small>
                )}
                {(item.lastFailureAt || item.lastSuccessAt) && (
                  <details>
                    <summary>Previous observations</summary>
                    {item.lastSuccessAt && (
                      <p>
                        Last success: {observationTime(item.lastSuccessAt)} ·{' '}
                        {item.lastSuccess}
                      </p>
                    )}
                    {item.lastFailureAt && (
                      <p>
                        Last failure: {observationTime(item.lastFailureAt)} ·{' '}
                        {item.lastFailure}
                      </p>
                    )}
                  </details>
                )}
              </li>
            ))}
          </ul>
        </details>
      )}
    </section>
  );
}
