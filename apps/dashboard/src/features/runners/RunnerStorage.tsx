import type { Runner } from './api';

const messages: Record<string, string> = {
  'retained-budget-exceeded': 'Over retained budget',
  'retained-budget-near': 'Near retained budget',
  'disk-free-low': 'Low disk space',
  'observation-incomplete': 'Incomplete observation',
  'observation-unavailable': 'Usage unavailable',
};

function bytes(value: number | null) {
  if (value === null) return 'Not reported';
  for (const [unit, scale] of [
    ['TiB', 2 ** 40],
    ['GiB', 2 ** 30],
    ['MiB', 2 ** 20],
    ['KiB', 2 ** 10],
  ] as const)
    if (value >= scale) return `${(value / scale).toFixed(1)} ${unit}`;
  return `${value} B`;
}

export function RunnerStorage({
  runner,
  details = false,
}: {
  runner: Runner;
  details?: boolean;
}) {
  const storage = runner.capabilities.storage;
  if (!storage || (!details && storage.level === 'ok')) return null;
  const stale =
    !runner.online || Date.now() - Date.parse(storage.observedAt) > 15 * 60_000;
  return (
    <section
      className={details ? 'camp-runner-section' : 'camp-runner-next-step'}
      aria-label="Checkpoint storage"
    >
      <h3>Checkpoint storage</h3>
      {storage.warnings.length > 0 && (
        <p>
          {storage.warnings
            .map((warning) => messages[warning] ?? 'Check storage')
            .join(' · ')}
        </p>
      )}
      {(storage.logicalBytes !== null || storage.freeBytes !== null) && (
        <p>
          {storage.logicalBytes === null
            ? 'Retained usage not observed'
            : `${storage.complete ? '' : 'At least '}${bytes(storage.logicalBytes)} retained`}
          {' · '}
          {storage.freeBytes === null
            ? 'Free disk not observed'
            : `${bytes(storage.freeBytes)} free`}
        </p>
      )}
      {details && (
        <dl className="camp-runner-facts">
          <dt>Retained budget</dt>
          <dd>
            {storage.retainedBudgetBytes === 0
              ? 'Disabled'
              : bytes(storage.retainedBudgetBytes)}
          </dd>
          <dt>Minimum free</dt>
          <dd>
            {storage.minFreeBytes === 0
              ? 'Disabled'
              : bytes(storage.minFreeBytes)}
          </dd>
        </dl>
      )}
      {(details || stale) && (
        <p className="app-muted">
          {stale ? 'Earlier observation' : 'Observed'} ·{' '}
          <time dateTime={storage.observedAt}>
            {new Date(storage.observedAt).toLocaleString()}
          </time>
        </p>
      )}
      {details && (
        <p>
          On the Runner host:{' '}
          <code>camp runner checkpoint storage --data-dir PATH</code>
        </p>
      )}
    </section>
  );
}
