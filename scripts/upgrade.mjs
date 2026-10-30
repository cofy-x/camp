import { join } from 'node:path';
import { command, go } from './lib.mjs';

// Qualify the latest public installation, including its retained Runner journal.
// The current launcher verifies and runs the immutable baseline archives.
export const upgradeBaseline = 'v0.1.0-alpha.2';

export async function baselineLauncher(directory, role) {
  const binary = join(directory, 'baseline-camp');
  const cache = join(directory, 'baseline-cache');
  await command(go, [
    'build',
    '-trimpath',
    '-ldflags',
    `-X main.version=${upgradeBaseline}`,
    '-o',
    binary,
    '.',
  ]);
  await command(binary, [role === 'runner' ? 'runner' : 'serve', '--help'], {
    cwd: directory,
    env: {
      PATH: process.env.PATH,
      HOME: directory,
      LANG: 'C.UTF-8',
      XDG_CACHE_HOME: cache,
    },
  });
  return { binary, cache };
}
