import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { checkDocs } from './check-docs.mjs';

function checkout(t, extra = {}) {
  const root = mkdtempSync(join(tmpdir(), 'camp-docs-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  execFileSync('git', ['-C', root, 'init', '-q']);
  const files = {
    '.gitignore': '.data/\n',
    'go.mod': 'module example.test/docs\n\ngo 1.27.3\n',
    '.node-version': '24.16.0\n',
    'package.json': JSON.stringify({
      packageManager: 'pnpm@12.11.2',
      engines: { node: '24.16.0', pnpm: '12.11.2' },
    }),
    'tools/dsh-runtime/package.json': JSON.stringify({
      dependencies: { '@deepseek-ai/dsh': '0.2.1-rc.3' },
    }),
    'tools/document-runtime/package.json': JSON.stringify({
      dependencies: { 'pdfjs-dist': '6.4.299' },
    }),
    'tools/project-runtime/package.json': JSON.stringify({
      dependencies: { pnpm: '12.11.2' },
    }),
    'tools/layerfs-runtime/pin.json': JSON.stringify({
      node: '24.16.0',
      pnpm: '12.11.2',
    }),
    'README.md': '# Guide\n',
    ...extra,
  };
  for (const [file, content] of Object.entries(files)) {
    mkdirSync(dirname(join(root, file)), { recursive: true });
    writeFileSync(join(root, file), content);
  }
  return root;
}

test('checks unstaged Markdown, reference/image links and GitHub heading collisions', (t) => {
  const root = checkout(t, {
    'go.mod': 'module example.test/docs\r\n\r\ngo 1.27.3\r\n',
    'README.md': [
      '# Guide',
      '[Guide](#guide)',
      '[Repeated][repeat]',
      '[Collision](<docs/A guide (new).md#repeat-1-1>)',
      '[Unicode](docs/A%20guide%20%28new%29.md#%E5%9B%A2%E9%98%9F--runner)',
      '![Diagram](assets/diagram.svg)',
      '[External](https://unreachable.invalid/does-not-exist)',
      '[Directory](docs/)',
      '`[Code example](missing.md)`',
      '```md',
      '[Another example](missing.md)',
      '# Not a heading',
      '```',
      '[repeat]: <docs/A guide (new).md#repeat-2>',
      'Go **1.27.3**, Node **24.16.0**, pnpm **12.11.2**, DSH **0.2.1-rc.3**, PDF.js **6.4.299**.',
      'npx --yes pnpm@12.11.2 exec playwright test',
      'This guide describes DSH 0.2.1-rc.3.',
    ].join('\n'),
    'docs/A guide (new).md':
      '# Repeat\n# Repeat\n# Repeat-1\n# Repeat\n# 团队 & `Runner`\n',
    'assets/diagram.svg': '<svg/>',
    '.data/ignored.md': '[Broken](absent.md)\n',
    'docs/decisions/old.md':
      '# Earlier choice\nNode 18.0.0 was used. [Guide](../A%20guide%20(new).md#repeat)\n',
  });
  const result = checkDocs(root);
  assert.deepEqual(result.errors, []);
  assert.equal(result.documents, 3);
  assert.equal(result.links, 7);
  assert.equal(
    execFileSync('git', ['-C', root, 'ls-files'], { encoding: 'utf8' }),
    '',
  );
});

test('reports broken targets and anchors with locations, including decision records', (t) => {
  const root = checkout(t, {
    'README.md': [
      '# Guide',
      '[Missing](docs/missing.md)',
      '[Anchor](#absent)',
      '[Escape](../outside.md)',
      '[Ignored](.data/secret.md)',
      '[Encoding](bad%ZZ.md)',
    ].join('\n'),
    '.data/secret.md': '# Private\n',
    'docs/decisions/old.md': '# Historical\n[Broken](absent.md)\n',
  });
  const { errors } = checkDocs(root);
  assert.equal(errors.length, 6);
  assert.match(
    errors.join('\n'),
    /README.md:2: Missing or ignored local target/,
  );
  assert.match(errors.join('\n'), /README.md:3: Missing heading anchor/);
  assert.match(
    errors.join('\n'),
    /README.md:4: Local link leaves this checkout/,
  );
  assert.match(
    errors.join('\n'),
    /README.md:5: Missing or ignored local target/,
  );
  assert.match(errors.join('\n'), /README.md:6: Cannot check bad%ZZ.md/);
  assert.match(
    errors.join('\n'),
    /docs\/decisions\/old.md:2: Missing or ignored local target/,
  );
});

test('detects stale version claims, shell examples and mismatched runtime pins', (t) => {
  const root = checkout(t, {
    'README.md':
      'Go **1.26.0**, Node **22.0.0**, pnpm **10.0.0**, DSH **0.1.0**, PDF.js **5.0.0**.\n```sh\nnpx pnpm@11.0.0 install\n```\n',
    'apps/example/package.json': JSON.stringify({
      engines: { node: '22.0.0', pnpm: '11.0.0' },
      packageManager: 'pnpm@11.0.0',
    }),
    'tools/layerfs-runtime/pin.json': JSON.stringify({
      node: '22.0.0',
      pnpm: '11.0.0',
    }),
    'tools/project-runtime/package.json': JSON.stringify({
      dependencies: { pnpm: '11.0.0' },
    }),
  });
  const { errors } = checkDocs(root);
  assert.equal(errors.length, 12);
  assert.match(
    errors.join('\n'),
    /README.md:1: Go 1.26.0 differs from go.mod \(1.27.3\)/,
  );
  assert.match(errors.join('\n'), /README.md:3: pnpm 11.0.0 differs/);
  assert.match(
    errors.join('\n'),
    /apps\/example\/package.json:1: engines.node 22.0.0 differs/,
  );
  assert.match(
    errors.join('\n'),
    /tools\/layerfs-runtime\/pin.json:1: node 22.0.0 differs/,
  );
  assert.match(
    errors.join('\n'),
    /tools\/project-runtime\/package.json:1: dependencies.pnpm 11.0.0 differs/,
  );
});

test('the command exits unsuccessfully for invalid documentation and succeeds after repair', (t) => {
  const root = checkout(t, { 'README.md': '# Guide\n[Missing](missing.md)\n' });
  const script = fileURLToPath(new URL('./check-docs.mjs', import.meta.url));
  const run = () =>
    spawnSync(process.execPath, [script, root], { encoding: 'utf8' });
  const failed = run();
  assert.equal(failed.status, 1);
  assert.match(failed.stderr, /README.md:2: Missing or ignored local target/);
  writeFileSync(join(root, 'missing.md'), '# Now present\n');
  const passed = run();
  assert.equal(passed.status, 0, passed.stderr);
  assert.match(
    passed.stdout,
    /Checked 2 Markdown files, 1 local links and pinned versions/,
  );
});
