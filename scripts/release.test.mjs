import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { createServer } from 'node:net';
import { promisify } from 'node:util';
import { join } from 'node:path';
import { test } from 'node:test';
import { root } from './lib.mjs';
import { homebrewFormula } from './homebrew.mjs';
import {
  hashFile,
  serverPlatforms,
  validVersion,
  verifyRelease,
} from './release.mjs';

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
      if (name === 'camp-runtime-linux-amd64') {
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
      schema: 1,
      version,
      sourceCommit,
      protocolVersion,
      installer: await make('camp-runner-linux-amd64'),
      runtime: await make('camp-runtime-linux-amd64'),
      offline: await make('camp-runner-offline-linux-amd64'),
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
    await descriptors();
    const formula = homebrewFormula(server);
    for (const platform of serverPlatforms) {
      assert.ok(formula.includes(server.servers[platform].filename));
      assert.ok(formula.includes(server.servers[platform].sha256));
    }
    assert.ok(!formula.includes('service do'));
    assert.equal(
      (await verifyRelease(directory, version, sourceCommit)).artifacts.length,
      6,
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
    const name = runner.installer.filename;
    runner.installer.filename = '../private.tar.gz';
    await descriptors();
    await assert.rejects(
      verifyRelease(directory, version, sourceCommit),
      /artifact/,
    );
    runner.installer.filename = name;
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
