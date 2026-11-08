import { describe, expect, it, vi } from 'vitest';
import {
  filePreviewByteLimit,
  filePreviewCharacterLimit,
  readFilePreview,
} from './readFilePreview';

const encoder = new TextEncoder();

function chunked(chunks: Uint8Array[]) {
  let index = 0;
  const cancel = vi.fn();
  const pull = vi.fn((controller: ReadableStreamDefaultController) => {
    if (index === chunks.length) controller.close();
    else controller.enqueue(chunks[index++]);
  });
  const stream = new ReadableStream<Uint8Array>(
    { pull, cancel },
    { highWaterMark: 0 },
  );
  return { response: new Response(stream), pull, cancel };
}

describe('bounded immutable file previews', () => {
  it('cancels oversized input after its boundary sentinel and keeps only the preview', async () => {
    const input = chunked([
      new Uint8Array(filePreviewByteLimit).fill(65),
      encoder.encode('B'),
      encoder.encode('This part must never be requested.'),
    ]);
    const result = await readFilePreview(input.response);
    expect(result).toEqual({
      content: 'A'.repeat(filePreviewCharacterLimit),
      binary: false,
      editable: false,
      shortened: true,
    });
    expect(input.pull).toHaveBeenCalledTimes(2);
    expect(input.cancel).toHaveBeenCalledTimes(1);
  });

  it('caps a single large transport chunk without requesting the rest', async () => {
    const input = chunked([
      new Uint8Array(filePreviewByteLimit * 4).fill(65),
      encoder.encode('Not needed'),
    ]);
    const result = await readFilePreview(input.response);
    expect(result.content).toHaveLength(filePreviewCharacterLimit);
    expect(result.editable).toBe(false);
    expect(result.shortened).toBe(true);
    expect(input.pull).toHaveBeenCalledTimes(1);
    expect(input.cancel).toHaveBeenCalledTimes(1);
  });

  it('requires EOF before enabling editing at the exact one MiB limit', async () => {
    const input = chunked([new Uint8Array(filePreviewByteLimit).fill(65)]);
    const result = await readFilePreview(input.response);
    expect(result.editable).toBe(true);
    expect(result.binary).toBe(false);
    expect(result.shortened).toBe(true);
    expect(result.content).toHaveLength(filePreviewCharacterLimit);
    expect(input.pull).toHaveBeenCalledTimes(2);
  });

  it('decodes valid UTF-8 characters split between chunks', async () => {
    const input = chunked([
      new Uint8Array([65, 0xe4]),
      new Uint8Array([0xb8]),
      new Uint8Array([0xad, 66]),
    ]);
    expect(await readFilePreview(input.response)).toEqual({
      content: 'A中B',
      binary: false,
      editable: true,
      shortened: false,
    });
  });

  it('does not classify a valid oversized UTF-8 boundary as binary', async () => {
    const prefix = new Uint8Array(filePreviewByteLimit).fill(65);
    prefix[prefix.length - 1] = 0xe4;
    const input = chunked([prefix, new Uint8Array([0xb8, 0xad])]);
    const result = await readFilePreview(input.response);
    expect(result.binary).toBe(false);
    expect(result.editable).toBe(false);
    expect(result.shortened).toBe(true);
    expect(result.content).not.toContain('�');
    expect(input.cancel).toHaveBeenCalledTimes(1);
  });

  it.each([
    new Uint8Array([65, 0xff]),
    new Uint8Array([65, 0, 66]),
    new Uint8Array([65, 0xe4]),
    new Uint8Array([0x89, 0x50, 0x4e, 0x47]),
  ])('keeps invalid or binary originals uneditable: %j', async (bytes) => {
    const { response } = chunked([bytes]);
    expect(await readFilePreview(response)).toEqual({
      content: '',
      binary: true,
      editable: false,
      shortened: false,
    });
  });

  it('validates bytes after the visible preview before enabling editing', async () => {
    const prefix = new Uint8Array(filePreviewCharacterLimit + 1).fill(65);
    const input = chunked([prefix, new Uint8Array([0xff])]);
    const result = await readFilePreview(input.response);
    expect(result.binary).toBe(true);
    expect(result.editable).toBe(false);
  });

  it('rejects a truncated UTF-8 character at EOF of an exact-limit file', async () => {
    const bytes = new Uint8Array(filePreviewByteLimit).fill(65);
    bytes[bytes.length - 1] = 0xe4;
    const result = await readFilePreview(chunked([bytes]).response);
    expect(result.binary).toBe(true);
    expect(result.editable).toBe(false);
  });

  it('does not leave a lone surrogate at the displayed character boundary', async () => {
    const text = 'A'.repeat(filePreviewCharacterLimit - 1) + '😀';
    const result = await readFilePreview(new Response(text));
    expect(result.content).toBe('A'.repeat(filePreviewCharacterLimit - 1));
    expect(result.shortened).toBe(true);
    expect(result.editable).toBe(true);
  });

  it('cancels an outstanding read when the owning preview is aborted', async () => {
    const cancel = vi.fn();
    const controller = new AbortController();
    let started!: () => void;
    const pending = new Promise<void>((resolve) => {
      started = resolve;
    });
    const stream = new ReadableStream<Uint8Array>(
      { pull: started, cancel },
      { highWaterMark: 0 },
    );
    const reading = readFilePreview(new Response(stream), controller.signal);
    const rejected = expect(reading).rejects.toMatchObject({
      name: 'AbortError',
    });
    await pending;
    controller.abort();
    await rejected;
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it('does not read an already-aborted preview', async () => {
    const input = chunked([encoder.encode('Do not read')]);
    const controller = new AbortController();
    controller.abort();
    await expect(
      readFilePreview(input.response, controller.signal),
    ).rejects.toMatchObject({ name: 'AbortError' });
    expect(input.pull).not.toHaveBeenCalled();
  });

  it('propagates transport failures as preview errors', async () => {
    const stream = new ReadableStream<Uint8Array>({
      pull(controller) {
        controller.error(new Error('Read failed'));
      },
    });
    await expect(readFilePreview(new Response(stream))).rejects.toThrow(
      'Read failed',
    );
  });

  it('handles empty response bodies without an unbounded fallback', async () => {
    expect(await readFilePreview(new Response(null))).toEqual({
      content: '',
      binary: false,
      editable: true,
      shortened: false,
    });
    const response = new Response(null);
    Object.defineProperty(response, 'body', { value: undefined });
    await expect(readFilePreview(response)).rejects.toThrow(
      'Could not read this file.',
    );
  });
});
