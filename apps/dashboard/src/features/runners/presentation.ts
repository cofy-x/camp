import type { Runner, SourceImport } from './api';

export function runnerSetupCommand(origin: string, name: string) {
  const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;
  return `./camp runner setup --server ${quote(origin)} --name ${quote(name)}`;
}

export function runnerStatus(runner: Runner) {
  if (runner.revoked) return 'Access revoked';
  if (!runner.enabled) return 'Disabled';
  if (!runner.online) return 'Offline';
  if (!runner.capabilities.ready) return 'Needs attention';
  return 'Online';
}

export function runnerRuntime(runner: Runner) {
  if (!runner.runtime && !runner.capabilities.executor)
    return 'Awaiting capability report';
  return runner.runtime === 'fixture' ||
    runner.capabilities.executor === 'fixture'
    ? 'Test executor'
    : 'DSH + LayerFS';
}

export function runnerQueueMessage(runner: Runner | undefined) {
  if (!runner) return 'Choose a Runner before assigning Agent work.';
  if (runner.revoked)
    return 'This Runner’s access was revoked. Choose another Runner.';
  if (!runner.enabled)
    return 'This Runner is disabled. Enable it or choose another Runner before assigning work.';
  if (runner.capabilities.executor && !runner.capabilities.ready)
    return (
      runner.capabilities.reason ||
      'This Runner needs attention before accepting new work.'
    );
  if (!runner.online)
    return 'New work queues here until this Runner reconnects.';
  return '';
}

export function importPending(value: Pick<SourceImport, 'status'>) {
  return ['queued', 'capturing', 'uploading', 'cancelling'].includes(
    value.status,
  );
}

export function mergeImportStatus(
  current: SourceImport | null,
  incoming: SourceImport,
) {
  if (current?.id === incoming.id && !importPending(current)) return current;
  if (current?.id === incoming.id && current.updatedAt > incoming.updatedAt)
    return current;
  return incoming;
}

export const importStatus: Record<SourceImport['status'], string> = {
  queued: 'Waiting for Runner',
  capturing: 'Capturing snapshot',
  uploading: 'Uploading snapshot',
  cancelling: 'Stopping import',
  succeeded: 'Room created',
  failed: 'Import failed',
  cancelled: 'Import cancelled',
  interrupted: 'Import interrupted',
};
