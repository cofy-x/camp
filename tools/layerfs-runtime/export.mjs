import { spawn } from 'node:child_process';
import process from 'node:process';
import { clearTimeout, setTimeout } from 'node:timers';

// Installed root-owned in the image. The host invokes this only after tool
// admission stops and all untrusted command descendants have been reaped.
let options;
try {
  options = JSON.parse(process.argv[2]);
  if (
    !Number.isSafeInteger(options.maxBytes) ||
    options.maxBytes < 1024 ||
    options.maxBytes > 512 * 1024 * 1024 ||
    !Array.isArray(options.excludeNames) ||
    options.excludeNames.length > 64 ||
    options.excludeNames.some(
      (name) =>
        typeof name !== 'string' ||
        !/^[A-Za-z0-9_.-]{1,128}$/.test(name) ||
        name === '.' ||
        name === '..',
    )
  )
    throw new Error('Invalid archive policy.');
} catch {
  process.stderr.write('Invalid archive policy.\n');
  process.exit(64);
}

const child = spawn(
  '/bin/tar',
  [
    '--format=pax',
    ...options.excludeNames.map((name) => `--exclude=${name}`),
    '-cf',
    '-',
    '.',
  ],
  {
    stdio: ['ignore', 'pipe', 'pipe'],
    env: { PATH: '/usr/bin:/bin', LANG: 'C.UTF-8' },
  },
);
let bytes = 0;
let failure = 0;
let diagnostic = '';
const stop = (code, message) => {
  if (failure) return;
  failure = code;
  diagnostic = message;
  child.kill('SIGKILL');
};
const timer = setTimeout(
  () => stop(75, 'Archive export exceeded 30 seconds.'),
  30_000,
);
child.stdout.on('data', (chunk) => {
  if (failure) return;
  if (bytes + chunk.length > options.maxBytes) {
    stop(74, 'Archive export exceeded its byte budget.');
    return;
  }
  bytes += chunk.length;
  if (!process.stdout.write(chunk)) child.stdout.pause();
});
process.stdout.on('drain', () => child.stdout.resume());
process.stdout.on('error', () =>
  stop(74, 'Archive output could not be written.'),
);
child.stderr.on('data', (chunk) => {
  if (!failure && diagnostic.length < 8192)
    diagnostic += chunk.toString('utf8').slice(0, 8192 - diagnostic.length);
});
child.on('error', (error) => {
  failure = 71;
  diagnostic = `Archive command could not start: ${error.message}`;
});
child.on('close', (code) => {
  clearTimeout(timer);
  const exitCode = failure || (code === 0 ? 0 : 71);
  if (exitCode)
    process.stderr.write(`${diagnostic || 'Archive command failed.'}\n`);
  process.exitCode = exitCode;
});
