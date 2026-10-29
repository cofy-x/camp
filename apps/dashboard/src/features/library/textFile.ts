import { workspaceApi } from '../workspace/api';

export async function readBoundedText(response: Response, maxBytes: number) {
  const reader = response.body?.getReader();
  if (!reader) throw new Error('Could not read this file.');
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    for (;;) {
      const part = await reader.read();
      if (part.done) break;
      size += part.value.byteLength;
      if (size > maxBytes)
        throw new Error(
          'This file is larger than the on-screen text limit. Download it to read the complete contents.',
        );
      chunks.push(part.value);
    }
  } finally {
    await reader.cancel();
  }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  if (bytes.includes(0))
    throw new Error(
      'This file contains binary data. Download it or ask an agent to work with it.',
    );
  return new TextDecoder('utf-8', { fatal: true }).decode(bytes);
}

export async function readTextFile(
  resourceId: string,
  path: string,
  commit: string,
  missingIsEmpty = false,
  signal?: AbortSignal,
) {
  const response = await fetch(workspaceApi.fileURL(resourceId, path, commit), {
    signal,
  });
  if (missingIsEmpty && response.status === 404) return '';
  if (!response.ok) throw new Error('Could not load this file for editing.');
  return readBoundedText(response, 1048576);
}
