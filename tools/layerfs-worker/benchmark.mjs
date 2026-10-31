import assert from 'node:assert/strict';
import { execFileSync, spawn } from 'node:child_process';
import { mkdir, mkdtemp, readFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { parseArgs } from 'node:util';
import { saveState } from './state.mjs';

assert.equal(process.platform, 'darwin');
assert.equal(process.arch, 'arm64');
const { values } = parseArgs({
  options: {
    smoke: { type: 'boolean', default: false },
    'output-base': { type: 'string' },
  },
  strict: true,
});
if (values['output-base'] && !path.isAbsolute(values['output-base']))
  throw new Error('--output-base requires an absolute private report parent.');
const parent = path.resolve(values['output-base'] ?? '.data');
await mkdir(parent, { recursive: true, mode: 0o700 });
const root = await mkdtemp(path.join(parent, 'mac-worker-benchmark-'));
const socket = execFileSync(
  'docker',
  ['context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'],
  { encoding: 'utf8' },
)
  .trim()
  .slice(7);
const report = {
  schema: 1,
  root,
  passed: false,
  repetitions: [],
  modelRequests: 0,
  scope: values.smoke
    ? 'one-round text harness preflight only'
    : 'three-round actual Mac four-workload comparison',
  policy:
    'all repetitions retained, no cache flush, existing OrbStack VM and unrelated services left running; each repetition has a fresh Mac client, worker process and owned Store; arms share that worker, image and VM, and their order rotates',
};
try {
  for (let iteration = 1; iteration <= (values.smoke ? 1 : 3); iteration++) {
    const child = spawn(
      process.execPath,
      [
        path.join(import.meta.dirname, 'benchmark-run.mjs'),
        root,
        socket,
        String(iteration),
        ...(values.smoke ? ['smoke'] : []),
      ],
      {
        env: { PATH: process.env.PATH, HOME: root, LANG: 'C.UTF-8' },
        stdio: 'inherit',
      },
    );
    const exit = await new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('close', resolve);
    });
    report.repetitions.push({
      iteration,
      exit,
      report: path.join(root, `round-${iteration}/report.json`),
    });
    await saveState(path.join(root, 'report.json'), report);
  }
  report.passed = report.repetitions.every((item) => item.exit === 0);
  const rounds = await Promise.all(
    report.repetitions.map((item) =>
      readFile(item.report, 'utf8').then(JSON.parse),
    ),
  );
  await saveState(path.join(root, 'native-plan.json'), {
    schema: 1,
    scope:
      'actual production native-local Prepare/publication Capture; complete-tree capture/restore measured separately',
    fixtures: rounds.flatMap((item) => item.nativeFixtures),
  });
} finally {
  await saveState(path.join(root, 'report.json'), report);
  process.stdout.write(
    `Actual Mac ${report.scope} ${report.passed ? 'passed' : 'failed'}: ${path.join(root, 'report.json')}\n`,
  );
  if (!report.passed) process.exitCode = 1;
}
