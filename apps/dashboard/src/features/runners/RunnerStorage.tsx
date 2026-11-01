import type { Runner } from './api';

const messages: Record<string, string> = {
  'retained-budget-exceeded':
    'Retained checkpoint data exceeds the soft budget.',
  'retained-budget-near':
    'Retained checkpoint data is approaching the soft budget.',
  'disk-free-low': 'Free disk space is below the configured minimum.',
  'observation-incomplete':
    'The bounded observation is incomplete. Usage may be higher.',
  'observation-unavailable': 'Storage usage could not be observed.',
};

function bytes(value: number | null) {
  if (value === null) return 'Not observed';
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
      <h3>
        {storage.level === 'warning'
          ? 'Storage needs attention'
          : 'Checkpoint storage'}
      </h3>
      {storage.warnings.map((warning) => (
        <p key={warning}>
          {messages[warning] ?? 'Check storage on the Runner host.'}
        </p>
      ))}
      <p>
        Retained: {storage.complete ? '' : 'at least '}
        {bytes(storage.logicalBytes)}
        {' · '}Free disk: {bytes(storage.freeBytes)}
      </p>
      <p>
        Soft budget:{' '}
        {storage.retainedBudgetBytes === 0
          ? 'Disabled'
          : bytes(storage.retainedBudgetBytes)}
        {' · '}Minimum free:{' '}
        {storage.minFreeBytes === 0 ? 'Disabled' : bytes(storage.minFreeBytes)}
      </p>
      <p className="app-muted">
        {stale ? 'Earlier observation' : 'Observed'} ·{' '}
        {new Date(storage.observedAt).toLocaleString()}
      </p>
      {(details || storage.level !== 'ok') && (
        <p>
          Inspect storage on the Runner host with{' '}
          <code>camp runner checkpoint storage</code> and its Runner data
          directory. Quarantine keeps all bytes; only an explicit permanent
          purge can remove them. Storage warnings do not block Agent work.
        </p>
      )}
    </section>
  );
}
