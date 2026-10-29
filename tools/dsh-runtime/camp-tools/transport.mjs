import process from 'node:process';
import { URL } from 'node:url';
import { TextDecoder } from 'node:util';
import { Buffer } from 'node:buffer';
import { FsError } from '@deepseek-ai/dsh-fs';

const MAX_RESPONSE_BYTES = 32 * 1024 * 1024;

export class BridgeClient {
  constructor(environment = process.env) {
    let endpoint;
    try {
      endpoint = new URL(environment.CAMP_TOOL_ENDPOINT);
    } catch {
      throw new Error('Camp tool bridge endpoint is not configured.');
    }
    if (
      endpoint.protocol !== 'http:' ||
      !['127.0.0.1', '[::1]'].includes(endpoint.hostname) ||
      endpoint.username ||
      endpoint.password ||
      endpoint.search ||
      endpoint.hash ||
      endpoint.pathname !== '/'
    )
      throw new Error('Camp tool bridge requires a loopback HTTP origin.');
    const token = environment.CAMP_TOOL_TOKEN;
    if (
      typeof token !== 'string' ||
      token.length < 32 ||
      /[\r\n]/u.test(token)
    ) {
      throw new Error('Camp tool bridge capability is not configured.');
    }
    this.endpoint = endpoint.origin;
    this.headers = {
      'content-type': 'application/json',
      authorization: `Bearer ${token}`,
    };
  }

  async request(path, payload, signal) {
    try {
      return await globalThis.fetch(`${this.endpoint}${path}`, {
        method: 'POST',
        headers: this.headers,
        body: JSON.stringify(payload),
        redirect: 'error',
        signal,
      });
    } catch {
      if (signal?.aborted)
        throw new FsError('Camp tool request aborted.', 'FS_ABORTED');
      throw new Error('Camp tool bridge is unavailable.');
    }
  }

  async json(path, payload, signal) {
    const response = await this.request(path, payload, signal);
    const body = await boundedText(response, MAX_RESPONSE_BYTES, signal);
    let result;
    try {
      result = JSON.parse(body);
    } catch {
      throw new Error('Camp tool bridge returned invalid JSON.');
    }
    if (!result || typeof result !== 'object' || Array.isArray(result)) {
      throw new Error('Camp tool bridge returned an invalid response.');
    }
    if (result.error) {
      const { code, message } = result.error;
      throw new FsError(
        typeof message === 'string' ? message : 'Camp tool request failed.',
        typeof code === 'string' && code.startsWith('FS_')
          ? code
          : 'FS_IO_ERROR',
      );
    }
    if (!response.ok || !Object.hasOwn(result, 'value'))
      throw new Error('Camp tool bridge rejected the request.');
    return result.value;
  }
}

export async function boundedText(response, limit, signal) {
  if (!response.body)
    throw new Error('Camp tool bridge returned an empty response.');
  const reader = response.body.getReader();
  const chunks = [];
  let size = 0;
  try {
    while (true) {
      signal?.throwIfAborted();
      const chunk = await reader.read();
      if (chunk.done) break;
      size += chunk.value.byteLength;
      if (size > limit)
        throw new Error('Camp tool response exceeded its size limit.');
      chunks.push(chunk.value);
    }
    return new TextDecoder('utf-8', { fatal: true }).decode(
      Buffer.concat(chunks, size),
    );
  } catch (error) {
    await reader.cancel().catch(() => {});
    throw error;
  } finally {
    reader.releaseLock();
  }
}

export async function* readEvents(response) {
  if (!response.ok || !response.body)
    throw new Error('Camp command bridge rejected the request.');
  const reader = response.body.getReader();
  const decoder = new TextDecoder('utf-8', { fatal: true });
  let pending = '';
  try {
    while (true) {
      const chunk = await reader.read();
      pending += chunk.done
        ? decoder.decode()
        : decoder.decode(chunk.value, { stream: true });
      if (Buffer.byteLength(pending) > 4 * 1024 * 1024)
        throw new Error('Camp command event exceeded its size limit.');
      let end;
      while ((end = pending.indexOf('\n')) >= 0) {
        const line = pending.slice(0, end);
        pending = pending.slice(end + 1);
        if (line.trim()) yield JSON.parse(line);
      }
      if (chunk.done) break;
    }
    if (pending.trim())
      throw new Error('Camp command stream ended inside an event.');
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
