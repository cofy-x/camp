import { spawn, spawnSync } from 'node:child_process';
import {
  copyFile,
  cp,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  writeFile,
} from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';

const root = import.meta.dirname;
const layerfs = JSON.parse(
  await readFile(path.join(root, '../layerfs-runtime/pin.json'), 'utf8'),
);
const pin = JSON.parse(await readFile(path.join(root, 'pin.json'), 'utf8'));
const args = process.argv.slice(2);
if (args.length !== 2 || args[0] !== '--source')
  throw new Error(
    'Usage: node install.mjs --source /path/to/read-only/layerfs',
  );
if (process.arch !== 'arm64' || process.version !== `v${layerfs.node}`)
  throw new Error(`An ARM64 host and Node ${layerfs.node} are required.`);
const cache = path.join(root, 'node_modules/.camp-worker');
await mkdir(cache, { recursive: true, mode: 0o700 });
const context = await mkdtemp(path.join(cache, 'build-'));
const source = path.join(context, 'source');
await mkdir(source);
const checkout = path.resolve(args[1]);
const identity = spawnSync(
  'git',
  ['-C', checkout, 'rev-parse', `${layerfs.commit}^{commit}`],
  { encoding: 'utf8' },
);
if (identity.status !== 0 || identity.stdout.trim() !== layerfs.commit)
  throw new Error(
    'The source checkout does not contain the pinned LayerFS commit.',
  );
const producer = spawn(
  'git',
  ['-C', checkout, 'archive', layerfs.commit, '--', '.', ':!docs/assets'],
  { stdio: ['ignore', 'pipe', 'inherit'] },
);
const consumer = spawn('tar', ['-xf', '-', '-C', source], {
  stdio: ['pipe', 'inherit', 'inherit'],
});
producer.stdout.pipe(consumer.stdin);

function wait(child) {
  return new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('close', (code) =>
      code === 0
        ? resolve()
        : reject(new Error(`Build subprocess exited ${code}.`)),
    );
  });
}

await Promise.all([wait(producer), wait(consumer)]);
const collector = path.join(context, 'collector');
await mkdir(collector);
await copyFile(
  path.join(root, 'collect-licenses.mjs'),
  path.join(collector, 'collect-licenses.mjs'),
);
await copyFile(
  path.join(root, '../layerfs-runtime/licenses.mjs'),
  path.join(collector, 'rust.mjs'),
);
await cp(
  path.join(root, '../layerfs-runtime/licenses'),
  path.join(collector, 'licenses'),
  { recursive: true },
);
const dockerSource = path.join(cache, `docker-cli-${pin.dockerCLICommit}`);
const command = (args) => {
  const result = spawnSync('git', args, { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(result.stderr.slice(-8192));
  return result.stdout.trim();
};
await mkdir(dockerSource, { recursive: true, mode: 0o700 });
command(['-C', dockerSource, 'init', '--quiet']);
if (
  spawnSync('git', [
    '-C',
    dockerSource,
    'cat-file',
    '-e',
    `${pin.dockerCLICommit}^{commit}`,
  ]).status !== 0
)
  command([
    '-C',
    dockerSource,
    'fetch',
    '--depth=1',
    pin.dockerCLIRepository,
    pin.dockerCLICommit,
  ]);
command(['-C', dockerSource, 'checkout', '--detach', pin.dockerCLICommit]);
if (command(['-C', dockerSource, 'rev-parse', 'HEAD']) !== pin.dockerCLICommit)
  throw new Error(
    'The Docker CLI notices differ from the fixed source revision.',
  );
const dockerNotices = path.join(context, 'docker-licenses');
await mkdir(dockerNotices);

async function notices(directory, relative = '') {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.name === '.git') continue;
    const member = path.join(directory, entry.name),
      name = path.join(relative, entry.name);
    if (
      /^(licen[sc]es?|notices?|copying|copyright|authors)([._-]|$)/i.test(
        entry.name,
      )
    ) {
      await mkdir(path.dirname(path.join(dockerNotices, name)), {
        recursive: true,
      });
      await cp(member, path.join(dockerNotices, name), {
        recursive: true,
        dereference: false,
      });
    } else if (entry.isDirectory()) await notices(member, name);
  }
}

await notices(dockerSource);
await copyFile(
  path.join(dockerSource, 'vendor/modules.txt'),
  path.join(dockerNotices, 'modules.txt'),
);
await writeFile(
  path.join(dockerNotices, 'source.json'),
  `${JSON.stringify({ repository: pin.dockerCLIRepository, commit: pin.dockerCLICommit, image: pin.dockerImage })}\n`,
);
for (const file of ['Dockerfile', 'seal.mjs'])
  await copyFile(path.join(root, file), path.join(context, file));
await mkdir(path.join(context, 'worker'));
for (const file of [
  'state.mjs',
  'cost.mjs',
  'contracts.mjs',
  'tree.mjs',
  'transfer.mjs',
  'protocol.mjs',
  'apply.mjs',
  'helper.mjs',
  'linux.mjs',
  'worker.mjs',
])
  await copyFile(path.join(root, file), path.join(context, 'worker', file));
await writeFile(
  path.join(context, '.dockerignore'),
  'source/node_modules\nsource/target\n',
);
const iid = path.join(context, 'image.id');
const build = spawn(
  'docker',
  [
    'build',
    '--platform',
    pin.platform,
    '--build-arg',
    `RUST_IMAGE=${pin.rustImage}`,
    '--build-arg',
    `NODE_IMAGE=${pin.nodeImage}`,
    '--build-arg',
    `DOCKER_IMAGE=${pin.dockerImage}`,
    '--build-arg',
    `LAYERFS_COMMIT=${layerfs.commit}`,
    '--iidfile',
    iid,
    context,
  ],
  { stdio: 'inherit' },
);
await wait(build);
const image = (await readFile(iid, 'utf8')).trim();
const inspected = spawnSync('docker', ['image', 'inspect', image], {
  encoding: 'utf8',
});
if (inspected.status !== 0) throw new Error('Built image inspection failed.');
const metadata = JSON.parse(inspected.stdout)[0];
if (metadata.Architecture !== 'arm64' || metadata.Os !== 'linux')
  throw new Error('The worker image is not native Linux ARM64.');
const installed = {
  schema: 1,
  commit: layerfs.commit,
  version: layerfs.version,
  ...pin,
  image,
  buildContext: context,
};
await writeFile(
  path.join(cache, 'installed.json'),
  `${JSON.stringify(installed, null, 2)}\n`,
  { mode: 0o600 },
);
await writeFile(
  path.join(root, 'installed.json'),
  `${JSON.stringify(installed, null, 2)}\n`,
  { mode: 0o600 },
);
process.stdout.write(`Native Linux ARM64 worker built: ${image}\n`);
