import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import test from 'node:test';
import {
  installLinuxDependencies,
  ubuntuSources,
} from './install-linux-deps.mjs';

test('Ubuntu CI sources retain security updates, architecture and signed metadata', () => {
  for (const architecture of ['amd64', 'arm64']) {
    const sources = ubuntuSources(architecture, 'noble');
    assert.match(sources, /Suites: noble noble-updates noble-backports\n/);
    assert.match(sources, /Suites: noble-security\n/);
    assert.match(sources, /Components: main universe\n/);
    assert.equal(
      (
        sources.match(
          /Signed-By: \/usr\/share\/keyrings\/ubuntu-archive-keyring.gpg/g,
        ) || []
      ).length,
      2,
    );
    assert.equal(
      (sources.match(new RegExp(`Architectures: ${architecture}`, 'g')) || [])
        .length,
      2,
    );
    assert.doesNotMatch(sources, /azure|mirror\+file|trusted=/);
    if (architecture === 'amd64') {
      assert.match(sources, /URIs: https:\/\/archive.ubuntu.com\/ubuntu\n/);
      assert.match(sources, /URIs: https:\/\/security.ubuntu.com\/ubuntu\n/);
    } else {
      assert.equal(
        (
          sources.match(/URIs: https:\/\/ports.ubuntu.com\/ubuntu-ports\n/g) ||
          []
        ).length,
        2,
      );
      assert.doesNotMatch(sources, /archive.ubuntu.com|security.ubuntu.com/);
    }
    const alternate = ubuntuSources(architecture, 'noble', 'mit');
    assert.equal(
      (alternate.match(/URIs: https:\/\/mirrors.mit.edu\//g) || []).length,
      2,
    );
    assert.match(
      alternate,
      architecture === 'amd64' ? /\/ubuntu\n/ : /\/ubuntu-ports\n/,
    );
    assert.match(alternate, /Suites: noble-security\n/);
    assert.equal(
      (
        alternate.match(
          /Signed-By: \/usr\/share\/keyrings\/ubuntu-archive-keyring.gpg/g,
        ) || []
      ).length,
      2,
    );
  }
  assert.throws(
    () => ubuntuSources('amd64', 'future'),
    /Unsupported CI Ubuntu/,
  );
  assert.throws(
    () => ubuntuSources('riscv64', 'noble'),
    /Unsupported CI architecture/,
  );
  assert.throws(
    () => ubuntuSources('amd64', 'noble', 'unknown'),
    /Unknown CI mirror/,
  );
});

test('installer refreshes strict signed indices before bounded installation, without changing host sources', (t) => {
  t.mock.method(console, 'log', () => {});
  const calls = [];
  let sourcePath;
  installLinuxDependencies(['bubblewrap', 'fuse3'], (binary, args, options) => {
    calls.push({ binary, args, options });
    if (binary === 'dpkg') return 'amd64\n';
    if (binary === 'lsb_release') return 'noble\n';
    if (binary === 'timeout') {
      assert.deepEqual(args, [
        '--kill-after=1s',
        '5s',
        'getent',
        'ahosts',
        'archive.ubuntu.com',
        'security.ubuntu.com',
      ]);
      assert.equal(options.stdio, 'ignore');
      return '';
    }
    assert.equal(binary, 'sudo');
    sourcePath = args
      .find((arg) => arg.startsWith('Dir::Etc::sourcelist='))
      .split('=')[1];
    assert.equal(
      readFileSync(sourcePath, 'utf8'),
      ubuntuSources('amd64', 'noble'),
    );
    assert.ok(args.includes('Dir::Etc::sourceparts=-'));
    assert.ok(args.includes('Acquire::Retries=2'));
    assert.ok(args.includes('Acquire::http::Timeout=15'));
    assert.ok(args.includes('Acquire::https::Timeout=15'));
    assert.ok(args.includes('Acquire::Languages=none'));
    assert.ok(
      args.includes('Acquire::IndexTargets::deb::CNF::DefaultEnabled=false'),
    );
    assert.equal(options.stdio, 'inherit');
    assert.deepEqual(args.slice(0, 2), [
      'env',
      'DEBIAN_FRONTEND=noninteractive',
    ]);
    assert.doesNotMatch(
      args.join(' '),
      /allow-unauthenticated|allow-insecure|\/etc\/apt\//,
    );
  });
  const [update, install] = calls.filter(({ binary }) => binary === 'sudo');
  assert.deepEqual(update.args.slice(2, 8), [
    'timeout',
    '--signal=TERM',
    '--kill-after=5s',
    '120s',
    'apt-get',
    '-o',
  ]);
  assert.deepEqual(update.args.slice(-2), ['update', '--error-on=any']);
  assert.equal(install.args[5], '180s');
  assert.deepEqual(install.args.slice(-5), [
    'install',
    '--yes',
    '--no-install-recommends',
    'bubblewrap',
    'fuse3',
  ]);
  assert.equal(calls.length, 5);
  assert.equal(existsSync(sourcePath), false);
});

test('a failed primary refresh switches to the independent signed mirror before installing anything', (t) => {
  t.mock.method(console, 'log', () => {});
  const diagnostics = [];
  t.mock.method(console, 'error', (message) => diagnostics.push(message));
  const apt = [];
  installLinuxDependencies(['bubblewrap'], (binary, args, options) => {
    if (binary === 'dpkg') return 'amd64\n';
    if (binary === 'lsb_release') return 'noble\n';
    if (binary === 'timeout') {
      assert.equal(options.stdio, 'ignore');
      return '';
    }
    assert.equal(binary, 'sudo');
    const sourcePath = args
      .find((arg) => arg.startsWith('Dir::Etc::sourcelist='))
      .slice('Dir::Etc::sourcelist='.length);
    const sources = readFileSync(sourcePath, 'utf8');
    apt.push({ args, sources });
    if (sources.includes('archive.ubuntu.com')) {
      assert.deepEqual(args.slice(-2), ['update', '--error-on=any']);
      throw Object.assign(new Error('primary DNS failure'), { status: 124 });
    }
    assert.equal(sources, ubuntuSources('amd64', 'noble', 'mit'));
  });
  assert.equal(apt.length, 3);
  assert.ok(apt[1].args.includes('update'));
  assert.ok(apt[2].args.includes('install'));
  assert.ok(
    diagnostics.some((message) => message.includes('independent MIT mirror')),
  );
});

test('unresolvable primary sources switch promptly without starting APT against them', (t) => {
  t.mock.method(console, 'log', () => {});
  t.mock.method(console, 'error', () => {});
  const apt = [];
  installLinuxDependencies(['bubblewrap'], (binary, args, options) => {
    if (binary === 'dpkg') return 'amd64\n';
    if (binary === 'lsb_release') return 'noble\n';
    if (binary === 'timeout') {
      assert.equal(options.stdio, 'ignore');
      if (args.includes('archive.ubuntu.com'))
        throw Object.assign(new Error('primary resolver timeout'), {
          status: 124,
        });
      assert.equal(args.at(-1), 'mirrors.mit.edu');
      return '';
    }
    assert.equal(binary, 'sudo');
    const sourcePath = args
      .find((arg) => arg.startsWith('Dir::Etc::sourcelist='))
      .slice('Dir::Etc::sourcelist='.length);
    assert.equal(
      readFileSync(sourcePath, 'utf8'),
      ubuntuSources('amd64', 'noble', 'mit'),
    );
    apt.push(args);
  });
  assert.equal(apt.length, 2);
  assert.deepEqual(apt[0].slice(-2), ['update', '--error-on=any']);
  assert.ok(apt[1].includes('install'));
});

test('index and download failures or deadlines stop installation, retain their exit status and clean up after diagnostic failure', (t) => {
  t.mock.method(console, 'log', () => {});
  const diagnostics = [];
  t.mock.method(console, 'error', (message) => diagnostics.push(message));
  for (const failedStage of ['update', 'install']) {
    for (const status of [100, 124]) {
      const calls = [];
      const failure = Object.assign(new Error('controlled APT failure'), {
        status,
      });
      let sourcePath;
      assert.throws(
        () =>
          installLinuxDependencies(['bubblewrap'], (binary, args, options) => {
            calls.push({ binary, args });
            if (binary === 'dpkg') return 'arm64\n';
            if (binary === 'lsb_release') return 'noble\n';
            if (binary === 'sudo') {
              sourcePath = args
                .find((arg) => arg.startsWith('Dir::Etc::sourcelist='))
                .split('=')[1];
              if (args.includes(failedStage)) throw failure;
            } else {
              assert.equal(binary, 'timeout');
              if (options.stdio === 'ignore') return '';
              assert.deepEqual(args, [
                '--kill-after=1s',
                '5s',
                'getent',
                'ahosts',
                args.at(-1),
              ]);
              assert.ok(
                ['ports.ubuntu.com', 'mirrors.mit.edu', 'github.com'].includes(
                  args.at(-1),
                ),
              );
              throw new Error('controlled DNS failure');
            }
          }),
        (error) => error === failure && error.status === status,
      );
      const apt = calls.filter(({ binary }) => binary === 'sudo');
      assert.equal(apt.length, 2);
      assert.equal(calls.at(-1).binary, 'timeout');
      assert.equal(existsSync(sourcePath), false);
    }
  }
  assert.ok(diagnostics.includes('DNS lookup failed: ports.ubuntu.com'));
});

test('installer rejects empty and option-shaped package requests before running commands', () => {
  for (const packages of [
    [],
    ['--allow-unauthenticated'],
    ['bubblewrap; true'],
  ])
    assert.throws(
      () =>
        installLinuxDependencies(packages, () => assert.fail('must not run')),
      /explicit Ubuntu package names/,
    );
});
