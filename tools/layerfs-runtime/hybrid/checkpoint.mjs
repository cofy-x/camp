import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { saveState } from '../state.mjs';
import { fileBytes } from './manifest.mjs';

const directories = (entries) => {
  const result = new Set();
  for (const entry of entries) {
    let parent = path.posix.dirname(entry.path);
    while (parent !== '.') {
      result.add(parent);
      parent = path.posix.dirname(parent);
    }
  }
  return result;
};

// The native tree is the sole writer. This proof receives a sealed manifest;
// it neither starts commands nor grants a worker enrollment/publication power.
export class CheckpointMirror {
  constructor(client, branch, receipt) {
    Object.assign(this, { client, branch, receipt });
  }

  async synchronize(snapshot, generation, { full = false, apply } = {}) {
    if (this.busy) throw new Error('A checkpoint transfer is already active');
    this.busy = true;
    try {
      return await this.transfer(snapshot, generation, { full, apply });
    } finally {
      this.busy = false;
    }
  }

  async transfer(snapshot, generation, { full, apply }) {
    let previous;
    try {
      previous = JSON.parse(await readFile(this.receipt, 'utf8'));
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    if (previous && previous.state !== 'confirmed')
      throw new Error(
        'Checkpoint outcome is uncertain; mutation will not be replayed',
      );
    if (previous && previous.branch !== this.branch)
      throw new Error(
        'Checkpoint receipt belongs to a different worker branch',
      );
    if (
      !Number.isSafeInteger(generation) ||
      generation !== (previous?.generation ?? 0) + 1
    )
      throw new Error(
        'Checkpoint generation is not the next sealed generation',
      );
    const old = new Map(
      (previous?.entries ?? []).map((entry) => [entry.path, entry]),
    );
    const next = new Map(snapshot.entries.map((entry) => [entry.path, entry]));
    const oldDirectories = directories(previous?.entries ?? []);
    const nextDirectories = directories(snapshot.entries);
    const changes = [];
    for (const entry of old.values())
      if (!next.has(entry.path))
        changes.push({ op: 'rm', path: `/${entry.path}` });
    for (const directory of [...oldDirectories].sort(
      (a, b) => b.length - a.length,
    ))
      if (!nextDirectories.has(directory))
        changes.push({ op: 'rm', path: `/${directory}` });
    for (const directory of [...nextDirectories].sort(
      (a, b) => a.length - b.length,
    ))
      if (!oldDirectories.has(directory))
        changes.push({ op: 'mkdir', path: `/${directory}`, mode: 0o755 });
    let transferredBytes = 0;
    for (const entry of snapshot.entries) {
      const before = old.get(entry.path);
      if (full || before?.sha256 !== entry.sha256) {
        const bytes = await fileBytes(snapshot, entry);
        changes.push({
          op: 'write',
          path: `/${entry.path}`,
          bytes,
          mode: entry.mode,
        });
        transferredBytes += bytes.length;
      } else if (before.mode !== entry.mode)
        changes.push({ op: 'chmod', path: `/${entry.path}`, mode: entry.mode });
    }
    const receipt = {
      state: 'pending',
      branch: this.branch,
      generation,
      sha256: snapshot.sha256,
      files: snapshot.files,
      bytes: snapshot.bytes,
      previousCommit: previous?.commit ?? null,
    };
    await saveState(this.receipt, receipt);
    try {
      const result = await (
        apply ?? ((...args) => this.client.fs.apply(...args))
      )(
        this.branch,
        changes,
        `Camp sealed generation ${generation} ${snapshot.sha256}`,
      );
      if (
        !['Committed', 'UpToDate'].includes(result.kind) ||
        typeof result.head !== 'string'
      )
        throw new Error('The worker omitted its commit receipt');
      if (changes.length === 0 && result.head !== previous?.commit)
        throw new Error(
          'An unchanged checkpoint moved to an unexpected worker head',
        );
      Object.assign(receipt, {
        state: 'confirmed',
        commit: result.head,
        entries: snapshot.entries,
        transferredBytes,
        operations: changes.length,
      });
      await saveState(this.receipt, receipt);
      return receipt;
    } catch (error) {
      await saveState(this.receipt, { ...receipt, state: 'uncertain' });
      throw error;
    }
  }
}
