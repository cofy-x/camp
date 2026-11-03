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
  if (
    runner.runtime === 'fixture' ||
    runner.capabilities.executor === 'fixture'
  )
    return 'Test executor';
  if (
    ['native-local', 'native-layerfs-worker'].includes(
      runner.capabilities.workspace ?? '',
    ) &&
    runner.capabilities.sandbox === 'seatbelt'
  )
    return 'Mac native';
  if (runner.capabilities.workspace === 'layerfs') return 'Linux native';
  return 'Native execution';
}

export function runnerAdapters(runner: Runner) {
  const { workspace, sandbox, checkpoint, layerfsVersion } =
    runner.capabilities;
  const labels: Record<string, string> = {
    'native-local': 'Native local directory',
    'native-layerfs-worker': 'Native directory with LayerFS worker',
    layerfs: 'LayerFS',
    seatbelt: 'Seatbelt',
    'linux-container': 'Linux container',
    'local-capture': 'Sealed local capture',
    'layerfs-disposable+native-capture':
      'LayerFS checkpoint and native capture',
  };
  const reported = (value?: string) =>
    value ? (labels[value] ?? 'Unrecognized adapter') : '';
  return {
    workspace: reported(workspace),
    sandbox: reported(sandbox),
    checkpoint: reported(checkpoint),
    layerfsVersion:
      workspace === 'layerfs' ||
      workspace === 'native-layerfs-worker' ||
      checkpoint === 'layerfs' ||
      checkpoint === 'layerfs-disposable+native-capture'
        ? layerfsVersion
        : undefined,
  };
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
