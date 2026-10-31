import { spawn, spawnSync } from 'node:child_process';
import { Buffer } from 'node:buffer';
import { randomBytes } from 'node:crypto';
import { chmod, mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';

const root = import.meta.dirname;
const destination = path.resolve(
  process.argv[2] ?? '.data/worker-arm64-qualification.json',
);
await mkdir(path.dirname(destination), { recursive: true, mode: 0o700 });
const installed = JSON.parse(
  await readFile(
    path.join(root, 'node_modules/.camp-worker/installed.json'),
    'utf8',
  ),
);
const environment = Object.fromEntries(
  ['PATH', 'HOME', 'LANG']
    .filter((key) => process.env[key])
    .map((key) => [key, process.env[key]]),
);

function docker(args, json = false) {
  const answer = spawnSync('docker', args, {
    encoding: 'utf8',
    env: environment,
  });
  if (answer.error || answer.status !== 0)
    throw new Error(answer.error?.message ?? answer.stderr);
  return json ? JSON.parse(answer.stdout) : answer.stdout.trim();
}

function removeOwnedHome(homeVolume, name) {
  const owned = docker(['volume', 'inspect', homeVolume], true)[0];
  if (
    owned.Labels?.['cofy.camp.operation'] !== name ||
    owned.Labels?.['cofy.camp.scope'] !== 'arm64-qualification'
  )
    return 'Volume ownership changed; cleanup stopped.';
  docker(['volume', 'rm', homeVolume]);
  return null;
}

const endpoint = docker([
  'context',
  'inspect',
  '--format',
  '{{.Endpoints.docker.Host}}',
]);
if (!endpoint.startsWith('unix://'))
  throw new Error(
    'Qualification requires an explicitly selected local Docker socket.',
  );
environment.DOCKER_HOST = endpoint;
const info = docker(['info', '--format', '{{json .}}'], true);
if (!['aarch64', 'arm64'].includes(info.Architecture))
  throw new Error('The selected engine is not native ARM64.');
const volumes = `${info.DockerRootDir}/volumes`;
const suffix = randomBytes(8).toString('hex');
const name = `camp44-${suffix}`,
  homeVolume = `${name}-home`;
const label = 'cofy.camp.scope=arm64-qualification';
docker([
  'volume',
  'create',
  '--label',
  label,
  '--label',
  `cofy.camp.operation=${name}`,
  homeVolume,
]);
let report;
try {
  const child = spawn(
    'docker',
    [
      'run',
      '--rm',
      '-i',
      '--name',
      name,
      '--label',
      label,
      '--label',
      `cofy.camp.operation=${name}`,
      '--platform',
      'linux/arm64',
      '--pull=never',
      '--network=host',
      '--read-only',
      '--tmpfs',
      '/tmp:rw,nosuid,nodev,mode=1777',
      '--mount',
      'type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock',
      '--mount',
      `type=bind,source=${volumes},target=/var/lib/docker/volumes`,
      '--mount',
      `type=bind,source=${root},target=/camp,readonly`,
      installed.image,
      '/camp/qualify-linux.mjs',
    ],
    { env: environment, stdio: ['pipe', 'pipe', 'pipe'] },
  );
  const output = [],
    errors = [];
  child.stdout.on('data', (chunk) => output.push(chunk));
  child.stderr.on('data', (chunk) => errors.push(chunk));
  child.stdin.end(
    JSON.stringify({
      home: `/var/lib/docker/volumes/${homeVolume}/_data`,
      image: installed.image,
      project: name,
      prefix: `${name}-`,
    }),
  );
  const code = await new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('close', resolve);
  });
  const stdout = Buffer.concat(output).toString('utf8');
  try {
    report = JSON.parse(stdout);
  } catch {
    report = {
      passed: false,
      output: stdout,
      diagnostic: Buffer.concat(errors).toString('utf8'),
    };
  }
  Object.assign(report, {
    sourceCommit: installed.commit,
    images: {
      rust: installed.rustImage,
      node: installed.nodeImage,
      docker: installed.dockerImage,
    },
    hostPlatform: `${process.platform}/${process.arch}`,
    engine: {
      kernel: info.KernelVersion,
      architecture: info.Architecture,
      storageDriver: info.Driver,
    },
    exitCode: code,
  });
  if (code !== 0 || !report.passed) process.exitCode = 1;
} finally {
  try {
    const error = removeOwnedHome(homeVolume, name);
    if (error) {
      report ??= { passed: false };
      report.homeCleanupError = error;
      process.exitCode = 1;
    }
  } catch (error) {
    report ??= { passed: false };
    report.homeCleanupError = error.message;
    process.exitCode = 1;
  }
  await writeFile(destination, `${JSON.stringify(report, null, 2)}\n`, {
    mode: 0o600,
  });
  await chmod(destination, 0o600);
}
process.stdout.write(
  `ARM64 qualification ${report.passed ? 'passed' : 'failed'}: ${destination}\n`,
);
