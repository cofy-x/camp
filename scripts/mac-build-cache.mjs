import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { appendFile, readFile } from 'node:fs/promises';
import { relative, resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';
import { go, root } from './lib.mjs';

export async function macBuildCache() {
  const capture = (executable, args, options = {}) =>
    execFileSync(executable, args, {
      cwd: root,
      encoding: 'utf8',
      ...options,
    }).trim();
  const directories = capture(
    go,
    [
      'list',
      '-deps',
      '-f',
      '{{if not .Standard}}{{.Dir}}{{end}}',
      './apps/server/cmd/camp-sandbox',
    ],
    { env: { ...process.env, CGO_ENABLED: '0' } },
  )
    .split('\n')
    .filter((directory) => directory.startsWith(`${root}${sep}`))
    .map((directory) => relative(root, directory));
  const paths = capture('git', [
    'ls-files',
    '-z',
    '--',
    ...directories,
    '.node-version',
    '.go-version',
    'go.mod',
    'go.sum',
    'LICENSE',
    'package.json',
    'pnpm-lock.yaml',
    'apps/dashboard/package.json',
    'packages/platinum/package.json',
    'packages/platinum/src/fonts/Inter-LICENSE.txt',
    'packages/platinum/src/fonts/ChicagoKare-LICENSE.txt',
    'packages/platinum/src/fonts/README.md',
    '.github/workflows/native-mac.yml',
    'scripts/mac-build-cache.mjs',
    'scripts/package-runner.mjs',
    'scripts/release.mjs',
    'scripts/licenses.mjs',
    'tools/release-archive',
    'tools/dsh-runtime',
    'tools/project-runtime',
    'tools/seatbelt-runtime',
    'tools/layerfs-worker',
  ])
    .split('\0')
    .filter(Boolean)
    .sort();
  const digest = createHash('sha256');
  const modules = capture(go, [
    'list',
    '-deps',
    '-f',
    '{{with .Module}}{{.Path}}@{{.Version}}{{end}}',
    './apps/server/cmd/server',
    './apps/server/cmd/camp-sandbox',
  ]);
  digest
    .update([...new Set(modules.split('\n').filter(Boolean))].sort().join('\n'))
    .update('\0');
  for (const path of paths) {
    digest.update(path).update('\0');
    digest.update(await readFile(resolve(root, path))).update('\0');
  }
  const prefix = [
    'native-mac-v1',
    process.arch,
    process.env.ImageOS,
    process.env.ImageVersion,
    `node${process.versions.node}`,
    capture(go, ['env', 'GOVERSION']),
  ].join('-');
  if (
    !/^[\w.-]+$/.test(prefix) ||
    !process.env.ImageOS ||
    !process.env.ImageVersion
  )
    throw new Error('A hosted Mac image identity is required.');
  const outputs = {
    prefix,
    key: `${prefix}-${digest.digest('hex')}`,
    'go-cache': capture(go, ['env', 'GOCACHE']),
  };
  if (process.env.GITHUB_OUTPUT)
    await appendFile(
      process.env.GITHUB_OUTPUT,
      Object.entries(outputs)
        .map(([name, value]) => `${name}=${value}\n`)
        .join(''),
    );
  return outputs;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href)
  console.log(JSON.stringify(await macBuildCache()));
