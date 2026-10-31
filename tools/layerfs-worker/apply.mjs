import { constants } from 'node:fs';
import { Buffer } from 'node:buffer';
import { chmod, link, mkdir, open, rm, rmdir, symlink } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { performance } from 'node:perf_hooks';
import { failure } from './state.mjs';
import { manifestDigest, scanTree, validateManifest } from './tree.mjs';

const equivalent = (a, b) => JSON.stringify(a) === JSON.stringify(b);

export function neededBlobs(previous, next) {
  const before = new Map(
    validateManifest(previous).entries.map((row) => [row.path, row]),
  );
  return [
    ...new Set(
      validateManifest(next)
        .entries.filter(
          (row) =>
            row.type === 'file' &&
            (before.get(row.path)?.type !== 'file' ||
              before.get(row.path)?.sha256 !== row.sha256),
        )
        .map((row) => row.sha256),
    ),
  ];
}

async function verifiedBlob(file, row) {
  const handle = await open(file, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const info = await handle.stat();
    if (!info.isFile() || info.size !== row.bytes)
      throw failure(
        'Corrupt',
        'Apply input has the wrong blob type or length.',
      );
    const hasher = createHash('sha256'),
      buffer = Buffer.allocUnsafe(256 << 10);
    for (;;) {
      const { bytesRead } = await handle.read(buffer, 0, buffer.length, null);
      if (!bytesRead) break;
      hasher.update(buffer.subarray(0, bytesRead));
    }
    if (hasher.digest('hex') !== row.sha256)
      throw failure('Corrupt', 'Apply input does not match its content hash.');
  } finally {
    await handle.close();
  }
}

// Only the trusted Linux administrative helper calls this on a mounted view.
// No shell/path from an Agent is accepted. A failed apply never implies Commit.
export async function applyTree(
  directory,
  previousDigest,
  capture,
  { failAfter } = {},
) {
  const started = performance.now();
  const next = validateManifest(capture.manifest);
  if (manifestDigest(next) !== capture.digest)
    throw failure('Corrupt', 'Apply manifest digest changed.');
  const before = await scanTree(directory);
  if (before.digest !== previousDigest)
    throw failure(
      'Conflict',
      'The mounted parent differs from the expected checkpoint.',
    );
  if (before.digest === capture.digest)
    return {
      applyMs: 0,
      beforeReadbackMs: before.metrics.scanMs,
      afterReadbackMs: 0,
      changedFiles: 0,
      copiedBytes: 0,
      digest: before.digest,
      unchanged: true,
    };
  const needed = new Set(neededBlobs(before.manifest, next));
  for (const row of next.entries)
    if (row.type === 'file' && needed.has(row.sha256))
      await verifiedBlob(path.join(capture.root, 'blobs', row.sha256), row);
  const old = new Map(before.manifest.entries.map((row) => [row.path, row]));
  const after = new Map(next.entries.map((row) => [row.path, row]));
  const metrics = {
    applyMs: 0,
    beforeReadbackMs: before.metrics.scanMs,
    changedFiles: 0,
    copiedBytes: 0,
  };
  let effects = 0;
  const effect = () => {
    effects++;
    if (failAfter !== undefined && effects >= failAfter)
      throw failure(
        'InjectedApplyFailure',
        'Controlled failure after a filesystem effect.',
      );
  };
  // Make only existing owned directories writable. The initial manifest proves
  // each traversed parent is a directory, never a symlink.
  await chmod(directory, 0o700);
  for (const row of before.manifest.entries)
    if (row.type === 'directory')
      await chmod(path.join(directory, row.path), 0o700);
  for (const row of [...before.manifest.entries].reverse()) {
    const replacement = after.get(row.path);
    const remove =
      !replacement ||
      replacement.type !== row.type ||
      row.type === 'symlink' ||
      row.type === 'hardlink' ||
      (row.type === 'file' && replacement.sha256 !== row.sha256);
    if (remove) {
      if (row.type === 'directory') await rmdir(path.join(directory, row.path));
      else await rm(path.join(directory, row.path));
      effect();
    }
  }
  for (const row of next.entries) {
    const destination = path.join(directory, row.path),
      original = old.get(row.path);
    if (row.type === 'directory') {
      if (original?.type !== 'directory') {
        await mkdir(destination, { mode: 0o700 });
        effect();
      }
    } else if (row.type === 'file') {
      if (original?.type !== 'file' || original.sha256 !== row.sha256) {
        const input = await open(
          path.join(capture.root, 'blobs', row.sha256),
          constants.O_RDONLY | constants.O_NOFOLLOW,
        );
        const output = await open(destination, 'wx', row.mode);
        try {
          const buffer = Buffer.allocUnsafe(256 << 10);
          for (;;) {
            const { bytesRead } = await input.read(
              buffer,
              0,
              buffer.length,
              null,
            );
            if (!bytesRead) break;
            for (let offset = 0; offset < bytesRead;)
              offset += (await output.write(buffer, offset, bytesRead - offset))
                .bytesWritten;
            metrics.copiedBytes += bytesRead;
          }
          await output.sync();
          metrics.changedFiles++;
          effect();
        } finally {
          await input.close();
          await output.close();
        }
      }
      await chmod(destination, row.mode);
    } else if (row.type === 'symlink') {
      await symlink(Buffer.from(row.target, 'base64'), destination);
      effect();
    } else if (row.type === 'hardlink') {
      await link(path.join(directory, row.target), destination);
      effect();
    }
  }
  for (const row of [...next.entries].reverse())
    if (row.type === 'directory')
      await chmod(path.join(directory, row.path), row.mode);
  await chmod(directory, next.rootMode);
  metrics.applyMs = performance.now() - started - before.metrics.scanMs;
  const verified = await scanTree(directory);
  if (verified.digest !== capture.digest)
    throw failure(
      'Mismatch',
      'Applied filesystem differs from the complete-tree snapshot.',
    );
  return {
    ...metrics,
    afterReadbackMs: verified.metrics.scanMs,
    digest: verified.digest,
    unchanged: equivalent(before.manifest, next),
  };
}
