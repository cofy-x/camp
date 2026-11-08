export const filePreviewByteLimit = 1048576;
export const filePreviewCharacterLimit = 65536;

export interface FilePreviewRead {
  content: string;
  binary: boolean;
  editable: boolean;
  shortened: boolean;
}

// Validate complete editable files, but retain only a bounded text preview.
// One extra byte distinguishes an exact-limit file from a larger original.
export async function readFilePreview(
  response: Response,
  signal?: AbortSignal,
): Promise<FilePreviewRead> {
  signal?.throwIfAborted();
  if (response.body === null)
    return { content: '', binary: false, editable: true, shortened: false };
  const reader = response.body?.getReader();
  if (!reader) throw new Error('Could not read this file.');

  const abort = () => {
    void reader.cancel(signal?.reason).catch(() => {});
  };
  signal?.addEventListener('abort', abort, { once: true });
  const decoder = new TextDecoder('utf-8', { fatal: true });
  let content = '';
  let characters = 0;
  let bytes = 0;
  let complete = false;
  let binary = false;
  const append = (text: string) => {
    characters += text.length;
    if (content.length < filePreviewCharacterLimit)
      content += text.slice(0, filePreviewCharacterLimit - content.length);
  };
  try {
    for (;;) {
      signal?.throwIfAborted();
      const part = await reader.read();
      signal?.throwIfAborted();
      if (part.done) {
        complete = true;
        try {
          append(decoder.decode());
        } catch {
          binary = true;
        }
        break;
      }
      const prefix = part.value.subarray(
        0,
        Math.max(0, filePreviewByteLimit - bytes),
      );
      bytes += Math.min(
        part.value.byteLength,
        filePreviewByteLimit + 1 - bytes,
      );
      if (prefix.includes(0)) {
        binary = true;
        break;
      }
      try {
        // An oversized prefix may end inside a valid UTF-8 character. Keep
        // decoder state pending until EOF rather than classifying that cut.
        append(decoder.decode(prefix, { stream: true }));
      } catch {
        binary = true;
        break;
      }
      if (bytes > filePreviewByteLimit) break;
    }
    signal?.throwIfAborted();
    if (binary)
      return { content: '', binary: true, editable: false, shortened: false };
    const last = content.charCodeAt(content.length - 1);
    if (last >= 0xd800 && last <= 0xdbff) content = content.slice(0, -1);
    return {
      content,
      binary: false,
      editable: complete,
      shortened: !complete || characters > filePreviewCharacterLimit,
    };
  } finally {
    signal?.removeEventListener('abort', abort);
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
