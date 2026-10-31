import { constants } from 'node:fs';
import { Buffer } from 'node:buffer';
import { TextDecoder } from 'node:util';
import { createHash, randomUUID } from 'node:crypto';
import {
  chmod,
  link,
  lstat,
  mkdir,
  open,
  readFile,
  readdir,
  readlink,
  rename,
  rm,
  symlink,
} from 'node:fs/promises';
import path from 'node:path';
import { performance } from 'node:perf_hooks';
import { failure, saveState, syncDirectory } from './state.mjs';

export const LIMITS = Object.freeze({
  entries: 250_000,
  bytes: 2 ** 31,
  manifestBytes: 64 << 20,
  pathBytes: 4096,
});
const compare = (a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b));
const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
const decoder = new TextDecoder('utf-8', { fatal: true });
const mode = (info) => Number(info.mode & 0o777n);
const signature = (info) =>
  [
    info.dev,
    info.ino,
    info.mode,
    info.nlink,
    info.size,
    info.mtimeNs,
    info.ctimeNs,
  ].join(':');
const inside = (parent, child) => {
  const relative = path.relative(parent, child);
  return (
    relative === '' ||
    (!relative.startsWith(`..${path.sep}`) &&
      relative !== '..' &&
      !path.isAbsolute(relative))
  );
};

export function manifestDigest(manifest) {
  return hash(Buffer.from(JSON.stringify(validateManifest(manifest))));
}

function exactKeys(value, keys) {
  return (
    value &&
    typeof value === 'object' &&
    !Array.isArray(value) &&
    Object.keys(value).sort().join(',') === keys.sort().join(',')
  );
}

export function validateManifest(manifest, limits = LIMITS) {
  if (
    !exactKeys(manifest, ['schema', 'rootMode', 'entries']) ||
    manifest.schema !== 1 ||
    !Array.isArray(manifest.entries) ||
    manifest.entries.length > limits.entries
  )
    throw failure('InvalidSnapshot', 'Invalid complete-tree manifest.');
  const permission = (n) => Number.isInteger(n) && n >= 0 && n <= 0o777;
  if (!permission(manifest.rootMode))
    throw failure('InvalidSnapshot', 'Invalid root permissions.');
  const seen = new Map();
  let bytes = 0,
    previous = '';
  const entries = manifest.entries.map((row) => {
    if (
      !row ||
      typeof row.path !== 'string' ||
      !row.path ||
      row.path.includes('\0') ||
      Buffer.byteLength(row.path) > limits.pathBytes ||
      row.path
        .split('/')
        .some((part) => !part || part === '.' || part === '..') ||
      row.path.startsWith('/') ||
      row.path.includes('\\') ||
      !permission(row.mode)
    )
      throw failure(
        'InvalidSnapshot',
        'Invalid path or permissions in complete-tree manifest.',
      );
    // JSON must not introduce an unpaired UTF-16 surrogate that is replaced on disk.
    if (
      decoder.decode(Buffer.from(row.path)) !== row.path ||
      (previous && compare(previous, row.path) >= 0)
    )
      throw failure(
        'InvalidSnapshot',
        'Snapshot paths must be exact, unique UTF-8 names in byte order.',
      );
    const parent = path.posix.dirname(row.path);
    if (parent !== '.' && seen.get(parent)?.type !== 'directory')
      throw failure(
        'InvalidSnapshot',
        'A snapshot parent is absent or is a link.',
      );
    let normalized;
    if (row.type === 'directory' && exactKeys(row, ['path', 'type', 'mode']))
      normalized = { path: row.path, type: row.type, mode: row.mode };
    else if (
      row.type === 'file' &&
      exactKeys(row, ['path', 'type', 'mode', 'bytes', 'sha256']) &&
      Number.isSafeInteger(row.bytes) &&
      row.bytes >= 0 &&
      /^[a-f0-9]{64}$/.test(row.sha256)
    ) {
      bytes += row.bytes;
      normalized = {
        path: row.path,
        type: row.type,
        mode: row.mode,
        bytes: row.bytes,
        sha256: row.sha256,
      };
    } else if (
      row.type === 'symlink' &&
      exactKeys(row, ['path', 'type', 'mode', 'target']) &&
      row.mode === 0o777 &&
      typeof row.target === 'string' &&
      row.target.length <= limits.pathBytes * 2
    ) {
      const target = Buffer.from(row.target, 'base64');
      if (
        !target.length ||
        target.includes(0) ||
        target.toString('base64') !== row.target
      )
        throw failure('InvalidSnapshot', 'Invalid opaque symlink target.');
      normalized = {
        path: row.path,
        type: row.type,
        mode: row.mode,
        target: row.target,
      };
    } else if (
      row.type === 'hardlink' &&
      exactKeys(row, ['path', 'type', 'mode', 'target']) &&
      seen.get(row.target)?.type === 'file' &&
      seen.get(row.target).mode === row.mode
    )
      normalized = {
        path: row.path,
        type: row.type,
        mode: row.mode,
        target: row.target,
      };
    else
      throw failure(
        'InvalidSnapshot',
        'Unsupported or malformed snapshot entry.',
      );
    if (bytes > limits.bytes)
      throw failure(
        'Capacity',
        'Complete-tree snapshot exceeds its byte limit.',
      );
    seen.set(row.path, normalized);
    previous = row.path;
    return normalized;
  });
  const normalized = { schema: 1, rootMode: manifest.rootMode, entries };
  if (Buffer.byteLength(JSON.stringify(normalized)) > limits.manifestBytes)
    throw failure('Capacity', 'Complete-tree manifest exceeds its byte limit.');
  return normalized;
}

async function content(file, expected, blobDirectory, metrics) {
  const started = performance.now();
  const input = await open(file, constants.O_RDONLY | constants.O_NOFOLLOW);
  const hasher = createHash('sha256');
  let output,
    temporary,
    length = 0;
  try {
    if (signature(await input.stat({ bigint: true })) !== signature(expected))
      throw failure('Changed', 'File changed before capture.');
    if (blobDirectory) {
      temporary = path.join(blobDirectory, `${randomUUID()}.part`);
      output = await open(temporary, 'wx', 0o600);
    }
    const buffer = Buffer.allocUnsafe(256 << 10);
    for (;;) {
      const { bytesRead } = await input.read(buffer, 0, buffer.length, null);
      if (!bytesRead) break;
      hasher.update(buffer.subarray(0, bytesRead));
      if (output) {
        const copying = performance.now();
        for (let position = 0; position < bytesRead;) {
          const written = await output.write(
            buffer,
            position,
            bytesRead - position,
          );
          position += written.bytesWritten;
        }
        metrics.copyMs += performance.now() - copying;
      }
      length += bytesRead;
      if (length > Number(expected.size))
        throw failure('Changed', 'File grew during capture.');
    }
    if (
      length !== Number(expected.size) ||
      signature(await input.stat({ bigint: true })) !== signature(expected)
    )
      throw failure('Changed', 'File changed during capture.');
    const digest = hasher.digest('hex');
    if (output) {
      await output.sync();
      await output.close();
      output = null;
      await rename(temporary, path.join(blobDirectory, digest));
      temporary = null;
      metrics.copiedBytes += length;
    }
    metrics.hashedBytes += length;
    return digest;
  } finally {
    await input.close();
    await output?.close();
    if (temporary) await rm(temporary, { force: true });
    metrics.hashAndReadMs += performance.now() - started;
  }
}

// The caller must seal tool admission and establish descendant cleanup first.
// Stat witnesses detect accidental writers; they do not replace that boundary.
export async function scanTree(
  directory,
  { blobDirectory, limits = LIMITS } = {},
) {
  const started = performance.now();
  const metrics = {
    scanMs: 0,
    hashAndReadMs: 0,
    copyMs: 0,
    copiedBytes: 0,
    hashedBytes: 0,
    files: 0,
  };
  const entries = [],
    witnesses = new Map(),
    groups = new Map();
  const rootInfo = await lstat(directory, { bigint: true });
  if (!rootInfo.isDirectory() || rootInfo.mode & 0o7000n)
    throw failure(
      'UnsupportedEntry',
      'Snapshot root must be an ordinary directory without special permission bits.',
    );
  const rootSignature = signature(rootInfo);
  let totalBytes = 0;

  async function walk(relative) {
    const absolute = path.join(directory, relative);
    const names = (await readdir(absolute, { encoding: 'buffer' }))
      .map((bytes) => {
        try {
          const name = decoder.decode(bytes);
          if (name.includes('\\')) throw new Error();
          return name;
        } catch {
          throw failure(
            'UnsupportedName',
            'Snapshot names must be lossless UTF-8 without backslashes.',
          );
        }
      })
      .sort(compare);
    for (const name of names) {
      const local = relative ? `${relative}/${name}` : name;
      const file = path.join(directory, local);
      const info = await lstat(file, { bigint: true });
      if (info.mode & 0o7000n)
        throw failure(
          'UnsupportedEntry',
          'Special permission bits are not supported by this checkpoint profile.',
        );
      witnesses.set(local, signature(info));
      const row = { path: local, type: '', mode: mode(info) };
      if (info.isDirectory()) row.type = 'directory';
      else if (info.isSymbolicLink()) {
        row.type = 'symlink';
        row.mode = 0o777;
        row.target = (await readlink(file, { encoding: 'buffer' })).toString(
          'base64',
        );
      } else if (info.isFile()) {
        const inode = `${info.dev}:${info.ino}`;
        const existing = groups.get(inode);
        if (existing) {
          existing.count++;
          row.type = 'hardlink';
          row.target = existing.path;
          existing.rows.push(row);
        } else {
          if (
            info.size > BigInt(limits.bytes) ||
            totalBytes + Number(info.size) > limits.bytes
          )
            throw failure(
              'Capacity',
              'Complete-tree snapshot exceeds its byte limit.',
            );
          totalBytes += Number(info.size);
          row.type = 'file';
          row.bytes = Number(info.size);
          row.sha256 = await content(file, info, blobDirectory, metrics);
          groups.set(inode, {
            path: local,
            count: 1,
            expected: info.nlink,
            rows: [row],
          });
          metrics.files++;
        }
      } else
        throw failure(
          'UnsupportedEntry',
          'Sockets, FIFOs, devices and other special files cannot be checkpointed.',
        );
      entries.push(row);
      if (entries.length > limits.entries)
        throw failure(
          'Capacity',
          'Complete-tree snapshot exceeds its entry limit.',
        );
      if (info.isDirectory()) await walk(local);
    }
  }

  await walk('');
  for (const group of groups.values()) {
    if (BigInt(group.count) !== group.expected)
      throw failure(
        'ExternalHardlink',
        'A file has aliases outside the sealed working tree.',
      );
    const source = { ...group.rows[0] };
    const rows = group.rows.sort((a, b) => compare(a.path, b.path));
    for (const [index, row] of rows.entries()) {
      delete row.target;
      delete row.bytes;
      delete row.sha256;
      if (index === 0) {
        row.type = 'file';
        row.bytes = source.bytes;
        row.sha256 = source.sha256;
      } else {
        row.type = 'hardlink';
        row.target = rows[0].path;
      }
    }
  }
  // Repeat namespace enumeration as well as metadata checks: removals, additions
  // and directory replacement cannot silently disappear from the manifest.
  let observed = 0;

  async function recheck(relative) {
    for (const bytes of await readdir(path.join(directory, relative), {
      encoding: 'buffer',
    })) {
      const name = decoder.decode(bytes),
        local = relative ? `${relative}/${name}` : name;
      const info = await lstat(path.join(directory, local), { bigint: true });
      if (witnesses.get(local) !== signature(info))
        throw failure('Changed', 'Working tree changed during capture.');
      observed++;
      if (info.isDirectory()) await recheck(local);
    }
  }

  await recheck('');
  if (
    observed !== witnesses.size ||
    signature(await lstat(directory, { bigint: true })) !== rootSignature
  )
    throw failure('Changed', 'Working tree membership changed during capture.');
  const manifest = validateManifest(
    {
      schema: 1,
      rootMode: mode(rootInfo),
      entries: entries.sort((a, b) => compare(a.path, b.path)),
    },
    limits,
  );
  metrics.scanMs = performance.now() - started;
  return { manifest, digest: manifestDigest(manifest), metrics };
}

export async function captureTree(directory, parent, options = {}) {
  directory = path.resolve(directory);
  parent = path.resolve(parent);
  if (inside(directory, parent))
    throw failure(
      'InvalidInput',
      'A retained capture must be outside the writable workspace.',
    );
  await mkdir(parent, { recursive: true, mode: 0o700 });
  const root = path.join(parent, `capture-${randomUUID()}`);
  const blobs = path.join(root, 'blobs');
  await mkdir(root, { mode: 0o700 });
  await mkdir(blobs, { mode: 0o700 });
  try {
    const captured = await scanTree(directory, {
      ...options,
      blobDirectory: blobs,
    });
    await syncDirectory(blobs);
    await saveState(path.join(root, 'manifest.json'), captured.manifest);
    await saveState(path.join(root, 'receipt.json'), {
      schema: 1,
      digest: captured.digest,
      state: 'sealed',
    });
    await syncDirectory(parent);
    return { root, ...captured };
  } catch (error) {
    await saveState(path.join(root, 'receipt.json'), {
      schema: 1,
      state: 'rejected',
      code: error.code ?? 'Failed',
    });
    error.capture = root;
    throw error;
  }
}

export async function loadCapture(root) {
  const manifest = validateManifest(
    JSON.parse(await readFile(path.join(root, 'manifest.json'), 'utf8')),
  );
  return { root, manifest, digest: manifestDigest(manifest) };
}

// Build every exact destination name first. On APFS this catches case and
// canonical-Unicode aliases using the destination filesystem itself, before
// file bytes or links are installed. Only a new, private staging tree is used.
export async function restoreTree(capture, parent) {
  const started = performance.now();
  const manifest = validateManifest(capture.manifest);
  if (capture.digest !== manifestDigest(manifest))
    throw failure('Corrupt', 'Capture manifest digest changed.');
  await mkdir(parent, { recursive: true, mode: 0o700 });
  const root = path.join(parent, `restore-${randomUUID()}`);
  await mkdir(root, { mode: 0o700 });
  try {
    for (const row of manifest.entries) {
      const destination = path.join(root, row.path);
      if (row.type === 'directory') await mkdir(destination, { mode: 0o700 });
      else {
        const placeholder = await open(destination, 'wx', 0o600);
        await placeholder.close();
      }
    }
    for (const row of manifest.entries) {
      const destination = path.join(root, row.path);
      if (row.type === 'file') {
        const info = await lstat(path.join(capture.root, 'blobs', row.sha256), {
          bigint: true,
        });
        if (!info.isFile() || Number(info.size) !== row.bytes)
          throw failure(
            'Corrupt',
            'Capture blob is missing or has the wrong size.',
          );
        const input = await open(
          path.join(capture.root, 'blobs', row.sha256),
          constants.O_RDONLY | constants.O_NOFOLLOW,
        );
        const output = await open(
          destination,
          constants.O_WRONLY | constants.O_NOFOLLOW,
        );
        const digest = createHash('sha256');
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
            digest.update(buffer.subarray(0, bytesRead));
            for (let offset = 0; offset < bytesRead;)
              offset += (await output.write(buffer, offset, bytesRead - offset))
                .bytesWritten;
          }
          if (digest.digest('hex') !== row.sha256)
            throw failure('Corrupt', 'Capture blob hash changed.');
          await output.chmod(row.mode);
          await output.sync();
        } finally {
          await input.close();
          await output.close();
        }
      } else if (row.type === 'symlink') {
        await rm(destination);
        await symlink(Buffer.from(row.target, 'base64'), destination);
      } else if (row.type === 'hardlink') {
        await rm(destination);
        await link(path.join(root, row.target), destination);
      }
    }
    for (const row of [...manifest.entries].reverse())
      if (row.type === 'directory')
        await chmod(path.join(root, row.path), row.mode);
    await chmod(root, manifest.rootMode);
    const verified = await scanTree(root);
    if (verified.digest !== capture.digest)
      throw failure(
        'Mismatch',
        'Restored working tree does not match the sealed capture.',
      );
    await syncDirectory(root);
    return {
      root,
      restoreMs: performance.now() - started,
      readbackMs: verified.metrics.scanMs,
    };
  } catch (error) {
    // Retain the capture and rejected staging tree for diagnosis. Never touch a
    // pre-existing destination or silently rename a conflicting source entry.
    error.restore = root;
    if (error.code === 'EEXIST')
      throw Object.assign(
        failure(
          'NameConflict',
          'The destination filesystem cannot represent the exact snapshot names.',
        ),
        { restore: root },
      );
    throw error;
  }
}
