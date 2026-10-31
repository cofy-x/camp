import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { cp, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { promisify } from 'node:util';

const execute = promisify(execFile);
const licensePattern = /^(licen[sc]es?|notices?|copying)([._-]|$)/i;

export async function collectRustLicenses({
  source,
  environment,
  rust,
  destination,
  platform = 'x86_64-unknown-linux-gnu',
}) {
  const options = { cwd: source, env: environment, maxBuffer: 16 << 20 };
  const { stdout } = await execute(
    'cargo',
    [
      `+${rust}`,
      'metadata',
      '--offline',
      '--locked',
      '--format-version',
      '1',
      '--filter-platform',
      platform,
    ],
    options,
  );
  const metadata = JSON.parse(stdout);
  const nodes = new Map(metadata.resolve.nodes.map((node) => [node.id, node]));
  const needed = new Set();

  function visit(id) {
    if (needed.has(id)) return;
    needed.add(id);
    for (const dependency of nodes.get(id).deps)
      if (dependency.dep_kinds.some((kind) => kind.kind !== 'dev'))
        visit(dependency.pkg);
  }

  for (const name of ['layerfs-daemon', 'layerfs-node']) {
    const entry = metadata.packages.find((item) => item.name === name);
    if (!entry) throw new Error(`Missing native package ${name}.`);
    visit(entry.id);
  }
  const supplements = JSON.parse(
    await readFile(
      path.join(import.meta.dirname, 'licenses/supplements.json'),
      'utf8',
    ),
  );
  await rm(destination, { recursive: true, force: true });
  await mkdir(destination, { recursive: true });
  const modules = {};
  for (const item of metadata.packages.filter((item) => needed.has(item.id))) {
    const identity = `${item.name}@${item.version}`;
    if (!/^[A-Za-z0-9_.+-]+@[A-Za-z0-9_.+-]+$/.test(identity))
      throw new Error('Invalid native license identity.');
    const directory = path.dirname(item.manifest_path);
    const output = path.join(destination, identity);
    await mkdir(output);
    const names = new Set(
      (await readdir(directory)).filter((name) => licensePattern.test(name)),
    );
    if (item.license_file) {
      const relative = path.relative(
        directory,
        path.resolve(directory, item.license_file),
      );
      if (relative.startsWith('..') || path.isAbsolute(relative))
        throw new Error('Native license path escaped its package.');
      names.add(relative);
    }
    for (const name of names)
      await cp(
        path.join(directory, name),
        path.join(output, path.basename(name)),
        { recursive: true },
      );
    let origin = item.repository;
    if (!names.size && item.source === null) {
      await cp(path.join(source, 'LICENSE'), path.join(output, 'LICENSE'));
    } else if (!names.size) {
      const commit = supplements.packages[identity];
      const vcs = JSON.parse(
        await readFile(path.join(directory, '.cargo_vcs_info.json'), 'utf8'),
      );
      if (
        !commit ||
        vcs.git.sha1 !== commit ||
        item.repository !== supplements.repository
      )
        throw new Error(`Missing reviewed license for ${identity}.`);
      const filename = path.join(
        import.meta.dirname,
        'licenses',
        supplements.filename,
      );
      const data = await readFile(filename);
      if (
        createHash('sha256').update(data).digest('hex') !== supplements.sha256
      )
        throw new Error('Native license supplement integrity failed.');
      await writeFile(path.join(output, 'LICENSE'), data);
      origin = `${supplements.repository}/blob/${commit}/LICENSE`;
    }
    modules[identity] = { license: item.license, origin };
  }
  const { stdout: sysroot } = await execute(
    'rustc',
    [`+${rust}`, '--print', 'sysroot'],
    options,
  );
  const compiler = path.join(destination, `Rust-${rust}`);
  await mkdir(compiler);
  for (const name of ['COPYRIGHT', 'LICENSE-MIT', 'LICENSE-APACHE'])
    await cp(
      path.join(sysroot.trim(), 'share/doc/rust', name),
      path.join(compiler, name),
    );
  await writeFile(
    path.join(destination, 'modules.json'),
    `${JSON.stringify(modules, null, 2)}\n`,
  );
  return modules;
}
