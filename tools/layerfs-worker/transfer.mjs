import { constants } from 'node:fs';
import { Buffer } from 'node:buffer';
import { createHash, randomUUID } from 'node:crypto';
import { mkdir, open, rename, rm } from 'node:fs/promises';
import path from 'node:path';
import { performance } from 'node:perf_hooks';
import { failure, syncDirectory } from './state.mjs';
import { LIMITS, manifestDigest, validateManifest } from './tree.mjs';

const CHUNK = 256 << 10;
const HEADER_LIMIT = LIMITS.manifestBytes + (32 << 20);

export function blobIndex(manifest) {
  const index = new Map();
  for (const row of validateManifest(manifest).entries)
    if (row.type === 'file') {
      if (index.has(row.sha256) && index.get(row.sha256) !== row.bytes)
        throw failure(
          'InvalidSnapshot',
          'A content digest has conflicting lengths.',
        );
      index.set(row.sha256, row.bytes);
    }
  return index;
}

// One bounded metadata header and raw file bytes, with Writable/HTTP backpressure.
// A transport interruption never reaches apply; the original capture is retained.
export async function* encodeCapture(
  capture,
  wanted = [...blobIndex(capture.manifest).keys()],
  metrics = {},
) {
  const index = blobIndex(capture.manifest);
  if (
    wanted.length !== new Set(wanted).size ||
    wanted.some((digest) => !index.has(digest))
  )
    throw failure('InvalidInput', 'Invalid requested blob set.');
  const serializing = performance.now();
  const header = Buffer.from(
    JSON.stringify({
      schema: 1,
      manifest: capture.manifest,
      digest: capture.digest,
      blobs: wanted,
    }),
  );
  if (
    header.length > HEADER_LIMIT ||
    manifestDigest(capture.manifest) !== capture.digest
  )
    throw failure('Corrupt', 'Invalid capture header.');
  const prefix = Buffer.alloc(4);
  prefix.writeUInt32BE(header.length);
  Object.assign(metrics, {
    serializationMs: performance.now() - serializing,
    wireBytes: header.length + 4,
    payloadBytes: 0,
    headerBytes: header.length + 4,
  });
  yield prefix;
  yield header;
  for (const digest of wanted) {
    const input = await open(
      path.join(capture.root, 'blobs', digest),
      constants.O_RDONLY | constants.O_NOFOLLOW,
    );
    const hasher = createHash('sha256');
    let bytes = 0;
    try {
      const info = await input.stat();
      if (!info.isFile() || info.size !== index.get(digest))
        throw failure('Corrupt', 'Capture blob changed before transfer.');
      for (;;) {
        // Each yielded chunk owns its memory until its consumer drains it.
        const buffer = Buffer.allocUnsafe(CHUNK);
        const { bytesRead } = await input.read(buffer, 0, buffer.length, null);
        if (!bytesRead) break;
        const chunk = buffer.subarray(0, bytesRead);
        hasher.update(chunk);
        bytes += bytesRead;
        metrics.wireBytes += bytesRead;
        metrics.payloadBytes += bytesRead;
        yield chunk;
      }
      if (bytes !== index.get(digest) || hasher.digest('hex') !== digest)
        throw failure('Corrupt', 'Capture blob changed during transfer.');
    } finally {
      await input.close();
    }
  }
}

export class ByteReader {
  constructor(stream) {
    this.iterator = stream[Symbol.asyncIterator]();
    this.pending = Buffer.alloc(0);
    this.bytes = 0;
  }
  async next() {
    while (!this.pending.length) {
      const next = await this.iterator.next();
      if (next.done)
        throw failure(
          'Truncated',
          'Snapshot transfer ended before its declared length.',
        );
      this.pending = Buffer.from(next.value);
    }
  }
  async consume(length, write) {
    while (length) {
      await this.next();
      const size = Math.min(length, this.pending.length);
      await write(this.pending.subarray(0, size));
      this.pending = this.pending.subarray(size);
      this.bytes += size;
      length -= size;
    }
  }
  async take(length) {
    const parts = [];
    await this.consume(length, (chunk) => {
      parts.push(chunk);
    });
    return Buffer.concat(parts, length);
  }
  async end() {
    if (this.pending.length)
      throw failure(
        'InvalidSnapshot',
        'Snapshot transfer contains trailing bytes.',
      );
    for (;;) {
      const next = await this.iterator.next();
      if (next.done) return;
      if (next.value.length)
        throw failure(
          'InvalidSnapshot',
          'Snapshot transfer contains trailing bytes.',
        );
    }
  }
}

export async function receiveCapture(stream, root, expected) {
  const started = performance.now();
  const reader = new ByteReader(stream);
  const size = (await reader.take(4)).readUInt32BE(0);
  if (size > HEADER_LIMIT)
    throw failure('Capacity', 'Snapshot header exceeds its byte limit.');
  let header;
  try {
    header = JSON.parse((await reader.take(size)).toString('utf8'));
  } catch (error) {
    if (error.code === 'Truncated') throw error;
    throw failure('InvalidSnapshot', 'Snapshot header is not JSON.');
  }
  const manifest = validateManifest(header.manifest);
  const digest = manifestDigest(manifest);
  if (
    header.schema !== 1 ||
    header.digest !== digest ||
    expected.digest !== digest
  )
    throw failure(
      'Conflict',
      'Snapshot digest does not match the prepared operation.',
    );
  const index = blobIndex(manifest),
    wanted = header.blobs;
  if (
    !Array.isArray(wanted) ||
    wanted.length !== new Set(wanted).size ||
    wanted.some((item) => !index.has(item)) ||
    (expected.blobs &&
      JSON.stringify(wanted) !== JSON.stringify(expected.blobs))
  )
    throw failure(
      'InvalidSnapshot',
      'Snapshot blob membership differs from the upload intent.',
    );
  const blobs = path.join(root, 'blobs');
  await mkdir(blobs, { recursive: true, mode: 0o700 });
  const pending = new Set();
  let sealFailure;
  try {
    for (const name of wanted) {
      if (sealFailure) throw sealFailure;
      const temporary = path.join(blobs, `${randomUUID()}.part`);
      const file = await open(temporary, 'wx', 0o600);
      const hasher = createHash('sha256');
      try {
        await reader.consume(index.get(name), async (chunk) => {
          hasher.update(chunk);
          for (let offset = 0; offset < chunk.length;)
            offset += (await file.write(chunk, offset, chunk.length - offset))
              .bytesWritten;
        });
        if (hasher.digest('hex') !== name)
          throw failure(
            'Corrupt',
            'Transferred blob does not match its digest.',
          );
        // The raw stream remains ordered; only completed, hash-validated files
        // synchronize concurrently, with bounded descriptors and no early apply.
        const task = (async () => {
          try {
            await file.sync();
            await file.close();
            await rename(temporary, path.join(blobs, name));
          } catch (error) {
            await file.close().catch(() => {});
            await rm(temporary, { force: true });
            throw error;
          }
        })()
          .catch((error) => {
            sealFailure ??= error;
          })
          .finally(() => pending.delete(task));
        pending.add(task);
        if (pending.size >= 8) await Promise.race(pending);
      } catch (error) {
        await file.close().catch(() => {});
        await rm(temporary, { force: true });
        throw error;
      }
    }
    await reader.end();
  } finally {
    await Promise.all(pending);
  }
  if (sealFailure) throw sealFailure;
  await syncDirectory(blobs);
  return {
    manifest,
    digest,
    metrics: {
      receiveMs: performance.now() - started,
      syncConcurrency: 8,
      wireBytes: reader.bytes,
      payloadBytes: wanted.reduce((sum, name) => sum + index.get(name), 0),
    },
  };
}
