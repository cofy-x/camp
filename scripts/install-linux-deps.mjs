import { execFileSync } from 'node:child_process';
import {
  chmodSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

export function ubuntuSources(architecture, codename) {
  if (codename !== 'noble')
    throw new Error(`Unsupported CI Ubuntu: ${codename}`);
  const archive = {
    amd64: 'https://archive.ubuntu.com/ubuntu',
    arm64: 'https://ports.ubuntu.com/ubuntu-ports',
  }[architecture];
  if (!archive) throw new Error(`Unsupported CI architecture: ${architecture}`);
  const security =
    architecture === 'amd64' ? 'https://security.ubuntu.com/ubuntu' : archive;
  return [
    [archive, 'noble noble-updates noble-backports'],
    [security, 'noble-security'],
  ]
    .map(
      ([uri, suites]) =>
        `Types: deb\nURIs: ${uri}\nSuites: ${suites}\nComponents: main restricted universe multiverse\nArchitectures: ${architecture}\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n`,
    )
    .join('\n');
}

export function installLinuxDependencies(packages, execute = execFileSync) {
  if (
    !packages.length ||
    packages.some((name) => !/^[a-z0-9][a-z0-9+.-]*$/.test(name))
  )
    throw new Error('Pass explicit Ubuntu package names.');
  const architecture = execute('dpkg', ['--print-architecture'], {
    encoding: 'utf8',
  }).trim();
  const codename = execute('lsb_release', ['--codename', '--short'], {
    encoding: 'utf8',
  }).trim();
  const sources = ubuntuSources(architecture, codename);
  const directory = mkdtempSync(path.join(tmpdir(), 'camp-ci-apt-'));
  // The unprivileged APT downloader must be able to read this public source file.
  chmodSync(directory, 0o755);
  const sourcePath = path.join(directory, 'ubuntu.sources');
  writeFileSync(sourcePath, sources, { mode: 0o644 });
  const options = [
    '-o',
    `Dir::Etc::sourcelist=${sourcePath}`,
    '-o',
    'Dir::Etc::sourceparts=-',
    '-o',
    'Acquire::Retries=2',
    '-o',
    'Acquire::http::Timeout=15',
    '-o',
    'Acquire::https::Timeout=15',
    '-o',
    'Dpkg::Use-Pty=0',
  ];
  console.log(
    `Linux CI dependencies: ${architecture}/${codename}; image=${process.env.ImageOS || 'unknown'}/${process.env.ImageVersion || 'unknown'}`,
  );
  console.log(sources);
  try {
    // Only this invocation uses these sources. Keep the hosted image's source
    // files, signing keys and unrelated repositories untouched.
    for (const [deadline, command] of [
      ['120s', ['update', '--error-on=any']],
      ['180s', ['install', '--yes', '--no-install-recommends', ...packages]],
    ])
      execute(
        'sudo',
        [
          'env',
          'DEBIAN_FRONTEND=noninteractive',
          'timeout',
          '--signal=TERM',
          '--kill-after=10s',
          deadline,
          'apt-get',
          ...options,
          ...command,
        ],
        { stdio: 'inherit' },
      );
  } catch (error) {
    console.error(
      'Linux CI dependency setup failed; see Set up job for the hosted region and image.',
    );
    try {
      console.error(readFileSync('/etc/resolv.conf', 'utf8'));
    } catch {
      console.error('Resolver configuration unavailable.');
    }
    const hosts = [
      ...new Set(
        [...sources.matchAll(/^URIs: https:\/\/([^/]+)/gm)].map(
          (match) => match[1],
        ),
      ),
    ];
    for (const host of hosts) {
      console.error(`DNS diagnostic: ${host}`);
      try {
        execute(
          'timeout',
          ['--kill-after=1s', '5s', 'getent', 'ahosts', host],
          { stdio: 'inherit' },
        );
      } catch {
        console.error(`DNS lookup failed: ${host}`);
      }
    }
    throw error;
  } finally {
    rmSync(directory, { recursive: true });
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href
) {
  try {
    installLinuxDependencies(process.argv.slice(2));
  } catch (error) {
    console.error(error.message);
    process.exitCode = error.status || 1;
  }
}
