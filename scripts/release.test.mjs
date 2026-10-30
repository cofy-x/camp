import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { createServer } from 'node:net';
import { promisify } from 'node:util';
import { join } from 'node:path';
import { test } from 'node:test';
import { root } from './lib.mjs';
import { homebrewFormula, verifyPublishedFormula } from './homebrew.mjs';
import {
  hashFile,
  mergeRunnerReleases,
  serverPlatforms,
  runnerPlatforms,
  validVersion,
  verifyRelease,
} from './release.mjs';

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
