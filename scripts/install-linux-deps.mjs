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

export function ubuntuSources(architecture, codename, mirror = 'canonical') {
  if (codename !== 'noble')
    throw new Error(`Unsupported CI Ubuntu: ${codename}`);
  let archive = {
    amd64: 'https://archive.ubuntu.com/ubuntu',
    arm64: 'https://ports.ubuntu.com/ubuntu-ports',
  }[architecture];
  if (!archive) throw new Error(`Unsupported CI architecture: ${architecture}`);
  if (!['canonical', 'mit'].includes(mirror))
    throw new Error(`Unknown CI mirror: ${mirror}`);
  if (mirror === 'mit')
    archive = `https://mirrors.mit.edu/${architecture === 'amd64' ? 'ubuntu' : 'ubuntu-ports'}`;
  const security =
    mirror === 'canonical' && architecture === 'amd64'
      ? 'https://security.ubuntu.com/ubuntu'
      : archive;
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
  const candidates = ['canonical', 'mit'].map((mirror) =>
    ubuntuSources(architecture, codename, mirror),
  );
  const directory = mkdtempSync(path.join(tmpdir(), 'camp-ci-apt-'));
  // The unprivileged APT downloader must be able to read this public source file.
  chmodSync(directory, 0o755);
  const sourcePath = path.join(directory, 'ubuntu.sources');
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
  const apt = (deadline, command) =>
    execute(
      'sudo',
      [
        'env',
        'DEBIAN_FRONTEND=noninteractive',
        'timeout',
        '--signal=TERM',
        '--kill-after=5s',
        deadline,
        'apt-get',
        ...options,
        ...command,
      ],
      { stdio: 'inherit' },
    );
  try {
    // Only this invocation uses these sources. Keep the hosted image's source
    // files, signing keys and unrelated repositories untouched.
    for (const [index, sources] of candidates.entries()) {
      console.log(sources);
      writeFileSync(sourcePath, sources, { mode: 0o644 });
      try {
        apt('120s', ['update', '--error-on=any']);
        break;
      } catch (error) {
        if (index === candidates.length - 1) throw error;
        console.error(
          `Ubuntu index refresh failed (${error.status || error.message}); trying the independent MIT mirror with the same Ubuntu signing key.`,
        );
      }
    }
    apt('180s', ['install', '--yes', '--no-install-recommends', ...packages]);
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
        [...candidates.join('\n').matchAll(/^URIs: https:\/\/([^/]+)/gm)]
          .map((match) => match[1])
          .concat('github.com'),
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
