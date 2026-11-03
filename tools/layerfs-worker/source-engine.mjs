import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { spawnSync } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { constants, createReadStream } from 'node:fs';
import {
  access,
  chmod,
  lstat,
  mkdir,
  mkdtemp,
  open,
  realpath,
  rename,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { URL } from 'node:url';
import { setTimeout, clearTimeout } from 'node:timers';

const environmentNames = [
  'PATH',
  'HOME',
  'LANG',
  'LC_ALL',
  'TMPDIR',
  'HTTP_PROXY',
  'HTTPS_PROXY',
  'NO_PROXY',
  'http_proxy',
  'https_proxy',
  'no_proxy',
];

export function sourceEngineArguments(
  args,
  { source = false, output = false } = {},
) {
  const options = {};
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (output && !arg.startsWith('--')) {
      assert.equal(
        options.output,
        undefined,
        'Select one qualification output.',
      );
      options.output = arg;
      continue;
    }
    const names = {
      '--docker-binary': 'dockerBinary',
      '--docker-socket': 'dockerSocket',
      ...(source ? { '--source': 'source' } : {}),
    };
    const key = Object.hasOwn(names, arg) ? names[arg] : undefined;
    assert.ok(
      key && args[i + 1] && !args[i + 1].startsWith('--'),
      'Unknown or incomplete source-engine option: ' + arg,
    );
    assert.equal(options[key], undefined, 'Do not repeat ' + arg + '.');
    options[key] = args[++i];
  }
  assert.equal(
    Boolean(options.dockerBinary),
    Boolean(options.dockerSocket),
    'Use --docker-binary and --docker-socket together.',
  );
  return options;
}

function localEndpoint(value) {
  const url = new URL(value);
  assert.ok(
    value.startsWith('unix://') &&
      url.protocol === 'unix:' &&
      !url.host &&
      !url.search &&
      !url.hash,
    'Select a local Unix Docker socket.',
  );
  return decodeURIComponent(url.pathname);
}

async function executable(name, environment) {
  const candidates = path.isAbsolute(name)
    ? [path.normalize(name)]
    : (environment.PATH ?? '')
        .split(path.delimiter)
        .filter(Boolean)
        .map((directory) => path.resolve(directory, name));
  for (const candidate of candidates) {
    try {
      const resolved = await realpath(candidate);
      assert.ok(
        (await lstat(resolved)).isFile(),
        'Docker must be a regular executable.',
      );
      await access(candidate, constants.X_OK);
      return { invocation: candidate, resolved };
    } catch (error) {
      if (path.isAbsolute(name)) throw error;
    }
  }
  throw new Error('Docker is unavailable on the selected PATH.');
}

async function fileSHA256(file) {
  const digest = createHash('sha256');
  let bytes = 0;
  for await (const chunk of createReadStream(file)) {
    bytes += chunk.length;
    assert.ok(
      bytes <= 512 << 20,
      'The selected Docker executable exceeds its bound.',
    );
    digest.update(chunk);
  }
  return digest.digest('hex');
}

// This source-only selector is not shipped in a Runner runtime or image.
export async function selectSourceEngine(
  options,
  {
    environment = process.env,
    run = spawnSync,
    temporaryParent = tmpdir(),
  } = {},
) {
  const explicit = Boolean(options.dockerBinary || options.dockerSocket);
  assert.equal(
    Boolean(options.dockerBinary),
    Boolean(options.dockerSocket),
    'Use --docker-binary and --docker-socket together.',
  );
  if (explicit) {
    assert.ok(
      path.isAbsolute(options.dockerBinary),
      '--docker-binary must be absolute.',
    );
    assert.ok(
      path.isAbsolute(options.dockerSocket),
      '--docker-socket must be absolute.',
    );
  }
  const selected = await executable(
    options.dockerBinary ?? 'docker',
    environment,
  );
  const env = Object.fromEntries(
    environmentNames
      .filter((name) => environment[name] !== undefined)
      .map((name) => [name, environment[name]]),
  );
  let socket = options.dockerSocket;
  if (!explicit) {
    if (environment.DOCKER_HOST && !environment.DOCKER_CONTEXT)
      socket = localEndpoint(environment.DOCKER_HOST);
    else {
      const lookup = { ...env };
      for (const name of ['DOCKER_CONFIG', 'DOCKER_CONTEXT'])
        if (environment[name]) lookup[name] = environment[name];
      const result = run(
        selected.invocation,
        ['context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'],
        {
          env: lookup,
          encoding: 'utf8',
          maxBuffer: 1 << 20,
          timeout: 10_000,
          killSignal: 'SIGKILL',
        },
      );
      assert.ok(
        !result.error && result.status === 0,
        'Could not inspect the selected local Docker context.',
      );
      socket = localEndpoint(result.stdout.trim());
    }
  }
  socket = await realpath(socket);
  assert.ok(
    (await lstat(socket)).isSocket(),
    'The selected Docker endpoint is not a Unix socket.',
  );
  const directory = await mkdtemp(
    path.join(temporaryParent, 'camp-worker-source-'),
  );
  await chmod(directory, 0o700);
  if (explicit) {
    // A fixed endpoint must not consult the operator's context or builder state.
    env.DOCKER_CONFIG = path.join(directory, 'docker-client');
    await mkdir(env.DOCKER_CONFIG, { mode: 0o700 });
    const config = { auths: {} };
    const buildx = path.join(path.dirname(selected.resolved), 'docker-buildx');
    try {
      await executable(buildx, env);
      config.cliPluginsExtraDirs = [path.dirname(selected.resolved)];
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    await writeFile(
      path.join(env.DOCKER_CONFIG, 'config.json'),
      JSON.stringify(config) + '\n',
      { mode: 0o600, flag: 'wx' },
    );
  } else if (environment.DOCKER_CONFIG)
    env.DOCKER_CONFIG = environment.DOCKER_CONFIG;
  env.DOCKER_HOST = 'unix://' + socket;
  const engine = {
    binary: selected.invocation,
    environment: env,
    args: ['--host', env.DOCKER_HOST],
    directory,
    run,
    proof: {
      schema: 1,
      selection: explicit ? 'explicit' : 'ambient-local',
      dockerBinary: selected.invocation,
      resolvedBinary: selected.resolved,
      binarySHA256: await fileSHA256(selected.resolved),
      socket,
      environmentNames: Object.keys(env).sort(),
      createdAt: new Date().toISOString(),
    },
  };
  if (explicit) {
    const buildx = path.join(path.dirname(selected.resolved), 'docker-buildx');
    try {
      const plugin = await executable(buildx, env);
      engine.proof.buildx = {
        invocation: plugin.invocation,
        resolved: plugin.resolved,
        sha256: await fileSHA256(plugin.resolved),
      };
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
  }
  await saveSourceEngineProof(engine, { phase: 'selected' });
  return engine;
}

export function sourceDocker(
  engine,
  args,
  { json = false, timeout = 30_000, maxBuffer = 8 << 20 } = {},
) {
  const result = engine.run(engine.binary, [...engine.args, ...args], {
    env: engine.environment,
    encoding: 'utf8',
    timeout,
    maxBuffer,
    killSignal: 'SIGKILL',
  });
  if (result.error || result.status !== 0) {
    const error = new Error(
      result.error?.message ?? String(result.stderr).slice(0, 8192),
    );
    error.status = result.status;
    error.stderr = String(result.stderr);
    throw error;
  }
  return json ? JSON.parse(result.stdout) : result.stdout.trim();
}

export async function saveSourceEngineProof(engine, update) {
  engine.proof = {
    ...engine.proof,
    ...update,
    updatedAt: new Date().toISOString(),
  };
  const temporary = path.join(engine.directory, '.' + randomUUID());
  const handle = await open(temporary, 'wx', 0o600);
  try {
    await handle.writeFile(JSON.stringify(engine.proof, null, 2) + '\n');
    await handle.sync();
  } finally {
    await handle.close();
  }
  await rename(temporary, path.join(engine.directory, 'engine.json'));
  const directory = await open(engine.directory, 'r');
  try {
    await directory.sync();
  } finally {
    await directory.close();
  }
}

export function requireSourceEngine(engine, expected) {
  const info = sourceDocker(engine, ['info', '--format', '{{json .}}'], {
    json: true,
    timeout: 10_000,
  });
  assert.ok(
    typeof info.ID === 'string' && info.ID.length > 0,
    'The selected engine has no identity.',
  );
  if (expected)
    assert.equal(
      info.ID,
      expected,
      'The selected Docker engine changed; retain the private intent.',
    );
  return info;
}

function absent(error, name, resource) {
  return (
    error.status === 1 &&
    (error.stderr
      .split('\n')
      .some(
        (line) =>
          line.includes('No such ') && line.trim().endsWith(': ' + name),
      ) ||
      (resource === 'volume' &&
        error.stderr.trim() ===
          'Error response from daemon: get ' + name + ': no such volume'))
  );
}

export function inspectSourceContainer(engine, name) {
  try {
    return sourceDocker(engine, ['container', 'inspect', name], {
      json: true,
      timeout: 10_000,
    })[0];
  } catch (error) {
    if (absent(error, name)) return null;
    throw error;
  }
}

export function inspectSourceVolume(engine, name) {
  try {
    return sourceDocker(engine, ['volume', 'inspect', name], {
      json: true,
      timeout: 10_000,
    })[0];
  } catch (error) {
    if (absent(error, name, 'volume')) return null;
    throw error;
  }
}

function owned(resource, owner) {
  const labels = resource?.Config?.Labels ?? resource?.Labels;
  assert.ok(
    labels &&
      Object.entries(owner.labels).every(
        ([key, value]) => labels[key] === value,
      ),
    'Source resource ownership changed; retain the private intent.',
  );
}

export function stopSourceContainer(engine, owner) {
  requireSourceEngine(engine, owner.engineID);
  const container = inspectSourceContainer(engine, owner.container);
  if (!container) return;
  owned(container, owner);
  assert.equal(
    container.Name,
    '/' + owner.container,
    'Source container name changed.',
  );
  assert.equal(container.Image, owner.image, 'Source container image changed.');
  assert.match(container.Id, /^[a-f0-9]{64}$/);
  if (container.State?.Running)
    sourceDocker(engine, ['container', 'stop', '--time', '5', container.Id], {
      timeout: 15_000,
    });
  requireSourceEngine(engine, owner.engineID);
  const stopped = inspectSourceContainer(engine, container.Id);
  if (!stopped) return;
  owned(stopped, owner);
  assert.equal(stopped.Id, container.Id);
  assert.equal(stopped.Name, '/' + owner.container);
  assert.equal(stopped.Image, owner.image);
  assert.equal(
    stopped.State?.Running,
    false,
    'The owned source container has not stopped.',
  );
  sourceDocker(engine, ['container', 'rm', container.Id], { timeout: 10_000 });
  assert.equal(
    inspectSourceContainer(engine, container.Id),
    null,
    'Source container removal was not confirmed.',
  );
}

export function removeSourceVolume(engine, owner) {
  requireSourceEngine(engine, owner.engineID);
  const volume = inspectSourceVolume(engine, owner.volume);
  if (!volume) return;
  owned(volume, owner);
  assert.equal(volume.Name, owner.volume);
  assert.equal(volume.Driver, 'local');
  const users = sourceDocker(
    engine,
    [
      'container',
      'ls',
      '--all',
      '--quiet',
      '--filter',
      'volume=' + owner.volume,
    ],
    { timeout: 10_000, maxBuffer: 64 << 10 },
  );
  assert.equal(users, '', 'The owned source volume is still in use.');
  requireSourceEngine(engine, owner.engineID);
  const current = inspectSourceVolume(engine, owner.volume);
  if (!current) return;
  owned(current, owner);
  assert.equal(current.Name, owner.volume);
  assert.equal(current.Driver, 'local');
  assert.equal(current.CreatedAt, volume.CreatedAt);
  sourceDocker(engine, ['volume', 'rm', owner.volume], { timeout: 10_000 });
  assert.equal(
    inspectSourceVolume(engine, owner.volume),
    null,
    'Source volume removal was not confirmed.',
  );
}

export function waitForSourceProbe(
  child,
  { signal, timeout = 300_000, stopGrace = 2_000 } = {},
) {
  return new Promise((resolve, reject) => {
    const output = [],
      errors = [];
    let outputBytes = 0,
      errorBytes = 0,
      failure,
      finished = false,
      killTimer;
    const stop = (error) => {
      if (finished || failure) return;
      failure = new Error(
        error instanceof Error ? error.message : String(error),
        { cause: error },
      );
      child.kill('SIGTERM');
      killTimer ??= setTimeout(() => {
        child.kill('SIGKILL');
        finish();
      }, stopGrace);
    };
    const abort = () =>
      stop(signal.reason ?? new Error('Source qualification interrupted.'));
    const timer = setTimeout(
      () => stop(new Error('Source qualification deadline exceeded.')),
      timeout,
    );
    const finish = (code, exitSignal) => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      clearTimeout(killTimer);
      signal?.removeEventListener('abort', abort);
      if (failure) {
        failure.probeOutput = {
          code,
          signal: exitSignal,
          stdout: Buffer.concat(output).toString('utf8'),
          stderr: Buffer.concat(errors).toString('utf8'),
        };
        reject(failure);
      } else
        resolve({
          code,
          signal: exitSignal,
          stdout: Buffer.concat(output).toString('utf8'),
          stderr: Buffer.concat(errors).toString('utf8'),
        });
    };
    child.stdout.on('data', (chunk) => {
      outputBytes += chunk.length;
      if (outputBytes > 8 << 20)
        stop(new Error('Source qualification output exceeded its bound.'));
      else output.push(chunk);
    });
    child.stderr.on('data', (chunk) => {
      errorBytes += chunk.length;
      if (errorBytes > 1 << 20)
        stop(
          new Error('Source qualification diagnostics exceeded their bound.'),
        );
      else errors.push(chunk);
    });
    child.once('error', (error) => {
      failure ??= error;
      finish();
    });
    child.once('close', finish);
    child.stdin.on('error', (error) => stop(error));
    signal?.addEventListener('abort', abort, { once: true });
    if (signal?.aborted) abort();
  });
}
