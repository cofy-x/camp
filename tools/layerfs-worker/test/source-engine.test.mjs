import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import process from 'node:process';
import { EventEmitter } from 'node:events';
import { execFile } from 'node:child_process';
import {
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  realpath,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PassThrough } from 'node:stream';
import { test } from 'node:test';
import { promisify } from 'node:util';
import { fileURLToPath, URL } from 'node:url';
import {
  inspectSourceContainer,
  inspectSourceVolume,
  removeSourceVolume,
  selectSourceEngine,
  sourceDocker,
  sourceEngineArguments,
  stopSourceContainer,
  waitForSourceProbe,
} from '../source-engine.mjs';

async function fixture(t) {
  const root = await realpath(
    await mkdtemp(path.join(tmpdir(), 'camp-source-engine-test-')),
  );
  t.after(() => rm(root, { recursive: true, force: true }));
  const binary = path.join(root, 'binary');
  await writeFile(binary, '#!/bin/sh\nexit 0\n', { mode: 0o700 });
  const bin = path.join(root, 'alias directory');
  await mkdir(bin);
  const alias = path.join(bin, 'docker');
  await symlink(binary, alias);
  const socket = path.join(root, 'engine.sock');
  const server = createServer();
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(socket, resolve);
  });
  t.after(() => new Promise((resolve) => server.close(resolve)));
  return { root, binary, alias, socket, bin };
}

const ok = (value) => ({
  status: 0,
  stdout: typeof value === 'string' ? value : JSON.stringify(value),
  stderr: '',
});
const missing = (name) => ({
  status: 1,
  stdout: '',
  stderr: 'Error: No such object: ' + name + '\n',
});

test('source CLI arguments preserve source/output forms and require one explicit engine pair', () => {
  assert.deepEqual(
    sourceEngineArguments(['--source', '/sdk'], { source: true }),
    { source: '/sdk' },
  );
  assert.deepEqual(
    sourceEngineArguments(
      [
        'report.json',
        '--docker-binary',
        '/bin/docker',
        '--docker-socket',
        '/engine.sock',
      ],
      { output: true },
    ),
    {
      output: 'report.json',
      dockerBinary: '/bin/docker',
      dockerSocket: '/engine.sock',
    },
  );
  for (const args of [
    ['--docker-binary', '/docker'],
    ['--docker-socket', '/socket'],
    ['--unknown', 'value'],
    ['toString', 'x'],
    ['--docker-binary', '/a', '--docker-binary', '/b'],
    ['a.json', 'b.json'],
  ])
    assert.throws(() => sourceEngineArguments(args, { output: true }));
});

test('explicit selection preserves executable alias and never reads global context or sensitive environment', async (t) => {
  const f = await fixture(t),
    calls = [];
  const engine = await selectSourceEngine(
    { dockerBinary: f.alias, dockerSocket: f.socket },
    {
      temporaryParent: f.root,
      environment: {
        PATH: f.bin,
        HOME: f.root,
        LANG: 'C',
        DOCKER_HOST: 'tcp://foreign:2376',
        DOCKER_CONTEXT: 'foreign',
        DOCKER_CONFIG: '/must-not-open',
        DOCKER_TLS_VERIFY: '0',
        DOCKER_CERT_PATH: '/credentials',
        DEEPSEEK_API_KEY: 'must-not-forward',
        GITHUB_TOKEN: 'must-not-forward',
        NODE_OPTIONS: '--require /must-not-load',
        HTTP_PROXY: 'http://proxy.invalid',
      },
      run: (binary, args, options) => {
        calls.push({ binary, args, options });
        return ok({ ID: 'engine' });
      },
    },
  );
  assert.equal(calls.length, 0);
  assert.equal(engine.binary, f.alias);
  assert.equal(engine.proof.resolvedBinary, f.binary);
  assert.equal(engine.environment.DOCKER_HOST, 'unix://' + f.socket);
  assert.equal(engine.environment.HTTP_PROXY, 'http://proxy.invalid');
  assert.notEqual(engine.environment.DOCKER_CONFIG, '/must-not-open');
  assert.deepEqual(
    JSON.parse(
      await readFile(
        path.join(engine.environment.DOCKER_CONFIG, 'config.json'),
        'utf8',
      ),
    ),
    { auths: {} },
  );
  for (const key of [
    'DOCKER_CONTEXT',
    'DOCKER_TLS_VERIFY',
    'DOCKER_CERT_PATH',
    'DEEPSEEK_API_KEY',
    'GITHUB_TOKEN',
    'NODE_OPTIONS',
  ])
    assert.equal(engine.environment[key], undefined);
  sourceDocker(engine, ['info'], { json: true });
  assert.deepEqual(calls[0].args, ['--host', 'unix://' + f.socket, 'info']);
  assert.equal(calls[0].binary, f.alias);
  assert.equal(calls[0].options.timeout, 30_000);
  assert.equal(calls[0].options.killSignal, 'SIGKILL');
  assert.ok(
    !(
      await readFile(path.join(engine.directory, 'engine.json'), 'utf8')
    ).includes('must-not-'),
  );
});

test('an adjacent Docker buildx executable is retained without reading global client state', async (t) => {
  const f = await fixture(t);
  const buildx = path.join(f.root, 'docker-buildx');
  await symlink(f.binary, buildx);
  const engine = await selectSourceEngine(
    { dockerBinary: f.alias, dockerSocket: f.socket },
    {
      temporaryParent: f.root,
      environment: {
        PATH: f.bin,
        HOME: '/must-not-open',
        DOCKER_CONFIG: '/must-not-open',
      },
      run: () =>
        assert.fail('Explicit selection must not inspect any context.'),
    },
  );
  const config = JSON.parse(
    await readFile(
      path.join(engine.environment.DOCKER_CONFIG, 'config.json'),
      'utf8',
    ),
  );
  assert.deepEqual(config, { auths: {}, cliPluginsExtraDirs: [f.root] });
  assert.equal(engine.proof.buildx.invocation, buildx);
  assert.equal(engine.proof.buildx.sha256, engine.proof.binarySHA256);
});

test('invalid explicit endpoints and executables fail without context fallback or a private engine operation', async (t) => {
  const f = await fixture(t);
  for (const options of [
    { dockerBinary: 'docker', dockerSocket: f.socket },
    { dockerBinary: f.alias, dockerSocket: 'tcp://foreign' },
    { dockerBinary: path.join(f.root, 'missing'), dockerSocket: f.socket },
    { dockerBinary: f.alias, dockerSocket: f.root },
  ])
    await assert.rejects(
      selectSourceEngine(options, {
        temporaryParent: f.root,
        environment: { PATH: f.bin },
        run: () =>
          assert.fail('Invalid explicit selection cannot inspect a context.'),
      }),
    );
  assert.equal(
    (await readdir(f.root)).some((name) =>
      name.startsWith('camp-worker-source-'),
    ),
    false,
  );
});

test('default callers honor a local environment endpoint or resolve their existing context once', async (t) => {
  const f = await fixture(t),
    calls = [];
  const run = (binary, args, options) => {
    calls.push({ args, options });
    return ok('unix://' + f.socket);
  };
  const direct = await selectSourceEngine(
    {},
    {
      temporaryParent: f.root,
      environment: {
        PATH: f.bin,
        HOME: f.root,
        DOCKER_HOST: 'unix://' + f.socket,
      },
      run,
    },
  );
  assert.equal(calls.length, 0);
  assert.equal(direct.binary, f.alias);
  const selected = await selectSourceEngine(
    {},
    {
      temporaryParent: f.root,
      environment: {
        PATH: f.bin,
        HOME: f.root,
        DOCKER_CONTEXT: 'selected',
        DEEPSEEK_API_KEY: 'must-not-forward',
      },
      run,
    },
  );
  assert.deepEqual(calls[0].args, [
    'context',
    'inspect',
    '--format',
    '{{.Endpoints.docker.Host}}',
  ]);
  assert.equal(calls[0].options.env.DOCKER_CONTEXT, 'selected');
  assert.equal(calls[0].options.timeout, 10_000);
  assert.equal(calls[0].options.killSignal, 'SIGKILL');
  assert.equal(calls[0].options.env.DEEPSEEK_API_KEY, undefined);
  assert.equal(selected.environment.DOCKER_CONTEXT, undefined);
  assert.deepEqual(selected.args, ['--host', 'unix://' + f.socket]);
  await assert.rejects(
    selectSourceEngine(
      {},
      {
        temporaryParent: f.root,
        environment: { PATH: f.bin, DOCKER_HOST: 'tcp://foreign:2376' },
        run: () => assert.fail('Remote hosts cannot fall back to a context.'),
      },
    ),
  );
});

function resources({
  foreignLabels = false,
  foreignImage = false,
  foreignEngine = false,
  inUse = false,
  replacedVolume = false,
} = {}) {
  const labels = { 'cofy.camp.scope': 'test', 'cofy.camp.nonce': 'nonce' };
  const owner = {
    engineID: 'engine',
    container: 'probe',
    volume: 'probe-home',
    image: 'sha256:' + 'c'.repeat(64),
    labels,
  };
  let container = {
    Id: 'a'.repeat(64),
    Name: '/probe',
    Image: foreignImage ? 'sha256:' + 'd'.repeat(64) : owner.image,
    Config: { Labels: foreignLabels ? {} : labels },
    State: { Running: true },
  };
  let volume = {
      Name: owner.volume,
      Driver: 'local',
      CreatedAt: 'original',
      Labels: foreignLabels ? {} : labels,
    },
    volumeReads = 0;
  const mutations = [];
  const engine = {
    binary: '/fixed/docker',
    args: ['--host', 'unix:///fixed.sock'],
    environment: {},
    run: (_binary, all) => {
      const args = all.slice(2);
      if (args[0] === 'info')
        return ok({ ID: foreignEngine ? 'foreign' : 'engine' });
      if (args[0] === 'container' && args[1] === 'inspect')
        return container ? ok([container]) : missing(args[2]);
      if (args[0] === 'volume' && args[1] === 'inspect') {
        volumeReads++;
        return volume
          ? ok([
              {
                ...volume,
                CreatedAt:
                  replacedVolume && volumeReads > 1
                    ? 'replacement'
                    : volume.CreatedAt,
              },
            ])
          : {
              status: 1,
              stdout: '',
              stderr:
                'Error response from daemon: get ' +
                args[2] +
                ': no such volume\n',
            };
      }
      if (args[0] === 'container' && args[1] === 'ls')
        return ok(inUse ? 'foreign-container' : '');
      mutations.push(args);
      if (args[0] === 'container' && args[1] === 'stop') {
        assert.equal(args.at(-1), container.Id);
        container.State.Running = false;
        return ok(container.Id);
      }
      if (args[0] === 'container' && args[1] === 'rm') {
        assert.equal(args.at(-1), container.Id);
        container = null;
        return ok('removed');
      }
      if (args[0] === 'volume' && args[1] === 'rm') {
        volume = null;
        return ok('removed');
      }
      assert.fail('Unexpected source engine command: ' + args.join(' '));
    },
  };
  return { engine, owner, mutations };
}

test('volume inspection recognizes the exact daemon absence response and rejects unrelated failures', () => {
  const name = 'probe-home';
  const missingVolume =
    'Error response from daemon: get ' + name + ': no such volume';
  const engine = (result) => ({
    binary: '/fixed/docker',
    args: ['--host', 'unix:///fixed.sock'],
    environment: {},
    run: () => result,
  });
  const response = (stderr, status = 1) => ({ status, stdout: '', stderr });
  for (const result of [missing(name), response(missingVolume + '\n')])
    assert.equal(inspectSourceVolume(engine(result), name), null);
  for (const result of [
    response(missingVolume, 125),
    response(missingVolume.replace(name, name + '-other')),
    response('permission denied: ' + missingVolume),
    response('Cannot connect to the Docker daemon'),
    response(missingVolume + ': transport failed'),
    response('permission denied\n' + missingVolume),
    response(missingVolume + '\ntransport failed'),
  ])
    assert.throws(() => inspectSourceVolume(engine(result), name));
  assert.throws(() =>
    inspectSourceContainer(engine(response(missingVolume)), name),
  );
});

test('source cleanup binds exact container ID, labels, image and engine, and never force removes', () => {
  const f = resources();
  stopSourceContainer(f.engine, f.owner);
  removeSourceVolume(f.engine, f.owner);
  assert.deepEqual(
    f.mutations.map((args) => args.slice(0, 2)),
    [
      ['container', 'stop'],
      ['container', 'rm'],
      ['volume', 'rm'],
    ],
  );
  assert.ok(
    f.mutations.every(
      (args) => !args.includes('--force') && !args.includes('-f'),
    ),
  );
  for (const change of [
    { foreignLabels: true },
    { foreignImage: true },
    { foreignEngine: true },
  ]) {
    const bad = resources(change);
    assert.throws(() => stopSourceContainer(bad.engine, bad.owner));
    assert.equal(bad.mutations.length, 0);
  }
  for (const change of [
    { foreignLabels: true },
    { foreignEngine: true },
    { inUse: true },
    { replacedVolume: true },
  ]) {
    const bad = resources(change);
    assert.throws(() => removeSourceVolume(bad.engine, bad.owner));
    assert.equal(bad.mutations.length, 0);
  }
});

function child({ ignoreTerm = false } = {}) {
  const c = new EventEmitter();
  Object.assign(c, {
    stdout: new PassThrough(),
    stderr: new PassThrough(),
    stdin: new PassThrough(),
    signals: [],
  });
  c.kill = (signal) => {
    c.signals.push(signal);
    if (signal === 'SIGKILL' || !ignoreTerm)
      globalThis.queueMicrotask(() => c.emit('close', null, signal));
    return true;
  };
  return c;
}

test('bounded source probe preserves a successful response and cleans deadline/signal listeners', async () => {
  const c = child();
  const completed = waitForSourceProbe(c, { timeout: 1000 });
  c.stdout.write('{"passed":true}');
  c.emit('close', 0, null);
  assert.deepEqual(await completed, {
    code: 0,
    signal: null,
    stdout: '{"passed":true}',
    stderr: '',
  });
  assert.deepEqual(c.signals, []);
  const timeout = child({ ignoreTerm: true });
  await assert.rejects(
    waitForSourceProbe(timeout, { timeout: 1, stopGrace: 1 }),
    /deadline exceeded/,
  );
  assert.deepEqual(timeout.signals, ['SIGTERM', 'SIGKILL']);
  const canceled = child(),
    controller = new globalThis.AbortController();
  const pending = waitForSourceProbe(canceled, { signal: controller.signal });
  controller.abort(new Error('operator interrupted'));
  await assert.rejects(pending, /operator interrupted/);
  assert.deepEqual(canceled.signals, ['SIGTERM']);
});

test('source probe output bounds and lost child spawns cannot become success', async () => {
  for (const stream of ['stdout', 'stderr']) {
    const c = child();
    const pending = waitForSourceProbe(c);
    c[stream].write(
      Buffer.alloc((stream === 'stdout' ? 8 : 1) * (1 << 20) + 1),
    );
    await assert.rejects(pending, /exceeded/);
    assert.deepEqual(c.signals, ['SIGTERM']);
  }
  const failed = child();
  const pending = waitForSourceProbe(failed);
  failed.emit('error', new Error('spawn failed'));
  await assert.rejects(pending, /spawn failed/);
});

test('worker packaging interrupted during archive hashing retains failure evidence without publishing a descriptor', async (t) => {
  const f = await fixture(t);
  const scripts = path.join(f.root, 'scripts');
  const worker = path.join(f.root, 'tools/layerfs-worker');
  await mkdir(scripts);
  await mkdir(worker, { recursive: true });
  for (const [source, destination] of [
    [
      new URL('../../../scripts/package-worker.mjs', import.meta.url),
      path.join(scripts, 'package-worker.mjs'),
    ],
    [
      new URL('../source-engine.mjs', import.meta.url),
      path.join(worker, 'source-engine.mjs'),
    ],
  ])
    await writeFile(destination, await readFile(source));
  await writeFile(
    path.join(scripts, 'lib.mjs'),
    `
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import path from 'node:path';
export const root = path.dirname(import.meta.dirname);
export const command = (binary, args, options) => promisify(execFile)(binary, args, { ...options, timeout: 5000 });
`,
  );
  const image = 'sha256:' + 'c'.repeat(64);
  const commit = 'd'.repeat(40);
  await writeFile(
    path.join(worker, 'installed.json'),
    JSON.stringify({
      platform: 'linux/arm64',
      image,
      commit,
      version: '1.1.0',
    }),
  );
  await writeFile(
    f.binary,
    `#!${process.execPath}
import { writeFileSync } from 'node:fs';
const args = process.argv.slice(2).slice(2);
const image = ${JSON.stringify(image)};
if (args[0] === 'info') process.stdout.write(JSON.stringify({ ID: 'engine', Architecture: 'arm64' }));
else if (args[0] === 'image' && args[1] === 'inspect') process.stdout.write(JSON.stringify([{ Id: image, Architecture: 'arm64', Os: 'linux', Config: { Labels: { 'camp.layerfs.commit': ${JSON.stringify(commit)}, 'camp.runtime': 'layerfs-worker' } } }]));
else if (args[0] === 'container' && args[1] === 'inspect') { process.stderr.write('Error: No such object: ' + args[2]); process.exitCode = 1; }
else if (args[0] === 'run') process.stdout.write(JSON.stringify({ status: 'sealed', architecture: 'arm64', files: {}, build_id: 'e'.repeat(64) }));
else if (args[0] === 'image' && args[1] === 'save') writeFileSync(args[args.indexOf('--output') + 1], 'fixture archive');
else throw new Error('Unexpected fixture Docker command');
`,
  );
  await writeFile(
    path.join(f.bin, 'git'),
    `#!${process.execPath}\nprocess.stdout.write('${commit}\\n');\n`,
    { mode: 0o700 },
  );
  const preload = path.join(f.root, 'interrupt-hash.mjs');
  await writeFile(
    preload,
    `
import fs from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
Object.defineProperty(process, 'arch', { value: 'arm64' });
const original = fs.createReadStream;
fs.createReadStream = function(file, ...args) {
  const stream = original.call(this, file, ...args);
  if (String(file).endsWith('.tar.gz')) stream.once('data', () => process.emit('SIGTERM'));
  return stream;
};
syncBuiltinESMExports();
`,
  );
  await assert.rejects(
    promisify(execFile)(
      process.execPath,
      [
        '--import',
        preload,
        path.join(scripts, 'package-worker.mjs'),
        '--docker-binary',
        f.alias,
        '--docker-socket',
        f.socket,
      ],
      {
        env: {
          PATH: f.bin + path.delimiter + process.env.PATH,
          HOME: f.root,
          TMPDIR: f.root,
        },
        timeout: 10_000,
      },
    ),
    (error) => /Worker packaging interrupted by SIGTERM/.test(error.stderr),
  );
  const artifacts = await readdir(path.join(f.root, 'dist'));
  assert.ok(artifacts.some((name) => name.endsWith('.tar.gz')));
  assert.ok(artifacts.every((name) => name.startsWith('.worker-')));
  const proofName = (await readdir(f.root)).find((name) =>
    name.startsWith('camp-worker-source-'),
  );
  const proof = JSON.parse(
    await readFile(path.join(f.root, proofName, 'engine.json'), 'utf8'),
  );
  assert.equal(proof.phase, 'failed-or-cleanup-unknown');
  assert.equal(proof.error, 'Worker packaging interrupted by SIGTERM.');
});

test('all three source commands reject incomplete explicit selection before any engine work', async () => {
  for (const filename of [
    '../install.mjs',
    '../qualify.mjs',
    '../../../scripts/package-worker.mjs',
  ])
    await assert.rejects(
      promisify(execFile)(
        process.execPath,
        [
          fileURLToPath(new URL(filename, import.meta.url)),
          '--docker-binary',
          '/unused/docker',
        ],
        { env: {}, timeout: 5000 },
      ),
      (error) =>
        /Use --docker-binary and --docker-socket together/.test(error.stderr),
    );
});
