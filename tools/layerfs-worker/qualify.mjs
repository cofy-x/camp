import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { chmod, mkdir, open, readFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import {
  inspectSourceContainer,
  inspectSourceVolume,
  removeSourceVolume,
  requireSourceEngine,
  saveSourceEngineProof,
  selectSourceEngine,
  sourceDocker,
  sourceEngineArguments,
  stopSourceContainer,
  waitForSourceProbe,
} from './source-engine.mjs';

const root = import.meta.dirname;
const options = sourceEngineArguments(process.argv.slice(2), { output: true });
const destination = path.resolve(
  options.output ?? '.data/worker-arm64-qualification.json',
);
const installed = JSON.parse(
  await readFile(
    path.join(root, 'node_modules/.camp-worker/installed.json'),
    'utf8',
  ),
);
const selected = await selectSourceEngine(options);
const info = requireSourceEngine(selected);
assert.ok(
  ['aarch64', 'arm64'].includes(info.Architecture),
  'The selected engine is not native ARM64.',
);
assert.ok(
  path.posix.isAbsolute(info.DockerRootDir) &&
    !['\0', '\r', '\n', ','].some((character) =>
      info.DockerRootDir.includes(character),
    ),
  'The selected engine has an invalid local volume directory.',
);
const volumes = path.posix.join(info.DockerRootDir, 'volumes');
const docker = (args, json = false) => sourceDocker(selected, args, { json });
const image = docker(['image', 'inspect', installed.image], true)[0];
assert.equal(image.Id, installed.image);
assert.equal(image.Architecture, 'arm64');
assert.equal(image.Os, 'linux');
const nonce = randomBytes(16).toString('hex');
const name = 'camp44-' + nonce;
const owner = {
  engineID: info.ID,
  container: name,
  volume: name + '-home',
  image: installed.image,
  labels: {
    'cofy.camp.scope': 'arm64-qualification',
    'cofy.camp.operation': name,
    'cofy.camp.nonce': nonce,
    'cofy.camp.image': installed.image,
  },
};
const labelArgs = Object.entries(owner.labels).flatMap(([key, value]) => [
  '--label',
  key + '=' + value,
]);
const sourceInputs = async () =>
  Object.fromEntries(
    await Promise.all(
      [
        'source-engine.mjs',
        'qualify.mjs',
        'qualify-linux.mjs',
        'pin.json',
        '../layerfs-runtime/pin.json',
      ].map(async (file) => [
        file,
        createHash('sha256')
          .update(await readFile(path.join(root, file)))
          .digest('hex'),
      ]),
    ),
  );
const inputs = await sourceInputs();
await mkdir(path.dirname(destination), { recursive: true, mode: 0o700 });
const output = await open(destination, 'wx', 0o600);
await saveSourceEngineProof(selected, {
  operation: 'native-linux-arm64-image-qualification',
  phase: 'intent',
  owner,
  report: destination,
  sourceInputs: inputs,
  interruptionLimit:
    'SIGKILL cannot run cleanup; retain this intent for operator inspection. No automatic replay is performed.',
});
const interrupted = new globalThis.AbortController();
const interrupt = (signal) =>
  interrupted.abort(
    new Error('Source qualification interrupted by ' + signal + '.'),
  );
const int = () => interrupt('SIGINT');
const term = () => interrupt('SIGTERM');
process.on('SIGINT', int);
process.on('SIGTERM', term);
let report = { passed: false },
  raw,
  successful = false,
  cleanupConfirmed = false;
let failure,
  cleanupFailure,
  volumeDispatched = false,
  containerDispatched = false;
try {
  assert.equal(
    inspectSourceContainer(selected, owner.container),
    null,
    'The source container name is already occupied.',
  );
  assert.equal(
    inspectSourceVolume(selected, owner.volume),
    null,
    'The source volume name is already occupied.',
  );
  interrupted.signal.throwIfAborted();
  await saveSourceEngineProof(selected, { phase: 'volume-create-dispatched' });
  volumeDispatched = true;
  assert.equal(
    docker(['volume', 'create', ...labelArgs, owner.volume]),
    owner.volume,
  );
  const volume = inspectSourceVolume(selected, owner.volume);
  assert.equal(volume?.Name, owner.volume);
  assert.equal(volume.Driver, 'local');
  for (const [key, value] of Object.entries(owner.labels))
    assert.equal(
      volume.Labels?.[key],
      value,
      'Source volume ownership changed.',
    );
  requireSourceEngine(selected, owner.engineID);
  interrupted.signal.throwIfAborted();
  await saveSourceEngineProof(selected, { phase: 'probe-dispatched' });
  containerDispatched = true;
  const child = spawn(
    selected.binary,
    [
      ...selected.args,
      'run',
      '--rm',
      '-i',
      '--name',
      name,
      ...labelArgs,
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
      'type=bind,source=' + volumes + ',target=/var/lib/docker/volumes',
      '--mount',
      'type=bind,source=' + root + ',target=/camp,readonly',
      installed.image,
      '/camp/qualify-linux.mjs',
    ],
    { env: selected.environment, stdio: ['pipe', 'pipe', 'pipe'] },
  );
  const result = waitForSourceProbe(child, { signal: interrupted.signal });
  child.stdin.end(
    JSON.stringify({
      home: path.posix.join(volumes, owner.volume, '_data'),
      image: installed.image,
      project: name,
      prefix: name + '-',
    }),
  );
  raw = await result;
  const observed = JSON.parse(raw.stdout);
  assert.equal(raw.code, 0, 'The ARM64 probe did not exit successfully.');
  assert.equal(raw.signal, null, 'The ARM64 probe was interrupted.');
  assert.equal(observed.passed, true, 'The ARM64 probe did not pass.');
  assert.deepEqual(
    await sourceInputs(),
    inputs,
    'Qualification source changed during the probe.',
  );
  // Keep resource names and detailed failures in the private intent only.
  report = {
    ...observed,
    store: observed.store
      ? {
          filesystemMagic: observed.store.filesystemMagic,
          profile: observed.store.profile,
          crashDurable: observed.store.crashDurable,
        }
      : undefined,
  };
  delete report.error;
  delete report.cleanupErrors;
  successful = true;
} catch (error) {
  failure = error.message;
  raw ??= error.probeOutput;
  report.passed = false;
  report.error = {
    code: 'QualificationFailed',
    message:
      'The ARM64 probe failed; inspect the retained private source-engine proof.',
  };
} finally {
  try {
    await saveSourceEngineProof(selected, {
      phase: 'cleanup',
      probe: raw,
      failure,
    });
    if (containerDispatched) stopSourceContainer(selected, owner);
    if (volumeDispatched) removeSourceVolume(selected, owner);
    cleanupConfirmed = true;
  } catch (error) {
    cleanupFailure = error.message;
    report.homeCleanupError =
      'Owned source resources could not be confirmed absent; inspect the retained private intent.';
  }
  report.passed = successful && cleanupConfirmed && !interrupted.signal.aborted;
  Object.assign(report, {
    sourceCommit: installed.commit,
    images: {
      rust: installed.rustImage,
      node: installed.nodeImage,
      docker: installed.dockerImage,
    },
    hostPlatform: process.platform + '/' + process.arch,
    engine: {
      kernel: info.KernelVersion,
      architecture: info.Architecture,
      storageDriver: info.Driver,
    },
    sourceInputs: inputs,
    cleanupConfirmed,
  });
  try {
    await saveSourceEngineProof(selected, {
      phase: report.passed
        ? 'passed'
        : cleanupConfirmed
          ? 'failed'
          : 'cleanup-unknown',
      probe: raw,
      failure,
      cleanupFailure,
      reportPassed: report.passed,
    });
    await output.writeFile(JSON.stringify(report, null, 2) + '\n');
    await output.sync();
  } finally {
    await output.close();
    await chmod(destination, 0o600);
    process.removeListener('SIGINT', int);
    process.removeListener('SIGTERM', term);
  }
}
if (!report.passed) process.exitCode = 1;
process.stdout.write(
  'ARM64 qualification ' +
    (report.passed ? 'passed' : 'failed') +
    ': ' +
    destination +
    '\n',
);
