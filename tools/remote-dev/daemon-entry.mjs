import { spawn } from 'node:child_process';
import process from 'node:process';

const name = process.env.CAMP_RUNNER_NAME;
if (!['daemon-a', 'daemon-b'].includes(name))
  throw new Error('Choose one fixture Runner.');

async function run(args) {
  return new Promise((resolve, reject) => {
    const child = spawn('camp', args, { stdio: 'inherit' });
    const stop = () => child.kill('SIGTERM');
    process.on('SIGTERM', stop);
    process.on('SIGINT', stop);
    child.once('error', reject);
    child.once('exit', (code) => {
      process.off('SIGTERM', stop);
      process.off('SIGINT', stop);
      if (code === 0) resolve();
      else reject(new Error(`Camp daemon exited (${code}).`));
    });
  });
}

// Join is idempotent for an enrolled Runner and resumes a persisted pending
// registration after a lost response. A configuration file alone proves neither.
await run([
  'runner',
  'setup',
  '--server',
  'https://server:3443',
  '--name',
  name,
  '--data-dir',
  '/data',
  '--code-file',
  '/join/token',
  '--ca-file',
  '/trust/ca.crt',
  '--executor',
  'fixture',
  '--source',
  'notes=/opt/camp-remote/sources',
]);
await run(['runner', 'run', '--data-dir', '/data']);
