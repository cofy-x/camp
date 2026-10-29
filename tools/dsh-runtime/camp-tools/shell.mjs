import { setTimeout, clearTimeout } from 'node:timers';
import { Buffer } from 'node:buffer';
import { randomUUID } from 'node:crypto';
import { ShellExecutor } from '@deepseek-ai/dsh-shell';
import { BridgeClient, readEvents } from './transport.mjs';

const MAX_TIMEOUT_MS = 120_000;
const MAX_OUTPUT_BYTES = 64 * 1024;
const CLEANUP_TIMEOUT_MS = 10_000;

function boundedInteger(value, fallback, maximum) {
  if (value === undefined) return fallback;
  if (!Number.isSafeInteger(value) || value <= 0)
    throw new Error('Command limits must be positive integers.');
  return Math.min(value, maximum);
}

export function resolveCommand(request) {
  if (
    typeof request.command !== 'string' ||
    !request.command.trim() ||
    request.command.length > 128 * 1024
  )
    throw new Error('command must contain 1–131072 characters.');
  if (request.onExpiry !== undefined && request.onExpiry !== 'kill')
    throw new Error('Background commands are not available in Camp.');
  if (request.stdin !== undefined || request.env !== undefined)
    throw new Error(
      'Command stdin and environment overrides are not available in Camp.',
    );
  if (
    request.workdir !== undefined &&
    (typeof request.workdir !== 'string' || !request.workdir)
  )
    throw new Error('workdir must be non-empty text.');
  if (request.sandboxPolicy && request.sandboxPolicy.mode !== 'workspace-write')
    throw new Error('Command access cannot be widened in Camp.');
  return {
    command: request.command,
    workdir: request.workdir ?? '/workspace',
    timeoutMs: boundedInteger(request.timeoutMs, 30_000, MAX_TIMEOUT_MS),
    stdoutMaxBytes: boundedInteger(
      request.stdoutMaxBytes,
      MAX_OUTPUT_BYTES,
      MAX_OUTPUT_BYTES,
    ),
    onExpiry: 'kill',
    signal: request.signal,
    sandboxPolicy: request.sandboxPolicy,
  };
}

class OutputTail {
  constructor(limit) {
    this.limit = limit;
    this.bytes = Buffer.alloc(0);
    this.total = 0;
  }
  append(text) {
    const chunk = Buffer.from(text, 'utf8');
    this.total += chunk.length;
    this.bytes = Buffer.concat([this.bytes, chunk]);
    if (this.bytes.length > this.limit)
      this.bytes = this.bytes.subarray(-this.limit);
  }
  readFrom(offset) {
    const start = this.total - this.bytes.length;
    return {
      text: this.bytes.subarray(Math.max(0, offset - start)).toString('utf8'),
      nextOffset: this.total,
      lossy: offset < start,
    };
  }
}

/** A foreground handle remains owned until the bridge confirms process cleanup. */
export class CampCommand {
  constructor(bridge, spec) {
    this.bridge = bridge;
    this.spec = spec;
    this.commandId = randomUUID();
    this.status = 'running';
    this.exitCode = null;
    this.signal = null;
    this.stdout = new OutputTail(spec.stdoutMaxBytes);
    this.stderr = new OutputTail(MAX_OUTPUT_BYTES);
    this.observed = { stdout: this.stdout, stderr: this.stderr };
    this.stdoutOffset = 0;
    this.stderrOffset = 0;
    this.transport = new globalThis.AbortController();
    this.cancelled = false;
    this.cancellation = null;
    this.resultPromise = this.run();
    this.done = this.resultPromise.then(
      () => {},
      () => {},
    );
    const onAbort = () => this.kill();
    spec.signal?.addEventListener('abort', onAbort, { once: true });
    if (spec.signal?.aborted) this.kill();
    void this.done.then(() =>
      spec.signal?.removeEventListener('abort', onAbort),
    );
  }

  result() {
    return this.resultPromise;
  }

  readOutput() {
    const stdout = this.stdout.readFrom(this.stdoutOffset);
    const stderr = this.stderr.readFrom(this.stderrOffset);
    this.stdoutOffset = stdout.nextOffset;
    this.stderrOffset = stderr.nextOffset;
    return {
      delta: stdout.text + (stderr.text ? `\n[stderr]\n${stderr.text}` : ''),
      lossy: stdout.lossy || stderr.lossy,
    };
  }

  kill() {
    if (this.status !== 'running' || this.cancellation) return false;
    this.cancelled = true;
    this.cancellation = this.cancel();
    // Cancellation failure must interrupt the stream; run() still awaits it.
    void this.cancellation.catch(() => this.transport.abort());
    return true;
  }

  async cancel() {
    const value = await this.bridge.json(
      '/shell/cancel',
      { commandId: this.commandId },
      globalThis.AbortSignal.timeout(CLEANUP_TIMEOUT_MS),
    );
    if (value?.cleanupConfirmed !== true)
      throw new Error('Camp could not confirm command cleanup.');
  }

  async run() {
    const timeout = setTimeout(() => {
      this.kill();
      this.transport.abort();
    }, this.spec.timeoutMs + CLEANUP_TIMEOUT_MS);
    try {
      const response = await this.bridge.request(
        '/shell',
        {
          commandId: this.commandId,
          command: this.spec.command,
          workdir: this.spec.workdir,
          timeoutMs: this.spec.timeoutMs,
          stdoutMaxBytes: this.spec.stdoutMaxBytes,
        },
        this.transport.signal,
      );
      let result;
      for await (const event of readEvents(response)) {
        if (result)
          throw new Error(
            'Camp command emitted data after its terminal result.',
          );
        if (event.type === 'stdout' || event.type === 'stderr') {
          if (typeof event.text !== 'string')
            throw new Error('Camp command output is invalid.');
          this[event.type].append(event.text);
        } else if (event.type === 'result') {
          if (event.cleanupConfirmed !== true)
            throw new Error('Camp command ended without confirmed cleanup.');
          result = validateResult(event.result);
        } else if (event.type === 'error') {
          throw new Error(
            typeof event.error?.message === 'string'
              ? event.error.message
              : 'Camp command preparation failed.',
          );
        } else throw new Error('Camp command returned an unknown event.');
      }
      if (!result)
        throw new Error('Camp command stream ended without a terminal result.');
      if (this.cancellation) await this.cancellation;
      this.exitCode = result.exitCode;
      this.signal = result.signal;
      this.status =
        result.aborted || result.timedOut || result.signal
          ? 'killed'
          : 'completed';
      return result;
    } catch (error) {
      this.kill();
      try {
        await this.cancellation;
      } catch {
        throw new Error(
          'Camp command transport failed and cleanup is unconfirmed.',
        );
      }
      throw error;
    } finally {
      clearTimeout(timeout);
      if (this.status === 'running') this.status = 'killed';
    }
  }
}

function validateResult(result) {
  if (
    !result ||
    !(result.exitCode === null || Number.isSafeInteger(result.exitCode)) ||
    !(result.signal === null || typeof result.signal === 'string') ||
    typeof result.timedOut !== 'boolean' ||
    typeof result.aborted !== 'boolean' ||
    (result.timedOut && result.aborted) ||
    !Number.isSafeInteger(result.timeoutMs) ||
    result.timeoutMs < 1 ||
    ![result.stdout, result.stderr].every(
      (stream) =>
        stream &&
        typeof stream.text === 'string' &&
        typeof stream.truncated === 'boolean' &&
        Buffer.byteLength(stream.text) <= MAX_OUTPUT_BYTES,
    )
  ) {
    throw new Error('Camp command returned an invalid result.');
  }
  return result;
}

export default class CampShell extends ShellExecutor {
  constructor(ctx) {
    super(ctx);
    this.bridge = new BridgeClient();
    this.commands = new Set();
    ctx.on('dispose', async () => {
      for (const command of this.commands) command.kill();
      await Promise.all([...this.commands].map((command) => command.done));
    });
  }
  resolve(request) {
    return resolveCommand(request);
  }
  async execute(spec) {
    spec.signal?.throwIfAborted();
    const command = new CampCommand(this.bridge, resolveCommand(spec));
    this.commands.add(command);
    void command.done.then(() => this.commands.delete(command));
    return command;
  }
}
