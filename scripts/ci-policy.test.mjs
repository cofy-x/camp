import assert from 'node:assert/strict';
import { readFile, mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import test from 'node:test';
import { execFileSync, spawnSync } from 'node:child_process';
import os from 'node:os';
import path from 'node:path';
import {
  gateDecision,
  classifyChanges,
  validationStage,
} from './change-policy.mjs';

function workflowJob(text, id) {
  return text.split(`\n  ${id}:\n`)[1].split(/\n {2}[a-z_]+:\n/)[0];
}

test('protected contexts always resolve, including documentation and failed classification', async () => {
  const text = await readFile(
    new URL('../.github/workflows/ci.yml', import.meta.url),
    'utf8',
  );
  assert(!/\n {4}paths(?:-ignore)?:/.test(text));
  for (const [id, name] of [
    ['source', 'Source'],
    ['browser', 'Browser'],
    ['fixture', 'Fixture'],
  ]) {
    const block = workflowJob(text, id);
    assert(block.includes(`name: ${name}\n`));
    assert(block.includes('if: ${{ always() }}'));
    assert(
      block.includes('CLASSIFICATION_RESULT: ${{ needs.changes.result }}'),
    );
    assert(block.includes('test "$CLASSIFICATION_RESULT" = success'));
  }
  assert(text.includes('test "$RESULT" = skipped'));
  assert(text.includes("needs.changes.outputs.browser == 'true'"));
  assert(text.includes("needs.changes.outputs.server == 'true'"));
});
test('native/checkpoint workflows have one caller classification and retain explicit manual qualification', async () => {
  for (const name of ['native-mac.yml', 'checkpoint-proof.yml']) {
    const text = await readFile(
      new URL(`../.github/workflows/${name}`, import.meta.url),
      'utf8',
    );
    assert(text.includes('  workflow_call:'));
    assert(text.includes('  workflow_dispatch:'));
    assert(!text.includes('  pull_request:'));
  }
});

test('native recovery retains failure evidence without masking the test exit', async (t) => {
  const workflow = await readFile(
    new URL('../.github/workflows/native-mac.yml', import.meta.url),
    'utf8',
  );
  const step = workflow
    .split(
      '      - name: Check native reconnection, revocation and crash recovery without replay\n',
    )[1]
    .split('      - name:')[0];
  assert.match(step, /^ {8}shell: bash$/m);
  const script = step.match(/^ {8}run: (.+)$/m)[1];
  const directory = await mkdtemp(path.join(os.tmpdir(), 'camp-native-gate-'));
  t.after(() => rm(directory, { recursive: true }));
  await mkdir(path.join(directory, 'dist/native-mac'), { recursive: true });
  await writeFile(
    path.join(directory, 'go'),
    '#!/bin/sh\nprintf "controlled failure\\n"\nexit 7\n',
    { mode: 0o700 },
  );
  const result = spawnSync(
    'bash',
    ['--noprofile', '--norc', '-eo', 'pipefail', '-c', script],
    {
      cwd: directory,
      env: { PATH: `${directory}:/usr/bin:/bin`, LANG: 'C.UTF-8' },
    },
  );
  assert.equal(result.status, 7, result.stderr?.toString());
  assert.equal(
    await readFile(
      path.join(directory, 'dist/native-mac/recovery.jsonl'),
      'utf8',
    ),
    'controlled failure\n',
  );
});

test('required Source, Browser and Fixture shell gates reject failed, cancelled and uncertain execution', async (t) => {
  const text = await readFile(
    new URL('../.github/workflows/ci.yml', import.meta.url),
    'utf8',
  );
  const tmp = await mkdtemp(path.join(os.tmpdir(), 'camp-ci-gate-'));
  t.after(() => rm(tmp, { recursive: true }));
  for (const id of ['source', 'browser', 'fixture']) {
    const script = workflowJob(text, id)
      .split('        run: |\n')[1]
      .split('\n')
      .filter((line) => line.startsWith('          '))
      .map((line) => line.slice(10))
      .join('\n');
    for (const required of ['true', 'false', '', 'unknown'])
      for (const result of ['success', 'failure', 'cancelled', 'skipped', ''])
        for (const secondary of id === 'fixture'
          ? ['']
          : ['success', 'failure', 'cancelled', 'skipped', ''])
          for (const classification of [
            'success',
            'failure',
            'cancelled',
            'skipped',
            '',
          ]) {
            const execution = spawnSync(
              'bash',
              ['--noprofile', '--norc', '-e', '-c', script],
              {
                env: {
                  PATH: '/usr/bin:/bin',
                  LANG: 'C.UTF-8',
                  DOCS_ONLY: required,
                  REQUIRED: required,
                  RESULT: result,
                  GO_RESULT: secondary,
                  EVIDENCE_RESULT: secondary,
                  CLASSIFICATION_RESULT: classification,
                  GITHUB_STEP_SUMMARY: path.join(tmp, 'summary'),
                },
              },
            );
            const expected =
              id === 'source'
                ? ['true', 'false'].includes(required) &&
                  gateDecision('true', result, classification).passed &&
                  secondary === (required === 'true' ? 'skipped' : 'success')
                : gateDecision(required, result, classification).passed &&
                  (id === 'fixture' ||
                    secondary ===
                      (required === 'true' ? 'success' : 'skipped'));
            assert.equal(
              execution.status === 0,
              expected,
              JSON.stringify({
                id,
                required,
                result,
                classification,
                secondary,
                stderr: execution.stderr?.toString(),
              }),
            );
          }
  }
});

test('long source and fixture jobs are cancelable while protected gates remain short and command-free', async () => {
  const workflow = await readFile(
    new URL('../.github/workflows/ci.yml', import.meta.url),
    'utf8',
  );
  for (const id of [
    'source_checks',
    'source_go',
    'browser_evidence',
    'fixture_checks',
  ]) {
    const block = workflowJob(workflow, id);
    const header = block.split('    steps:\n')[0];
    assert.match(header, /!cancelled\(\)/);
    assert.doesNotMatch(header, /always\(\)/);
    assert.match(header, /needs.changes.result == 'success'/);
  }
  for (const [id, execution] of [
    ['source', 'source_checks'],
    ['browser', 'browser_project'],
    ['fixture', 'fixture_checks'],
  ]) {
    const block = workflowJob(workflow, id);
    assert.match(block, /timeout-minutes: 5/);
    assert.ok(block.includes(`needs: [changes, ${execution}`));
    assert.ok(block.includes('RESULT: ${{ needs.' + execution + '.result }}'));
    assert.doesNotMatch(block, /uses:|make |go |npx |docker /);
    assert.equal((block.match(/- name:/g) || []).length, 1);
  }
  const source = workflowJob(workflow, 'source_checks');
  for (const command of [
    'make typecheck lint format-check',
    'make generate-check',
    'make test-js',
    'make build smoke-production',
    'node scripts/smoke-upgrade.mjs',
    'make check-docs',
    "exec prettier --check '**/*.md'",
    'go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12',
    'git diff --exit-code',
  ])
    assert.ok(source.includes(command), command);
  assert.ok(source.includes("needs.changes.outputs.docs == 'true'"));
  assert.ok(source.includes('["static", "sql", "javascript", "production"]'));
  const go = workflowJob(workflow, 'source_go');
  assert.ok(go.includes('shard: [1, 2]'));
  assert.ok(go.includes('make test-go-shard GO_TEST_SHARD="$GO_TEST_SHARD"'));
  assert.ok(
    workflowJob(workflow, 'source').includes(
      'GO_RESULT: ${{ needs.source_go.result }}',
    ),
  );
  const fixture = workflowJob(workflow, 'fixture_checks');
  assert.ok(
    fixture
      .split('    steps:\n')[0]
      .includes("needs.changes.outputs.fixture == 'true'"),
  );
  assert.ok(fixture.includes('make test-remote'));
  assert.ok(
    fixture.includes(
      'node scripts/remote.mjs down --project "$CAMP_REMOTE_PROJECT"',
    ),
  );
  assert.match(
    fixture,
    /Remove this test project's containers and volumes\n[\s\S]+timeout-minutes: 5/,
  );
});
test('release runs complete core regression even after documentation-only main CI', async () => {
  const plan = validationStage(
    classifyChanges([{ status: 'M', paths: ['README.md'] }]),
    'release',
  );
  assert.equal(plan.documentationOnly, false);
  for (const key of ['source', 'browser', 'fixture'])
    assert.equal(plan.checks[key], true);
  const release = await readFile(
    new URL('../.github/workflows/release.yml', import.meta.url),
    'utf8',
  );
  assert.match(
    release,
    /core_validation:[\s\S]+uses: \.\/\.github\/workflows\/ci.yml[\s\S]+validation-stage: release/,
  );
  assert.match(
    release,
    /checkpoint_worker_build:[\s\S]+needs: \[preflight, core_validation\]/,
  );
});

test('portable Mac source policy drives the real command path and propagates failures', async () => {
  const { checkPortableGo } = await import('./check-portable-go.mjs');
  const { listedTests } = await import('./test-go-shard.mjs');
  const calls = [];
  const packages = [
    'github.com/cofy-x/camp',
    'github.com/cofy-x/camp/apps/server/internal/layerfs',
    'github.com/cofy-x/camp/apps/server/internal/resource',
  ];
  const listing = ['TestAlpha', 'TestBeta', 'Example', 'FuzzSeed']
    .map((name) => JSON.stringify({ Action: 'output', Output: `${name}\n` }))
    .join('\n');
  const respond = (args) =>
    args[0] === 'list'
      ? packages.join('\n')
      : args.includes('-list')
        ? listing
        : '';
  const plan = checkPortableGo((file, args, options) => {
    calls.push({ file, args, options });
    return respond(args);
  });
  assert.equal(calls.length, 4);
  const names = listedTests(listing);
  const selected = [];
  for (const call of calls.slice(1)) {
    assert.equal(call.file, 'go');
    assert.deepEqual(call.args.slice(-2), [packages[0], packages[2]]);
    assert.ok(call.args.includes('-race'));
    assert.equal(call.args[call.args.indexOf('-p') + 1], '2');
    assert.ok(!call.args.some((arg) => arg.startsWith('-timeout')));
    assert.equal(call.options.env.GOTOOLCHAIN, 'local');
    assert.equal(call.options.env.DEEPSEEK_API_KEY, undefined);
    assert.deepEqual(call.options.env, calls[0].options.env);
  }
  assert.equal(
    calls[1].args[calls[1].args.indexOf('-list') + 1],
    '^(Test|Example|Fuzz)',
  );
  for (const call of calls.slice(2)) {
    assert.ok(call.args.includes('-count=1'));
    const pattern = new RegExp(call.args[call.args.indexOf('-run') + 1]);
    selected.push(...names.filter((name) => pattern.test(name)));
  }
  assert.deepEqual(selected.sort(), names);
  assert.equal(new Set(selected).size, names.length);
  assert.equal(plan.exclusions.length, 1);
  assert.throws(
    () => checkPortableGo(() => packages[0]),
    /Stale platform exclusion/,
  );
  for (const failedCall of [1, 2, 3]) {
    let call = 0;
    assert.throws(
      () =>
        checkPortableGo((_file, args) => {
          if (call++ === failedCall) throw Error('controlled Go failure');
          return respond(args);
        }),
      /controlled Go failure/,
    );
    assert.equal(call, failedCall + 1);
  }
  for (const empty of [
    '',
    '{"Action":"fail","Package":"broken"}',
    JSON.stringify({ Action: 'output', Output: 'TestAlpha' }),
  ]) {
    let ran = false;
    assert.throws(() =>
      checkPortableGo((_file, args) => {
        if (args[0] === 'list') return packages.join('\n');
        if (args.includes('-list')) return empty;
        ran = true;
        return '';
      }),
    );
    assert.equal(ran, false);
  }
  const text = await readFile(
    new URL('../.github/workflows/ci.yml', import.meta.url),
    'utf8',
  );
  const block = text.split('  mac_source:\n')[1].split(/\n {2}[a-z_]+:\n/)[0];
  assert(block.includes("inputs.validation-stage == 'release'"));
  assert(block.includes('node scripts/check-portable-go.mjs'));
  assert(block.includes('test-js'));
});

test('portable Go shards execute the real package set, nested tests, examples and fuzz seeds without caching', async (t) => {
  const { checkPortableGo } = await import('./check-portable-go.mjs');
  const cwd = await mkdtemp(path.join(os.tmpdir(), 'camp-portable-go-'));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  await writeFile(
    path.join(cwd, 'go.mod'),
    'module github.com/cofy-x/camp\n\ngo 1.27.3\n',
  );
  await writeFile(
    path.join(cwd, 'root_test.go'),
    `package camp
import ("fmt"; "testing")
func TestAlpha(t *testing.T) { t.Run("nested", func(t *testing.T) {}) }
func FuzzSeed(f *testing.F) { f.Add("one"); f.Add("two"); f.Fuzz(func(t *testing.T, s string) {}) }
func Example() { fmt.Println("portable proof")
// Output: portable proof
}
`,
  );
  for (const name of ['layerfs', 'resource'])
    await mkdir(path.join(cwd, 'apps/server/internal', name), {
      recursive: true,
    });
  await writeFile(
    path.join(cwd, 'apps/server/internal/layerfs/excluded_test.go'),
    `package layerfs
import "testing"
func TestExcluded(t *testing.T) { t.Fatal("Linux-only package must stay excluded") }
`,
  );
  const resource = path.join(
    cwd,
    'apps/server/internal/resource/resource_test.go',
  );
  await writeFile(
    resource,
    `package resource
import "testing"
func TestAlpha(t *testing.T) {}
func TestBeta(t *testing.T) {}
`,
  );
  const events = [];
  const execute = (binary, args, options) => {
    const testing = args[0] === 'test';
    if (testing) {
      assert.ok(args.includes('-race'));
      assert.equal(args[args.indexOf('-p') + 1], '2');
      assert.ok(!args.some((arg) => arg.endsWith('/layerfs')));
    }
    const stdout = execFileSync(
      binary,
      testing && !args.includes('-list')
        ? ['test', '-json', ...args.slice(1)]
        : args,
      {
        ...options,
        cwd,
        stdio: ['ignore', 'pipe', 'pipe'],
        maxBuffer: 64 << 20,
      },
    );
    if (testing && !args.includes('-list'))
      events.push(
        ...stdout
          .trim()
          .split('\n')
          .map((line) => JSON.parse(line)),
      );
    return stdout;
  };
  for (const iteration of [1, 2]) {
    events.length = 0;
    checkPortableGo(execute);
    const ran = events.filter((event) => event.Action === 'run');
    for (const [suffix, names] of [
      [
        '',
        [
          'TestAlpha',
          'TestAlpha/nested',
          'Example',
          'FuzzSeed',
          'FuzzSeed/seed#0',
          'FuzzSeed/seed#1',
        ],
      ],
      ['/apps/server/internal/resource', ['TestAlpha', 'TestBeta']],
    ])
      for (const name of names)
        assert.equal(
          ran.filter(
            (event) =>
              event.Package === `github.com/cofy-x/camp${suffix}` &&
              event.Test === name,
          ).length,
          1,
          `${iteration}:${suffix}:${name}`,
        );
    assert.ok(events.every((event) => !event.Output?.includes('(cached)')));
  }
  await writeFile(
    resource,
    `package resource
import "testing"
func TestAlpha(t *testing.T) { t.Fatal("controlled real Go test failure") }
func TestBeta(t *testing.T) {}
`,
  );
  assert.throws(
    () => checkPortableGo(execute),
    (error) => error.status === 1,
  );
});

test('browser sharding keeps all projects, strict evidence uploads and a separate required verifier', async () => {
  const workflow = await readFile(
    new URL('../.github/workflows/ci.yml', import.meta.url),
    'utf8',
  );
  const browser = workflowJob(workflow, 'browser_project');
  assert.ok(browser.includes('project: [chromium, firefox, webkit, touch]'));
  assert.ok(browser.includes('shard: [1, 2]'));
  assert.ok(browser.includes('fail-fast: false'));
  assert.ok(
    browser.includes('--project="$BROWSER_PROJECT" --shard="$BROWSER_SHARD"'),
  );
  assert.doesNotMatch(browser, /--grep|--pass-with-no-tests/);
  assert.equal((browser.match(/if-no-files-found: error/g) ?? []).length, 3);
  assert.equal((browser.match(/success' && hashFiles/g) ?? []).length, 3);
  const verifier = workflowJob(workflow, 'browser_evidence');
  assert.ok(verifier.includes("needs.browser_project.result == 'success'"));
  assert.ok(verifier.includes('actions: read'));
  assert.ok(verifier.includes('node scripts/check-browser-shards.mjs'));
  const gate = workflowJob(workflow, 'browser');
  assert.ok(
    gate.includes('needs: [changes, browser_project, browser_evidence]'),
  );
  assert.ok(gate.includes('test "$EVIDENCE_RESULT" = success'));
  assert.ok(gate.includes('test "$EVIDENCE_RESULT" = skipped'));
});

test('Linux CI, checkpoint and release installations share one bounded dependency installer', async () => {
  for (const [name, count] of [
    ['ci.yml', 1],
    ['checkpoint-proof.yml', 1],
    ['release.yml', 2],
  ]) {
    const workflow = await readFile(
      new URL(`../.github/workflows/${name}`, import.meta.url),
      'utf8',
    );
    const installs = workflow
      .split('      - ')
      .filter((step) =>
        step.includes('run: node scripts/install-linux-deps.mjs '),
      );
    assert.equal(installs.length, count, name);
    for (const step of installs) assert.match(step, /timeout-minutes: 8/);
    assert.doesNotMatch(
      workflow,
      /apt-get|apt-mirrors\.txt|archive\.ubuntu\.com|--with-deps/,
    );
  }
});

test('browser jobs use an immutable official image matching the locked Playwright version, without reinstalling browsers', async () => {
  const workflow = await readFile(
    new URL('../.github/workflows/ci.yml', import.meta.url),
    'utf8',
  );
  const manifest = JSON.parse(
    await readFile(new URL('../package.json', import.meta.url), 'utf8'),
  );
  const browser = workflowJob(workflow, 'browser_project');
  const image = browser.match(/image: (\S+)/)[1];
  const version = manifest.devDependencies['@playwright/test'];
  assert.ok(
    image.startsWith(`mcr.microsoft.com/playwright:v${version}-noble@sha256:`),
  );
  assert.match(image, /@sha256:[0-9a-f]{64}$/);
  assert.match(browser, /options: --init --ipc=host/);
  assert.match(browser, /shell: bash/);
  assert.ok(
    browser.includes(
      'git config --global --add safe.directory "$GITHUB_WORKSPACE"',
    ),
  );
  assert.match(browser, /pnpm@12\.11\.2 install --frozen-lockfile/);
  assert.match(browser, /go mod download/);
  assert.match(browser, /pnpm@12\.11\.2 run test:e2e/);
  assert.doesNotMatch(
    browser,
    /playwright install|\bmake\b|--privileged|retries:/,
  );
});

test('checkpoint caches build inputs only and always requalifies, while fixture caches retain full recovery', async () => {
  const checkpoint = await readFile(
    new URL('../.github/workflows/checkpoint-proof.yml', import.meta.url),
    'utf8',
  );
  const build = checkpoint
    .split(
      '      - name: Build the pinned runtime and qualify public checkpoint operations\n',
    )[1]
    .split('      - name:')[0];
  assert.doesNotMatch(build, /\n\s+if:|cache-hit/);
  assert.ok(build.includes('make install-project-runtime install-layerfs'));
  assert.ok(
    build.includes(
      'node tools/layerfs-runtime/hybrid/probe.mjs dist/checkpoint-proof.json',
    ),
  );
  for (const name of [
    'source',
    'build/cargo',
    'build/rustup',
    'build/checkout',
  ])
    assert.equal(
      checkpoint.split(`.camp-layerfs/${name}\n`).length - 1,
      2,
      name,
    );
  const cached = checkpoint
    .split('uses: actions/cache/')
    .slice(1)
    .map((block) => block.split('      - name:')[0].split('      - uses:')[0])
    .join('\n');
  assert.doesNotMatch(cached, /installed\.json|\/\.data|\/dist/);
  assert.ok(checkpoint.includes('"$RUNNER_TEMP/camp-checkpoint-proof"'));
  assert.ok(
    checkpoint.includes(
      "github.event_name == 'push' && github.ref == 'refs/heads/main'",
    ),
  );
  assert.ok(
    checkpoint.includes(
      "hashFiles('.node-version', 'Makefile', '.github/workflows/checkpoint-proof.yml', 'tools/layerfs-runtime/**'",
    ),
  );
  assert.ok(checkpoint.includes('Return build-cache ownership'));
  const workflow = await readFile(
    new URL('../.github/workflows/ci.yml', import.meta.url),
    'utf8',
  );
  const fixture = workflowJob(workflow, 'fixture_checks');
  assert.ok(fixture.includes('load: true'));
  assert.ok(
    fixture.includes(
      "CACHE_WRITE: ${{ (github.event_name == 'push' && github.ref == 'refs/heads/main') || (github.event_name == 'workflow_dispatch' && inputs.populate_build_cache) }}",
    ),
  );
  assert.ok(fixture.includes("DOCKER_BUILD_RECORD_UPLOAD: 'false'"));
  assert.ok(fixture.includes('make test-remote REMOTE_ARGS=--skip-build'));
  assert.ok(
    fixture.includes('node --test tools/remote-dev/cache-plan.test.mjs'),
  );
});

test('build caches permit automatic main and explicit manual writes, while PRs and release callers remain read-only', async () => {
  const workflow = await readFile(
    new URL('../.github/workflows/ci.yml', import.meta.url),
    'utf8',
  );
  const expression = workflowJob(workflow, 'fixture_checks').match(
    /CACHE_WRITE: \$\{\{ (.+) \}\}/,
  )[1];
  const permits = new Function('github', 'inputs', `return ${expression};`);
  for (const event_name of [
    'push',
    'pull_request',
    'workflow_dispatch',
    'workflow_call',
  ])
    for (const ref of [
      'refs/heads/main',
      'refs/heads/feature',
      'refs/pull/1/merge',
    ])
      for (const populate_build_cache of [false, true])
        assert.equal(
          permits({ event_name, ref }, { populate_build_cache }),
          (event_name === 'push' && ref === 'refs/heads/main') ||
            (event_name === 'workflow_dispatch' && populate_build_cache),
          `${event_name} ${ref} ${populate_build_cache}`,
        );
  const checkpoint = await readFile(
    new URL('../.github/workflows/checkpoint-proof.yml', import.meta.url),
    'utf8',
  );
  assert.ok(checkpoint.includes(expression));
  assert.ok(
    workflowJob(workflow, 'checkpoint').includes(
      "populate_build_cache: ${{ github.event_name == 'workflow_dispatch' && inputs.populate_build_cache || false }}",
    ),
  );
  assert.match(
    workflow,
    /workflow_dispatch:\n {4}inputs:\n {6}populate_build_cache:[\s\S]+?type: boolean\n {8}default: false/,
  );
});

test('Mac build caches retain both hosts, forced native execution and complete package acceptance', async () => {
  const workflow = await readFile(
    new URL('../.github/workflows/native-mac.yml', import.meta.url),
    'utf8',
  );
  assert.ok(workflow.includes('host: [macos-15, macos-26]'));
  assert.ok(workflow.includes('fail-fast: false'));
  for (const name of [
    'Check actual read and network boundaries',
    'Check native command lifecycle and cleanup receipts',
    'Check actual native DSH, capture, continuation and collaboration',
    "Check the operator's retained local qualification command",
    'Build complete native Runner packages',
    'Check complete online and offline installation',
  ]) {
    const block = workflow
      .split(`      - name: ${name}\n`)[1]
      ?.split('      - name:')[0];
    assert.ok(block, name);
    assert.doesNotMatch(block, /cache-hit|\n {8}if:/);
  }
  const tests = workflow
    .split('\n')
    .filter((line) => /run: go test/.test(line));
  assert.equal(tests.length, 8);
  assert.ok(tests.every((line) => line.includes('-count=1')));
  assert.ok(workflow.includes('node scripts/check-native-packages.mjs'));
  assert.ok(workflow.includes('path: dist/native-mac/'));
  const paths = workflow
    .split('          path: |\n')[1]
    .split('          key:')[0];
  assert.ok(paths.includes('outputs.go-cache'));
  assert.ok(paths.includes('.data/native-runtime-compression'));
  assert.doesNotMatch(paths, /dist\/|installed\.json|journals|readiness/);
  assert.ok(
    workflow.includes(
      "(github.event_name == 'workflow_dispatch' && inputs.populate_build_cache)",
    ),
  );
  const caller = await readFile(
    new URL('../.github/workflows/ci.yml', import.meta.url),
    'utf8',
  );
  assert.ok(
    workflowJob(caller, 'native').includes(
      "populate_build_cache: ${{ github.event_name == 'workflow_dispatch' && inputs.populate_build_cache || false }}",
    ),
  );
});
