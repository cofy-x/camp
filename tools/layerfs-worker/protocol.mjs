import { Buffer } from 'node:buffer';
import { failure } from './state.mjs';
import { LIMITS } from './tree.mjs';

export const MAX_MESSAGE = LIMITS.manifestBytes + (32 << 20);

export async function writeBytes(stream, bytes) {
  await new Promise((resolve, reject) =>
    stream.write(bytes, (error) => (error ? reject(error) : resolve())),
  );
}

export async function writeMessage(stream, value) {
  const bytes = Buffer.from(JSON.stringify(value));
  if (bytes.length > MAX_MESSAGE)
    throw failure('Capacity', 'Worker metadata exceeds its byte limit.');
  const head = Buffer.alloc(4);
  head.writeUInt32BE(bytes.length);
  await writeBytes(stream, head);
  await writeBytes(stream, bytes);
}

export async function readMessage(reader, allowEOF = false) {
  const before = reader.bytes;
  let head;
  try {
    head = await reader.take(4);
  } catch (error) {
    if (allowEOF && error.code === 'Truncated' && reader.bytes === before)
      return null;
    throw error;
  }
  const length = head.readUInt32BE(0);
  if (length > MAX_MESSAGE)
    throw failure('Capacity', 'Worker metadata exceeds its byte limit.');
  try {
    return JSON.parse((await reader.take(length)).toString('utf8'));
  } catch (error) {
    if (error.code === 'Truncated') throw error;
    throw failure('Protocol', 'Worker metadata is not JSON.');
  }
}

export async function* body(reader, length) {
  if (
    !Number.isSafeInteger(length) ||
    length < 0 ||
    length > LIMITS.bytes + MAX_MESSAGE
  )
    throw failure('Capacity', 'Invalid worker transfer length.');
  while (length) {
    const size = Math.min(256 << 10, length);
    yield await reader.take(size);
    length -= size;
  }
}

export function captureWireBytes(capture, blobs) {
  const files = new Map(
    capture.manifest.entries
      .filter((row) => row.type === 'file')
      .map((row) => [row.sha256, row.bytes]),
  );
  blobs ??= [...files.keys()];
  return (
    4 +
    Buffer.byteLength(
      JSON.stringify({
        schema: 1,
        manifest: capture.manifest,
        digest: capture.digest,
        blobs,
      }),
    ) +
    blobs.reduce((bytes, name) => bytes + files.get(name), 0)
  );
}
