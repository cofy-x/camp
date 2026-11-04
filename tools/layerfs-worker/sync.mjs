import { execFile } from 'node:child_process';
import path from 'node:path';
import { promisify } from 'node:util';
import { failure } from './state.mjs';

const execute = promisify(execFile);

// Production passes its verified packaged helper. Source probes may retain the
// existing per-file synchronization by omitting it. A selected helper never
// falls back after a missing executable, failed flush, timeout or invalid reply.
export function treeSynchronizer(helper) {
  if (helper === undefined || helper === '') return undefined;
  if (typeof helper !== 'string' || !path.isAbsolute(helper))
    throw failure(
      'InvalidInput',
      'Use an absolute staging synchronization helper.',
    );
  return async (directory) => {
    const { stdout } = await execute(helper, ['sync-staging-tree', directory], {
      env: { PATH: '/usr/bin:/bin', LANG: 'C.UTF-8' },
      timeout: 600_000,
      maxBuffer: 4096,
      killSignal: 'SIGTERM',
    });
    let result;
    try {
      result = JSON.parse(stdout);
    } catch {
      throw failure(
        'Unknown',
        'Staging synchronization did not confirm completion.',
      );
    }
    if (
      !result ||
      result.schema !== 1 ||
      result.synchronized !== true ||
      Object.keys(result).length !== 2
    )
      throw failure(
        'Unknown',
        'Staging synchronization returned an incompatible result.',
      );
  };
}
