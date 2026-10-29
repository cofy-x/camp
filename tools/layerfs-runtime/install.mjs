import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  copyFile,
  cp,
  mkdir,
  readFile,
  rename,
  realpath,
  rm,
  writeFile,
} from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { createRequire } from 'node:module';
import { collectRustLicenses } from './licenses.mjs';

const root = import.meta.dirname;
const pin = JSON.parse(await readFile(path.join(root, 'pin.json'), 'utf8'));
const cache = path.join(root, 'node_modules/.camp-layerfs');
const source = path.join(cache, 'source');
const build = path.join(cache, 'build');
const buildEnv = {
  ...process.env,
  RUSTUP_HOME: path.join(build, 'rustup'),
  CARGO_HOME: path.join(build, 'cargo'),
  CARGO_TARGET_DIR: path.join(source, 'target'),
  LAYERFS_CONSTRUCTION_WORKERS: '1',
};
const args = process.argv.slice(2);
if (args.length > 2 || (args.length && args[0] !== '--source'))
  throw new Error(
    'Usage: node install.mjs [--source /path/to/layerfs-checkout]',
  );
if (process.platform !== 'linux' || process.arch !== 'x64')
  throw new Error(
    'This pinned Camp LayerFS runtime currently requires Linux x64.',
  );
if (process.version !== `v${pin.node}`)
  throw new Error(`Camp LayerFS runtime requires Node ${pin.node}.`);

function run(command, arguments_, options = {}) {
  const result = spawnSync(command, arguments_, {
    cwd: root,
    stdio: 'inherit',
    ...options,
  });
  if (result.error) throw result.error;
  if (result.status !== 0)
    throw new Error(`${command} exited with status ${result.status}.`);
  return result.stdout?.trim();
}

async function archive(checkout, destination) {
  await mkdir(destination, { recursive: true });
  const producer = spawn(
    'git',
    ['-C', checkout, 'archive', pin.commit, '--', '.', ':!docs/assets'],
    {
      stdio: ['ignore', 'pipe', 'inherit'],
    },
  );
  const consumer = spawn('tar', ['-xf', '-', '-C', destination], {
    stdio: ['pipe', 'inherit', 'inherit'],
  });
  producer.stdout.pipe(consumer.stdin);
  const wait = (child) =>
    new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('close', (code) =>
        code === 0
          ? resolve()
          : reject(new Error(`Source archive failed (${code}).`)),
      );
    });
  await Promise.all([wait(producer), wait(consumer)]);
}

await mkdir(build, { recursive: true });
const checkout = args[1] ? path.resolve(args[1]) : path.join(build, 'checkout');
if (!args[1]) {
  try {
    await readFile(path.join(checkout, 'HEAD'));
  } catch {
    run('git', ['init', '--bare', checkout]);
  }
  // The pinned build needs source, not large documentation videos. Keep this
  // promisor configuration inside Camp's private build cache only.
  run('git', ['-C', checkout, 'config', 'remote.origin.url', pin.repository]);
  run('git', ['-C', checkout, 'config', 'remote.origin.promisor', 'true']);
  run('git', [
    '-C',
    checkout,
    'config',
    'remote.origin.partialclonefilter',
    'blob:limit=10m',
  ]);
  const present = spawnSync(
    'git',
    ['-C', checkout, 'rev-parse', '--verify', '--quiet', 'refs/camp/runtime'],
    { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] },
  );
  if (present.error) throw present.error;
  // Inspect a local ref rather than opening an absent object: a promisor
  // cat-file probe would implicitly fetch history before the shallow fetch.
  if (present.status !== 0 || present.stdout.trim() !== pin.commit)
    run('git', [
      '-C',
      checkout,
      'fetch',
      '--depth=1',
      '--filter=blob:limit=10m',
      'origin',
      `+${pin.commit}:refs/camp/runtime`,
    ]);
}
const revision = run(
  'git',
  ['-C', checkout, 'rev-parse', `${pin.commit}^{commit}`],
  { encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'] },
);
if (revision !== pin.commit)
  throw new Error('LayerFS source identity does not match pin.json.');
let existing = null;
try {
  existing = await readFile(path.join(source, '.camp-source'), 'utf8');
} catch {
  /* Fresh installation. */
}
if (existing?.trim() !== pin.commit) {
  const stage = path.join(cache, `source-stage-${process.pid}`);
  await archive(checkout, stage);
  await writeFile(path.join(stage, '.camp-source'), `${pin.commit}\n`);
  await rm(source, { recursive: true, force: true });
  await rename(stage, source);
}

run(
  'rustup',
  [
    'toolchain',
    'install',
    pin.rust,
    '--profile',
    'minimal',
    '--no-self-update',
  ],
  { env: buildEnv },
);
run('cargo', [`+${pin.rust}`, 'fetch', '--locked'], {
  cwd: source,
  env: buildEnv,
});
run(
  'cargo',
  [
    `+${pin.rust}`,
    'build',
    '--offline',
    '--locked',
    '--release',
    '-p',
    'layerfs-node',
    '-p',
    'layerfs-daemon',
  ],
  {
    cwd: source,
    env: buildEnv,
  },
);
await copyFile(
  path.join(source, 'target/release/liblayerfs_node.so'),
  path.join(source, 'engine/crates/layerfs-node/layerfs.node'),
);
await collectRustLicenses({
  source,
  environment: buildEnv,
  rust: pin.rust,
  destination: path.join(cache, 'licenses'),
});
run(
  'npm',
  ['install', '--ignore-scripts', '--offline', '--package-lock=false'],
  { cwd: path.join(source, 'api') },
);

const context = path.join(build, 'image');
await mkdir(path.join(context, 'libraries'), { recursive: true });
await copyFile(path.join(root, 'Dockerfile'), path.join(context, 'Dockerfile'));
await copyFile(
  path.join(source, 'target/release/layerfs-daemon'),
  path.join(context, 'layerfs-daemon'),
);
await copyFile(process.execPath, path.join(context, 'node'));
const pnpmSource = await realpath(
  path.join(root, '../project-runtime/node_modules/pnpm'),
);
const pnpmManifest = JSON.parse(
  await readFile(path.join(pnpmSource, 'package.json'), 'utf8'),
);
if (pnpmManifest.version !== pin.pnpm)
  throw new Error(
    `Install the pinned Camp project runtime: pnpm ${pin.pnpm} is required.`,
  );
const pnpmRequire = createRequire(path.join(pnpmSource, 'package.json'));
const pnpmNative = path.dirname(
  pnpmRequire.resolve('@pnpm/exe.linux-x64/package.json'),
);
const pnpmNativeManifest = JSON.parse(
  await readFile(path.join(pnpmNative, 'package.json'), 'utf8'),
);
if (pnpmNativeManifest.version !== pin.pnpm)
  throw new Error('The pnpm native binary version does not match its wrapper.');
const pnpmImage = path.join(context, 'pnpm');
await rm(pnpmImage, { recursive: true, force: true });
await mkdir(pnpmImage, { recursive: true });
for (const entry of [
  'bin',
  'dist',
  'native-binary.mjs',
  'package.json',
  'THIRD-PARTY-NOTICES.md',
])
  await cp(path.join(pnpmSource, entry), path.join(pnpmImage, entry), {
    recursive: true,
    dereference: true,
  });
await cp(pnpmNative, path.join(pnpmImage, 'node_modules/@pnpm/exe.linux-x64'), {
  recursive: true,
  dereference: true,
});
await copyFile(path.join(root, 'pnpm.sh'), path.join(context, 'pnpm.sh'));
await copyFile(path.join(root, 'export.mjs'), path.join(context, 'export.mjs'));
for (const executable of [
  process.execPath,
  path.join(context, 'layerfs-daemon'),
]) {
  const dependencies = run('ldd', [executable], {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'inherit'],
  });
  for (const match of dependencies.matchAll(/\s(\S+) => (\/\S+) \(/g)) {
    if (['libc.so.6', 'libm.so.6'].includes(match[1])) continue;
    await copyFile(match[2], path.join(context, 'libraries', match[1]));
  }
}
run('docker', [
  'build',
  '--build-arg',
  `BASE_IMAGE=${pin.baseImage}`,
  '--iidfile',
  path.join(build, 'image.id'),
  context,
]);
const image = (await readFile(path.join(build, 'image.id'), 'utf8')).trim();
const sha256 = async (file) =>
  createHash('sha256')
    .update(await readFile(file))
    .digest('hex');
const installed = {
  schema: 1,
  commit: pin.commit,
  version: pin.version,
  platform: 'linux/x64',
  source,
  daemon: path.join(source, 'target/release/layerfs-daemon'),
  native: path.join(source, 'engine/crates/layerfs-node/layerfs.node'),
  image,
  baseImage: pin.baseImage,
  daemonSha256: await sha256(path.join(context, 'layerfs-daemon')),
  nativeSha256: await sha256(
    path.join(source, 'engine/crates/layerfs-node/layerfs.node'),
  ),
  nodeSha256: await sha256(process.execPath),
  pnpmVersion: pin.pnpm,
  pnpmSha256: await sha256(path.join(pnpmNative, 'pnpm')),
};
await writeFile(
  path.join(root, 'installed.json'),
  `${JSON.stringify(installed, null, 2)}\n`,
  { mode: 0o600 },
);
process.stdout.write(
  `Camp LayerFS ${pin.commit} installed with image ${image}.\n`,
);
