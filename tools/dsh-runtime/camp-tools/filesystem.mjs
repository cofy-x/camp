import { Buffer } from 'node:buffer';
import { FileSystem, FsError } from '@deepseek-ai/dsh-fs';
import { posix } from 'node:path';
import { pathToFileURL } from 'node:url';
import { BridgeClient } from './transport.mjs';

/** The Go bridge owns identity, containment, versions and atomic mutations. */
export default class CampFileSystem extends FileSystem {
  constructor(ctx) {
    super(ctx);
    this.bridge = new BridgeClient();
    this.targets = new Map();
  }

  remember(target) {
    if (
      !target ||
      typeof target.targetKey !== 'string' ||
      typeof target.displayPath !== 'string' ||
      !posix.isAbsolute(target.displayPath) ||
      posix.normalize(target.displayPath) !== target.displayPath
    ) {
      throw new Error('Camp file bridge returned an invalid target.');
    }
    this.targets.set(target.targetKey, target.displayPath);
    return target;
  }

  processPath(target) {
    const path = this.targets.get(target.targetKey);
    if (path === undefined || path !== target.displayPath)
      throw new FsError('Unknown Camp file target.', 'FS_SANDBOX_DENIED');
    return path;
  }

  fileUrl(target) {
    return pathToFileURL(this.processPath(target)).href;
  }

  contains(parent, child) {
    const relative = posix.relative(
      this.processPath(parent),
      this.processPath(child),
    );
    return (
      relative === '' ||
      (relative !== '..' &&
        !relative.startsWith('../') &&
        !posix.isAbsolute(relative))
    );
  }

  async resolve(path, opts = {}) {
    return this.remember(
      await this.bridge.json(
        '/fs',
        { operation: 'resolve', path, cwd: opts.cwd },
        opts.signal,
      ),
    );
  }

  async operation(operation, target, extra, signal) {
    this.processPath(target);
    return this.bridge.json('/fs', { operation, target, ...extra }, signal);
  }

  async stat(target, signal) {
    return (await this.operation('stat', target, {}, signal)) ?? undefined;
  }
  async lstat(path, opts = {}, signal) {
    return (
      (await this.bridge.json(
        '/fs',
        { operation: 'lstat', path, cwd: opts.cwd },
        signal,
      )) ?? undefined
    );
  }
  readText(target, signal) {
    return this.operation('readText', target, {}, signal);
  }
  async streamText(target, signal) {
    const text = await this.readText(target, signal);
    return (async function* () {
      signal?.throwIfAborted();
      yield text;
    })();
  }
  async readBytes(target, signal, maxBytes) {
    return Buffer.from(
      await this.operation('readBytes', target, { maxBytes }, signal),
      'base64',
    );
  }
  async readByteRange(target, range, signal) {
    return Buffer.from(
      await this.operation('readByteRange', target, range, signal),
      'base64',
    );
  }
  async listDir(target, signal) {
    const entries = await this.operation('listDir', target, {}, signal);
    return entries.map((entry) => ({
      ...entry,
      target: this.remember(entry.target),
    }));
  }
  writeText(target, content, expected, signal) {
    return this.operation('writeText', target, { content, expected }, signal);
  }
  editText(target, edit, expected, signal) {
    return this.operation('editText', target, { edit, expected }, signal);
  }
}
