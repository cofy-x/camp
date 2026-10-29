import { Button, PixelIcon } from '@cofy-x/camp-platinum';
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
  rechecking,
  error,
  onRefresh,
  onRecheck,
  onSection,
  onAgents,
  onWorkspace,
}: {
  readiness: Readiness | null;
  loading: boolean;
  rechecking: boolean;
  error: string;
  onRefresh: () => void;
  onRecheck: () => void;
  onSection: (section: 'models' | 'environments' | 'runners' | 'team') => void;
  onAgents: () => void;
  onWorkspace: () => void;
}) {
  const component = (id: string) =>
    readiness?.components.find((item) => item.id === id);
  const model = component('model');
  const connection = component('connection');
  const runtime = component('runtime') ?? component('execution');
  const pdf = component('document-pdf');
  const docx = component('document-docx');
  const readers = [pdf, docx];
  const readersReady = readers.filter(
    (item) => item?.status === 'ready',
  ).length;
  const readersUnobserved = readers.every(
    (item) => !item || item.status === 'unknown',
  );
  const issues =
    readiness?.components.filter(
      (item) => item.status === 'missing' || item.status === 'error',
    ) ?? [];
  return (
    <section className="camp-getting-ready" aria-label="Getting Ready">
      <header className="camp-preparation-heading">
        <PixelIcon name="checklist" size={28} />
        <div>
          <h2>Getting Ready</h2>
        </div>
        <Button primary onClick={onWorkspace}>
          Open Workspace
        </Button>
      </header>
      {error && (
        <div className="app-error" role="alert">
          <p>
            {error}
            {readiness ? ' Previous observations are shown below.' : ''}
          </p>
          <Button disabled={loading || rechecking} onClick={onRefresh}>
            Retry Status
          </Button>
        </div>
      )}
      {loading && !readiness && <p role="status">Loading setup status…</p>}
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
            <dd className="camp-preparation-action">
              <Button onClick={() => onSection('models')}>
                Model Settings
              </Button>
            </dd>
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
                {rechecking
                  ? 'Checking…'
                  : readiness.ready
                    ? 'Available'
                    : 'Unavailable'}
              </strong>
              {(rechecking || !readiness.ready) && (
                <p>
                  {rechecking
                    ? 'New work waits until this check finishes.'
                    : runtime?.detail ||
                      'Open Diagnostic details to review setup.'}
                </p>
              )}
            </dd>
            <dd className="camp-preparation-action">
              <Button onClick={() => onSection('runners')}>Open Runners</Button>
            </dd>
          </div>
          <div>
            <dt>Document reading</dt>
            <dd>
              <strong>
                {readersReady === 2
                  ? 'Available'
                  : readersUnobserved
                    ? 'Not checked'
                    : readersReady
                      ? 'Partly available'
                      : 'Unavailable'}
              </strong>
              {!readersUnobserved && readersReady !== 2 && (
                <p>
                  PDF: {pdf ? statusLabel(pdf) : 'Not checked'} · Word:{' '}
                  {docx ? statusLabel(docx) : 'Not checked'}
                </p>
              )}
            </dd>
          </div>
        </dl>
      )}
      <div className="camp-preparation-check">
        <Button disabled={loading || rechecking} onClick={onRecheck}>
          {rechecking ? 'Checking Environment…' : 'Check Environment'}
        </Button>
      </div>
      <fieldset className="app-control-group camp-preparation-team">
        <legend>Team</legend>
        <div className="app-actions">
          <Button onClick={() => onSection('team')}>Team Settings</Button>
          <Button onClick={onAgents}>Open Agents</Button>
        </div>
      </fieldset>
      {readiness && (
        <details className="camp-preparation-diagnostics">
          <summary>
            Diagnostic details
            {issues.length ? ` · ${issues.length} unavailable or failed` : ''}
          </summary>
          {readiness.checkedAt && (
            <p>Latest observation: {observationTime(readiness.checkedAt)}</p>
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
