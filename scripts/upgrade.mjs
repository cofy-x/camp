import { join } from 'node:path';
import { command, go } from './lib.mjs';

// The original public release predates the root Go package. The current small
// launcher can still verify/run its immutable standalone Server/Runner assets.
export const upgradeBaseline = 'v0.1.0-alpha.1';

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
