import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { openHome } from './home.mjs';
import { manifestDigest, scanTree } from './tree.mjs';
import { saveState } from './state.mjs';

assert.equal(process.platform, 'darwin');
assert.equal(process.arch, 'arm64');
const root = await mkdtemp(path.resolve('.data/apfs-worker-'));
const mount = path.join(root, 'mounted'),
  image = path.join(root, 'private-case-sensitive.sparseimage');
await mkdir(mount, { mode: 0o700 });
const report = {
  schema: 1,
  root,
  passed: false,
  scope:
    'Actual owned APFS case-sensitive disk image; Linux Store remains on the selected engine local filesystem',
  modelRequests: 0,
};
let device, active;
const plist = (xml) =>
  JSON.parse(
    execFileSync(
      '/usr/bin/plutil',
      ['-convert', 'json', '-o', '-', '--', '-'],
      {
        input: xml,
        encoding: 'utf8',
      },
    ),
  );
try {
  execFileSync(
    '/usr/bin/hdiutil',
    [
      'create',
      '-size',
      '1g',
      '-type',
      'SPARSE',
      '-fs',
      'Case-sensitive APFS',
      '-volname',
      'Camp44PrivateCase',
      image,
    ],
    { stdio: 'pipe' },
  );
  const attached = plist(
    execFileSync('/usr/bin/hdiutil', [
      'attach',
      '-nobrowse',
      '-noautoopen',
      '-mountpoint',
      mount,
      '-plist',
      image,
    ]),
  );
  const volume = attached['system-entities'].find(
    (entry) => entry['mount-point'] === mount,
  );
  assert.ok(volume);
  device = attached['system-entities'].find((entry) =>
    /^\/dev\/disk[0-9]+$/.test(entry['dev-entry'] ?? ''),
  )?.['dev-entry'];
  assert.ok(
    device,
    'The owned image must have an identified whole-disk device for scoped detach.',
  );
  report.filesystem = plist(
    execFileSync('/usr/sbin/diskutil', ['info', '-plist', mount]),
  );
  const parent = path.join(mount, 'private-restores');
  await mkdir(parent, { mode: 0o700 });
  await writeFile(path.join(parent, 'existing-marker'), 'unchanged');
  const home = path.join(root, 'worker');
  await mkdir(home, { mode: 0o700 });
  const socket = execFileSync(
    'docker',
    ['context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'],
    { encoding: 'utf8' },
  )
    .trim()
    .slice(7);
  active = await openHome({
    dataRoot: root,
    home,
    docker: socket,
    runnerId: 'controlled-apfs-runner',
    storageId: 'controlled-apfs-storage',
  });
  report.worker = active.worker.status;
  for (const [label, names] of [
    ['case-sensitive', ['A', 'a']],
    ['unicode-normalization', ['é', 'e\u0301']],
  ]) {
    const captureRoot = path.join(home, label);
    await mkdir(path.join(captureRoot, 'blobs'), {
      recursive: true,
      mode: 0o700,
    });
    const entries = [];
    for (const [index, name] of names.entries()) {
      const bytes = Buffer.from(`distinct Linux member ${index}`),
        sha256 = createHash('sha256').update(bytes).digest('hex');
      await writeFile(path.join(captureRoot, 'blobs', sha256), bytes, {
        mode: 0o600,
      });
      entries.push({
        path: name,
        type: 'file',
        mode: 0o600,
        bytes: bytes.length,
        sha256,
      });
    }
    entries.sort((a, b) =>
      Buffer.compare(Buffer.from(a.path), Buffer.from(b.path)),
    );
    const manifest = { schema: 1, rootMode: 0o700, entries },
      digest = manifestDigest(manifest);
    await saveState(path.join(captureRoot, 'manifest.json'), manifest);
    await saveState(path.join(captureRoot, 'receipt.json'), {
      state: 'sealed',
      digest,
    });
    const checkpoint = await active.checkpoints.submitCapture(
      { root: captureRoot, manifest, digest },
      createHash('sha256').update(label).digest('hex'),
      label,
    );
    if (label === 'case-sensitive') {
      const restored = await active.checkpoints.restore(
        checkpoint.binding.operationId,
        parent,
      );
      assert.equal((await scanTree(restored.root)).digest, digest);
      report.caseSensitive = {
        passed: true,
        digest,
        operationId: checkpoint.binding.operationId,
      };
    } else {
      await assert.rejects(
        active.checkpoints.restore(checkpoint.binding.operationId, parent),
        { code: 'NameConflict' },
      );
      report.unicodeConflictRejected = true;
    }
    assert.equal(
      await readFile(path.join(parent, 'existing-marker'), 'utf8'),
      'unchanged',
    );
  }
  report.passed = true;
} catch (error) {
  report.error = { code: error.code, message: error.message };
} finally {
  try {
    await active?.worker.close();
  } catch (error) {
    report.workerCloseError = error.message;
    report.passed = false;
  }
  if (device) {
    try {
      execFileSync('/usr/bin/hdiutil', ['detach', device], { stdio: 'pipe' });
      report.detachedOwnedImage = true;
    } catch (error) {
      report.detachError = error.message;
      report.passed = false;
    }
  }
  await saveState(path.join(root, 'report.json'), report);
  process.stdout.write(
    `Actual APFS qualification ${report.passed ? 'passed' : 'failed'}: ${path.join(root, 'report.json')}\n`,
  );
  if (!report.passed) process.exitCode = 1;
}
