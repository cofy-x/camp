import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  realpath,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { createServer } from 'node:net';
import { promisify } from 'node:util';
import { join } from 'node:path';
import { test } from 'node:test';
import { root } from './lib.mjs';
import { homebrewFormula, verifyPublishedFormula } from './homebrew.mjs';
import { setupPhases } from './setup-progress.mjs';
import {
  hashFile,
  mergeRunnerReleases,
  serverPlatforms,
  runnerPlatforms,
  validVersion,
  verifyRelease,
  verifyRunnerInstallationReport,
} from './release.mjs';

test('setup phase evidence retains durations without private diagnostics', () => {
  assert.deepEqual(
    setupPhases(
      [
        'Runtime download…',
        'Runtime download: working; 12.0 MiB / 90.0 MiB; elapsed 2s',
        'Runtime download: complete; 90.0 MiB / 90.0 MiB; elapsed 1m2.345s',
        'Validating fixed runtime files: complete; 90.0 MiB processed; 123 entries processed; elapsed 12ms',
        'Runner self-test: complete; elapsed 0s',
        'private/signed/url?credential=secret: complete; elapsed 1s',
        'Runtime download: failed; elapsed private-credential',
        'Verifying runtime archive: cancelled; elapsed 15ms',
      ].join('\n'),
    ),
    [
      { phase: 'Runtime download', status: 'complete', elapsedMs: 62345 },
      {
        phase: 'Validating fixed runtime files',
        status: 'complete',
        elapsedMs: 12,
      },
      { phase: 'Runner self-test', status: 'complete', elapsedMs: 0 },
      {
        phase: 'Verifying runtime archive',
        status: 'cancelled',
        elapsedMs: 15,
      },
    ],
  );
});

test('installation reports bind each entrypoint to its archive kind and source', () => {
  const descriptor = {
    version: 'v0.1.0-alpha.3',
    sourceCommit: 'a'.repeat(40),
    runners: {
      'darwin-arm64': {
        installer: { sha256: '1'.repeat(64) },
        offline: { sha256: '2'.repeat(64) },
      },
    },
  };
  for (const scenario of ['online', 'offline', 'server-entrypoint']) {
    const report = {
      status: 'passed',
      platform: 'darwin',
      architecture: 'arm64',
      version: descriptor.version,
      sourceCommit: descriptor.sourceCommit,
      mode: scenario === 'offline' ? 'offline' : 'online',
      entrypoint:
        scenario === 'server-entrypoint'
          ? 'provided-binary'
          : 'runner-installer',
      entrypointBinarySHA256: '3'.repeat(64),
      archiveSHA256: (scenario === 'offline' ? '2' : '1').repeat(64),
    };
    verifyRunnerInstallationReport(
      report,
      descriptor,
      'darwin-arm64',
      scenario,
    );
    for (const changed of [
      { archiveSHA256: (scenario === 'offline' ? '1' : '2').repeat(64) },
      { sourceCommit: 'b'.repeat(40) },
      { mode: scenario === 'offline' ? 'online' : 'offline' },
      {
        entrypoint:
          scenario === 'server-entrypoint'
            ? 'runner-installer'
            : 'provided-binary',
      },
      { entrypointBinarySHA256: '' },
    ])
      assert.throws(() =>
        verifyRunnerInstallationReport(
          { ...report, ...changed },
          descriptor,
          'darwin-arm64',
          scenario,
        ),
      );
  }
});

test('historical Homebrew formulas use their published bytes and unique checksum', () => {
  const legacy =
    'class Camp < Formula\n  # Previous release instructions\nend\n';
  const checksum = `${createHash('sha256').update(legacy).digest('hex')}  camp.rb\n`;
  verifyPublishedFormula(legacy, legacy, checksum);
  assert.throws(() =>
    verifyPublishedFormula(`${legacy}# changed\n`, legacy, checksum),
  );
  assert.throws(() =>
    verifyPublishedFormula(legacy, `${legacy}# changed\n`, checksum),
  );
  assert.throws(() =>
    verifyPublishedFormula(legacy, legacy, `${checksum}${checksum}`),
  );
  assert.throws(() => verifyPublishedFormula(legacy, legacy, ''));
  assert.throws(() =>
    verifyPublishedFormula(legacy, legacy, `${'0'.repeat(64)}  camp.rb\n`),
  );
});

test('release CI resolves only an actual local Docker Unix socket', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'camp-docker-socket-'));
  const server = createServer();
  const socket = join(directory, "engine's socket.sock");
  const output = join(directory, 'output');
  const execute = promisify(execFile);
  const run = (endpoint) =>
    execute(
      process.execPath,
      [join(root, 'scripts/release-docker-socket.mjs')],
      { env: { ...process.env, DOCKER_HOST: endpoint, GITHUB_OUTPUT: output } },
    );
  try {
    await new Promise((resolve, reject) => {
      server.once('error', reject);
      server.listen(socket, resolve);
    });
    await run(`unix://${socket}`);
    assert.equal(await readFile(output, 'utf8'), `path=${socket}\n`);
    for (const endpoint of [
      'tcp://127.0.0.1:2375',
      'unix://remote.invalid/socket',
      `unix://${socket}?redirect=1`,
      `unix://${output}`,
      'unix:///missing-camp-engine.sock',
    ])
      await assert.rejects(run(endpoint));
  } finally {
    await new Promise((resolve) => server.close(resolve));
    await rm(directory, { recursive: true, force: true });
  }
});

test('release transfer rejects changed archives, checksums, identities and unsafe names', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'camp-release-test-'));
  try {
    const version = 'v0.1.0-alpha.1';
    const sourceCommit = 'a'.repeat(40);
    const protocol = await readFile(
      join(root, 'apps/server/internal/runnerprotocol/protocol.go'),
      'utf8',
    );
    const protocolVersion = Number(protocol.match(/const Version = (\d+)/)[1]);
    const make = async (name) => {
      const payload = Buffer.from(`archive:${name}`);
      let filename = `${name}.tar.gz`;
      await writeFile(join(directory, filename), payload);
      const sha256 = await hashFile(join(directory, filename));
      if (name.startsWith('camp-runtime-')) {
        filename = `${name}-${sha256}.tar.gz`;
        await writeFile(join(directory, filename), payload);
      }
      await writeFile(
        join(directory, `${filename}.sha256`),
        `${sha256}  ${filename}\n`,
      );
      return {
        filename,
        sha256,
        bytes: payload.length,
        unpackedBytes: payload.length + 10,
      };
    };
    const runner = {
      schema: 2,
      version,
      sourceCommit,
      protocolVersion,
      runners: Object.fromEntries(
        await Promise.all(
          runnerPlatforms.map(async (platform) => [
            platform,
            {
              installer: await make(`camp-runner-${platform}`),
              runtime: {
                ...(await make(`camp-runtime-${platform}`)),
                manifestSHA256: 'a'.repeat(64),
              },
              offline: await make(`camp-runner-offline-${platform}`),
            },
          ]),
        ),
      ),
    };
    const server = {
      schema: 1,
      version,
      sourceCommit,
      server: await make('camp-server-linux-amd64'),
    };
    server.servers = Object.fromEntries(
      await Promise.all(
        serverPlatforms.map(async (platform) => [
          platform,
          platform === 'linux-amd64'
            ? server.server
            : await make(`camp-server-${platform}`),
        ]),
      ),
    );
    const descriptors = async () => {
      await writeFile(
        join(directory, 'runner-release.json'),
        JSON.stringify(runner),
      );
      await writeFile(
        join(directory, 'server-release.json'),
        JSON.stringify(server),
      );
    };
    for (const platform of runnerPlatforms) {
      await writeFile(
        join(directory, `runner-release-${platform}.json`),
        JSON.stringify({
          ...runner,
          runners: { [platform]: runner.runners[platform] },
        }),
      );
    }
    const identity = { version, sourceCommit, published: true };
    assert.deepEqual(await mergeRunnerReleases(directory, identity), runner);
    await assert.rejects(
      mergeRunnerReleases(directory, {
        ...identity,
        sourceCommit: 'c'.repeat(40),
      }),
      /identity/,
    );
    const macContribution = join(directory, 'runner-release-darwin-arm64.json');
    const contribution = await readFile(macContribution);
    await rm(macContribution);
    await assert.rejects(mergeRunnerReleases(directory, identity));
    assert.equal(
      Object.keys(
        (await mergeRunnerReleases(directory, identity, { partial: true }))
          .runners,
      ).length,
      1,
    );
    await writeFile(macContribution, contribution);
    await descriptors();
    const formula = homebrewFormula(server);
    for (const platform of serverPlatforms) {
      assert.ok(formula.includes(server.servers[platform].filename));
      assert.ok(formula.includes(server.servers[platform].sha256));
    }
    assert.ok(!formula.includes('service do'));
    assert.equal(
      (await verifyRelease(directory, version, sourceCommit)).artifacts.length,
      9,
    );
    server.sourceCommit = 'b'.repeat(40);
    await descriptors();
    await assert.rejects(
      verifyRelease(directory, version, sourceCommit),
      /identity/,
    );
    server.sourceCommit = sourceCommit;
    const mac = server.servers['darwin-arm64'];
    delete server.servers['darwin-arm64'];
    await descriptors();
    await assert.rejects(
      verifyRelease(directory, version, sourceCommit),
      /platform set/,
    );
    server.servers['darwin-arm64'] = mac;
    const name = runner.runners['linux-amd64'].installer.filename;
    runner.runners['linux-amd64'].installer.filename = '../private.tar.gz';
    await descriptors();
    await assert.rejects(
      verifyRelease(directory, version, sourceCommit),
      /artifact/,
    );
    runner.runners['linux-amd64'].installer.filename = name;
    runner.protocolVersion++;
    await descriptors();
    await assert.rejects(
      verifyRelease(directory, version, sourceCommit),
      /protocol/,
    );
    runner.protocolVersion--;
    await descriptors();
    const archive = join(directory, name);
    const original = await readFile(archive);
    await writeFile(archive, Buffer.alloc(original.length, 1));
    await assert.rejects(
      verifyRelease(directory, version, sourceCommit),
      /integrity/,
    );
    await writeFile(archive, original);
    await writeFile(`${archive}.sha256`, 'different checksum');
    await assert.rejects(
      verifyRelease(directory, version, sourceCommit),
      /checksum/,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('release versions select immutable numbered releases', () => {
  for (const version of [
    'v0.1.0-alpha.1',
    'v0.1.0-beta.2',
    'v0.1.0-rc.1',
    'v1.0.0',
  ])
    assert.ok(validVersion(version));
  for (const version of [
    undefined,
    '',
    'dev',
    'latest',
    'v0.01.0',
    'v0.1.0-alpha.01',
    'v0.1.0\n',
    '../v0.1.0',
    'v0.1.0?url=elsewhere',
  ])
    assert.equal(validVersion(version), false);
});

test('upgrade fixture retains completion replies while grants and ordinary reads continue', async () => {
  const { runnerReplyProxy } = await import('./upgrade-proxy.mjs');
  const { createServer: httpServer } = await import('node:http');
  const { connect } = await import('node:net');
  const { once } = await import('node:events');
  let accepted;
  const committed = new Promise((resolve) => {
    accepted = resolve;
  });
  const server = httpServer((request, response) => {
    response.end(
      request.url.endsWith('/complete')
        ? 'retained-completion'
        : 'ordinary-read',
    );
    if (request.url.endsWith('/complete')) accepted();
  });
  let control;
  server.on('upgrade', (request, socket) => {
    control = socket;
    socket.write(
      'HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n',
    );
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const proxy = await runnerReplyProxy(server.address().port);
  const client = connect(Number(new URL(proxy.origin).port), '127.0.0.1');
  try {
    const headers = once(client, 'data');
    client.write(
      'GET /api/daemon/connect HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n',
    );
    assert.match((await headers)[0].toString(), /101 Switching/);
    proxy.holdReplies();
    let resolved = false;
    const reply = fetch(
      `${proxy.origin}/api/daemon/operations/previous/complete`,
      { method: 'POST' },
    ).then((response) => {
      resolved = true;
      return response.text();
    });
    await committed;
    assert.equal(await (await fetch(proxy.origin)).text(), 'ordinary-read');
    const grant = once(client, 'data');
    control.write('continued-grant');
    assert.equal((await grant)[0].toString(), 'continued-grant');
    assert.equal(resolved, false);
    proxy.releaseReplies();
    assert.equal(await reply, 'retained-completion');
  } finally {
    client.destroy();
    control?.destroy();
    await proxy.close();
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
  }
});

test('previous package evidence binds both artifact provenance and recovery checks', async () => {
  const { previousRelease, verifyPreviousRunnerUpgrade } =
    await import('./upgrade.mjs');
  const descriptor = {
    version: 'v0.1.0-alpha.9',
    sourceCommit: 'a'.repeat(40),
    runners: { 'linux-amd64': { installer: { sha256: 'b'.repeat(64) } } },
  };
  const report = {
    status: 'passed',
    mode: 'previous-upgrade',
    platform: 'linux',
    architecture: 'x64',
    version: descriptor.version,
    sourceCommit: descriptor.sourceCommit,
    fromVersion: previousRelease,
    entrypoint: 'runner-installer',
    entrypointBinarySHA256: 'c'.repeat(64),
    archiveSHA256: 'b'.repeat(64),
    model: { paid: false, requests: 5 },
  };
  const checks = [
    'runnerIdentityPreserved',
    'durableSourceReceiptPreserved',
    'journalPreserved',
    'legacyProtocolAccepted',
    'unsupportedInputProtocolQueued',
    'upgradedInputHistoryVerified',
    'sourceReceiptRetryPreserved',
    'lostCompletionReplyRecovered',
  ];
  for (const field of checks) report[field] = true;
  verifyPreviousRunnerUpgrade(report, descriptor, 'linux-amd64');
  for (const field of checks)
    assert.throws(() =>
      verifyPreviousRunnerUpgrade(
        { ...report, [field]: false },
        descriptor,
        'linux-amd64',
      ),
    );
  assert.throws(() =>
    verifyPreviousRunnerUpgrade(
      { ...report, sourceCommit: 'd'.repeat(40) },
      descriptor,
      'linux-amd64',
    ),
  );
  assert.throws(() =>
    verifyPreviousRunnerUpgrade(
      { ...report, archiveSHA256: 'e'.repeat(64) },
      descriptor,
      'linux-amd64',
    ),
  );
  assert.throws(() =>
    verifyPreviousRunnerUpgrade(
      { ...report, model: { paid: true, requests: 5 } },
      descriptor,
      'linux-amd64',
    ),
  );
});

test('baseline acquisition retains supported proxy settings while isolating model keys and fixture state', async () => {
  const { baselineAcquisitionEnvironment, httpProxyEnvironment } =
    await import('./upgrade.mjs');
  const proxies = Object.fromEntries(
    [
      'HTTP_PROXY',
      'HTTPS_PROXY',
      'NO_PROXY',
      'http_proxy',
      'https_proxy',
      'no_proxy',
    ].map((key) => [key, `synthetic-${key}`]),
  );
  const input = Object.freeze({
    ...proxies,
    PATH: '/fixed/toolchain/bin',
    HOME: '/operator/home',
    LANG: 'operator-language',
    XDG_CACHE_HOME: '/operator/cache',
    DEEPSEEK_API_KEY: 'synthetic-model-key',
    GITHUB_TOKEN: 'synthetic-github-token',
    CAMP_ENV_FILE: '/operator/private.env',
    NODE_OPTIONS: 'synthetic-node-override',
    NODE_TLS_REJECT_UNAUTHORIZED: '0',
    SSL_CERT_FILE: '/operator/certificate-override',
    ALL_PROXY: 'unsupported-route',
    all_proxy: 'unsupported-lower-route',
    BASH_ENV: '/operator/shell-startup',
  });
  assert.deepEqual(
    baselineAcquisitionEnvironment('/fixture/home', '/fixture/cache', input),
    {
      PATH: input.PATH,
      HOME: '/fixture/home',
      LANG: 'C.UTF-8',
      XDG_CACHE_HOME: '/fixture/cache',
      ...proxies,
    },
  );
  assert.deepEqual(httpProxyEnvironment(input), proxies);
  assert.equal(input.HOME, '/operator/home');
});

test('baseline proxy projection preserves explicit empties and lowercase precedence inputs', async () => {
  const { baselineAcquisitionEnvironment } = await import('./upgrade.mjs');
  const base = {
    PATH: '/fixed/bin',
    HOME: '/fixture/home',
    LANG: 'C.UTF-8',
    XDG_CACHE_HOME: '/fixture/cache',
  };
  assert.deepEqual(
    baselineAcquisitionEnvironment(base.HOME, base.XDG_CACHE_HOME, {
      PATH: base.PATH,
      HTTPS_PROXY: '',
      https_proxy: 'synthetic-lower-route',
      NO_PROXY: 'synthetic-bypass',
      HTTP_PROXY: undefined,
    }),
    {
      ...base,
      HTTPS_PROXY: '',
      https_proxy: 'synthetic-lower-route',
      NO_PROXY: 'synthetic-bypass',
    },
  );
  assert.deepEqual(
    baselineAcquisitionEnvironment(base.HOME, base.XDG_CACHE_HOME, {
      PATH: base.PATH,
    }),
    base,
  );
});

test('baseline packages resolve to fixture caches on both Mac architectures and Linux', async (t) => {
  const { readBaselineInstallation } = await import('./upgrade.mjs');
  const directory = await realpath(
    await mkdtemp(join(tmpdir(), 'camp-baseline-evidence-')),
  );
  t.after(() => rm(directory, { recursive: true, force: true }));
  const launcher = join(directory, 'launcher');
  await writeFile(launcher, 'fresh driver launcher', { mode: 0o700 });
  const version = 'v0.1.0-alpha.10';
  for (const target of serverPlatforms) {
    const [platform, arch] = target.split('-');
    for (const role of target === 'darwin-amd64'
      ? ['server']
      : ['server', 'runner']) {
      const cache =
        platform === 'darwin'
          ? join(directory, 'Library/Caches')
          : join(directory, 'baseline-cache');
      const installed = join(
        cache,
        'camp/releases',
        version,
        `camp-${role}-${target}`,
      );
      await mkdir(installed, { recursive: true, mode: 0o700 });
      const binary = join(installed, 'camp');
      await writeFile(binary, 'actual fixed public executable', {
        mode: 0o700,
      });
      const receipt = join(installed, '.camp-install.json');
      const saved = {
        version,
        archive: `camp-${role}-${target}.tar.gz`,
        archiveSHA256: 'b'.repeat(64),
        binarySHA256: await hashFile(binary),
      };
      await writeFile(receipt, JSON.stringify(saved), { mode: 0o600 });
      const read = () =>
        readBaselineInstallation(
          directory,
          role,
          version,
          launcher,
          platform,
          arch === 'amd64' ? 'x64' : arch,
        );
      const result = await read();
      assert.equal(result.binary, binary);
      assert.equal(result.cache, cache);
      assert.equal(result.identity.launcherSHA256, await hashFile(launcher));
      for (const invalid of [
        { ...saved, version: 'v0.1.0-alpha.9' },
        { ...saved, archive: 'wrong.tar.gz' },
        { ...saved, archiveSHA256: undefined },
        { ...saved, binarySHA256: 'c'.repeat(64) },
      ]) {
        await writeFile(receipt, JSON.stringify(invalid));
        await assert.rejects(read());
      }
      await writeFile(receipt, JSON.stringify(saved));
      await chmod(binary, 0o600);
      await assert.rejects(read());
      await chmod(binary, 0o700);
      await rm(binary);
      await symlink(launcher, binary);
      await assert.rejects(read());
    }
  }
});
