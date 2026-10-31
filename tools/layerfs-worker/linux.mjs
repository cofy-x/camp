import { spawn, spawnSync } from 'node:child_process';
import { Buffer } from 'node:buffer';
import { createHash, randomUUID } from 'node:crypto';
import { chmod, lstat, mkdir, open, statfs } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import process from 'node:process';
import { performance } from 'node:perf_hooks';
import { setTimeout as pause } from 'node:timers/promises';
import { LayerFS } from '/opt/camp/layerfs/api/agent/src/index.js';
import { LayerFSRuntime } from '/opt/camp/layerfs/api/runtime/src/index.js';
import { neededBlobs } from './apply.mjs';
import {
  digestPattern,
  checkpointPattern,
  uuidPattern,
  validateBinding,
} from './contracts.mjs';
import { captureWireBytes, writeBytes } from './protocol.mjs';
import { failure, readState, saveState } from './state.mjs';
import { LIMITS, manifestDigest, validateManifest } from './tree.mjs';
import { blobIndex, encodeCapture, receiveCapture } from './transfer.mjs';

const daemon = '/usr/local/bin/layerfs-daemon';
const helper = '/opt/camp/layerfs/worker/helper.mjs';

function docker(args) {
  const result = spawnSync('docker', args, {
    encoding: 'utf8',
    env: {
      PATH: '/usr/local/bin:/usr/bin:/bin',
      HOME: '/tmp',
      DOCKER_HOST: 'unix:///var/run/docker.sock',
    },
  });
  if (result.error || result.status !== 0)
    throw failure('Docker', result.error?.message ?? result.stderr);
  return JSON.parse(result.stdout);
}

export class LinuxWorker {
  static async open(config) {
    if (
      process.platform !== 'linux' ||
      process.arch !== 'arm64' ||
      config.protocol !== 1 ||
      !/^sha256:[a-f0-9]{64}$/.test(config.image) ||
      !/^\/var\/lib\/docker\/volumes\/camp-worker-[a-f0-9-]+\/_data$/.test(
        config.home,
      )
    )
      throw failure(
        'Unsupported',
        'The checkpoint worker requires native Linux ARM64 and an owned Linux home.',
      );
    const metadata = docker(['image', 'inspect', config.image])[0];
    if (
      metadata.Os !== 'linux' ||
      metadata.Architecture !== 'arm64' ||
      metadata.Config.Labels?.['camp.layerfs.commit'] !== config.commit
    )
      throw failure(
        'Conflict',
        'The worker image identity differs from the pinned installation.',
      );
    await chmod(config.home, 0o700);
    const filesystem = await statfs(config.home);
    if (![0x9123683e, 0xef53, 0x58465342].includes(filesystem.type))
      throw failure(
        'Unsupported',
        'The worker home must use Linux-local btrfs, ext4 or xfs.',
      );
    const file = path.join(config.home, 'worker.json');
    let state = await readState(file);
    const runtime = await LayerFSRuntime.open({
      home: config.home,
      image: config.image,
      daemon,
      docker: '/var/run/docker.sock',
      volumes: '/var/lib/docker/volumes',
      containerPrefix: config.prefix,
      sweep: false,
    });
    try {
      if (!state) {
        const storeId = randomUUID();
        state = {
          schema: 1,
          status: 'initializing',
          storeId,
          project: `camp-${storeId.replaceAll('-', '')}`,
          image: config.image,
          commit: config.commit,
        };
        await saveState(file, state);
        const source = path.join(config.home, 'empty');
        await mkdir(source, { mode: 0o700 });
        let job = await runtime.project.start_init(state.project, source, {
          timeout: 180_000,
        });
        while (!job.ended) {
          await pause(50);
          job = await runtime.project.get_init(job.id);
        }
        if (job.error) throw failure(job.error.code, job.error.message);
        const initialized = (await runtime.project.list()).find(
          (project) => project.name === state.project,
        );
        if (!initialized?.initialized || !initialized.volume)
          throw failure(
            'Unknown',
            'The initial worker Store could not be identified.',
          );
        state.volume = initialized.volume;
        state.status = 'ready';
        await saveState(file, state);
      }
      if (
        state.status !== 'ready' ||
        state.image !== config.image ||
        state.commit !== config.commit
      )
        throw failure(
          'Unknown',
          'An incomplete or incompatible worker home is retained for explicit recovery.',
        );
      const volume = docker(['volume', 'inspect', state.volume])[0];
      if (
        volume.Driver !== 'local' ||
        Object.keys(volume.Options ?? {}).length ||
        volume.Labels?.['layerfs.home'] !== config.home ||
        volume.Labels?.['layerfs.project'] !== state.project
      )
        throw failure(
          'Conflict',
          'Worker Store volume ownership or placement changed.',
        );
      const storePath = `/var/lib/docker/volumes/${state.volume}/_data/global/store.sqlite`;
      const storeFS = await statfs(storePath).catch(() => {
        throw failure(
          'LostStore',
          'The Disposable Store is unavailable; retained native captures must be recovered explicitly.',
        );
      });
      if (![0x9123683e, 0xef53, 0x58465342].includes(storeFS.type))
        throw failure(
          'Unsupported',
          'The Store is not on a qualified Linux-local filesystem.',
        );
      Object.assign(process.env, {
        LAYERFS_HOME: config.home,
        LAYERFS_STORE_PATH: storePath,
        LAYERFS_IMAGE: config.image,
        LAYERFS_DAEMON: daemon,
        LAYERFS_DOCKER: '/var/run/docker.sock',
        LAYERFS_VOLUME: state.volume,
        LAYERFS_CONTAINER_PREFIX: config.prefix,
      });
      const client = await LayerFS.open(state.project);
      const cache = path.join(config.home, 'cache');
      await mkdir(path.join(cache, 'blobs'), { recursive: true, mode: 0o700 });
      return new LinuxWorker({
        config,
        state,
        runtime,
        client,
        cache,
        filesystem: storeFS.type,
      });
    } catch (error) {
      await runtime.close();
      throw error;
    }
  }

  constructor(fields) {
    Object.assign(this, fields);
    this.mounts = new Set();
  }
  operationFile(id) {
    if (!uuidPattern.test(id))
      throw failure('InvalidInput', 'Invalid operation ID.');
    return path.join(this.config.home, 'operations', `${id}.json`);
  }
  namespaceFile(id) {
    if (!digestPattern.test(id))
      throw failure('InvalidInput', 'Invalid workspace ID.');
    return path.join(this.config.home, 'workspaces', `${id}.json`);
  }
  status() {
    return {
      protocol: 1,
      sourceCommit: this.config.commit,
      image: this.config.image,
      storeId: this.state.storeId,
      storeProfile: 'Disposable',
      crashDurable: false,
      platform: `${process.platform}/${process.arch}`,
      kernel: os.release(),
      filesystemMagic: this.filesystem,
      project: this.state.project,
      volume: this.state.volume,
    };
  }
  async head(branch) {
    const listed = await this.client.call('project.list_branches', {
      name: branch,
      pageSize: 1,
      pagePos: 0,
    });
    const item = listed.items?.find((row) => row.name === branch);
    if (!item)
      throw failure('Conflict', 'The expected worker branch is absent.');
    return item.head;
  }
  async mounted(branch) {
    const sandbox = (await this.client.sandbox.create()).sandbox;
    const resource = { sandbox };
    this.mounts.add(resource);
    resource.workspace = await this.client.workspace.mount(sandbox, branch);
    const records = await this.client.project.list_sandboxes(1000, 0);
    const record = records.items.find((item) => item.sandbox === sandbox);
    if (!record?.name)
      throw failure(
        'Unknown',
        'The administrative Sandbox was not identified.',
      );
    resource.container = record.name;
    return resource;
  }
  async release(resource) {
    if (resource.workspace)
      await this.client.workspace.unmount(resource.workspace);
    await this.client.sandbox.close(resource.sandbox);
    this.mounts.delete(resource);
  }
  command(resource, operation, digest = '', previous = '') {
    return spawn(
      'docker',
      [
        'exec',
        '-i',
        '--user',
        '501:20',
        resource.container,
        'node',
        helper,
        operation,
        resource.workspace.path,
        digest,
        previous,
      ],
      {
        env: {
          PATH: '/usr/local/bin:/usr/bin:/bin',
          HOME: '/tmp',
          DOCKER_HOST: 'unix:///var/run/docker.sock',
        },
        stdio: ['pipe', 'pipe', 'pipe'],
      },
    );
  }
  async runHelper(resource, operation, capture, previous, wanted) {
    const child = this.command(resource, operation, capture?.digest, previous);
    const output = [],
      errors = [];
    let bytes = 0;
    child.stdout.on('data', (chunk) => {
      bytes += chunk.length;
      if (bytes > LIMITS.manifestBytes + (1 << 20)) child.kill();
      else output.push(chunk);
    });
    child.stderr.on('data', (chunk) => {
      if (errors.reduce((sum, row) => sum + row.length, 0) < 64 << 10)
        errors.push(chunk);
    });
    const ended = new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('close', resolve);
    });
    let sending;
    try {
      if (capture)
        for await (const chunk of encodeCapture(capture, wanted))
          await writeBytes(child.stdin, chunk);
      child.stdin.end();
    } catch (error) {
      sending = error;
      child.stdin.destroy();
    }
    const code = await ended;
    if (sending) throw sending;
    if (code !== 0)
      throw failure(
        'Unknown',
        `Administrative ${operation} did not confirm completion: ${Buffer.concat(errors).toString('utf8').slice(0, 8192)}`,
      );
    return JSON.parse(Buffer.concat(output).toString('utf8'));
  }
  async prepare({ binding: input, manifest: original }) {
    const binding = validateBinding(input),
      manifest = validateManifest(original);
    if (
      binding.storeId !== this.state.storeId ||
      binding.digest !== manifestDigest(manifest)
    )
      throw failure(
        'Conflict',
        'Snapshot or Store identity differs from the operation.',
      );
    const existing = await readState(this.operationFile(binding.operationId));
    if (existing) {
      if (JSON.stringify(existing.binding) !== JSON.stringify(binding))
        throw failure(
          'Conflict',
          'An operation ID already names different frozen inputs.',
        );
      return existing;
    }
    let namespace = await readState(this.namespaceFile(binding.workspaceId));
    if (
      namespace?.pending ||
      (namespace &&
        (namespace.runnerId !== binding.runnerId ||
          namespace.storageId !== binding.storageId))
    )
      throw failure(
        'Unknown',
        'This workspace has a pending operation or a different owner.',
      );
    if (!namespace) {
      if (binding.generation !== 1 || binding.parentCheckpoint !== null)
        throw failure(
          'Conflict',
          'A new workspace starts at generation one with no parent Commit.',
        );
      namespace = {
        schema: 1,
        runnerId: binding.runnerId,
        storageId: binding.storageId,
        workspaceId: binding.workspaceId,
        branch: `w-${binding.workspaceId.slice(0, 48)}`,
        generation: 0,
        head: null,
        status: 'fork-dispatched',
        pending: binding.operationId,
      };
      await saveState(this.namespaceFile(binding.workspaceId), namespace);
      await this.client.branch.fork(namespace.branch, 'main');
      const resource = await this.mounted(namespace.branch);
      const scanned = await this.runHelper(resource, 'scan');
      await this.release(resource);
      namespace.manifest = scanned.manifest;
      namespace.digest = scanned.digest;
      namespace.status = 'ready';
      namespace.pending = null;
      await saveState(this.namespaceFile(binding.workspaceId), namespace);
    }
    if (
      namespace.generation + 1 !== binding.generation ||
      namespace.head !== binding.parentCheckpoint ||
      (await this.head(namespace.branch)) !== namespace.head
    )
      throw failure('Conflict', 'Generation or parent checkpoint is stale.');
    const wanted = [];
    const cacheStarted = performance.now();
    for (const [digest, length] of blobIndex(manifest)) {
      const file = path.join(this.cache, 'blobs', digest);
      let valid = false;
      try {
        const info = await lstat(file);
        if (info.isFile() && info.size === length) {
          const opened = await open(file, 'r'),
            hasher = createHash('sha256');
          try {
            for await (const chunk of opened.createReadStream())
              hasher.update(chunk);
            valid = hasher.digest('hex') === digest;
          } finally {
            await opened.close();
          }
        }
      } catch {
        /* Missing or corrupt cache bytes are transferred again before apply. */
      }
      if (!valid) wanted.push(digest);
    }
    const receipt = {
      schema: 1,
      binding,
      state: 'prepared',
      manifest,
      wanted,
      cacheValidationMs: performance.now() - cacheStarted,
      branch: namespace.branch,
      disposable: true,
      durableDelivery: false,
    };
    await saveState(this.operationFile(binding.operationId), receipt);
    namespace.pending = binding.operationId;
    await saveState(this.namespaceFile(binding.workspaceId), namespace);
    return receipt;
  }
  async upload({ operationId }, stream) {
    const receipt = await readState(this.operationFile(operationId));
    if (!receipt || receipt.state !== 'prepared')
      throw failure(
        'Unknown',
        'An upload is admitted once, only for a prepared operation.',
      );
    receipt.state = 'upload-dispatched';
    await saveState(this.operationFile(operationId), receipt);
    try {
      const received = await receiveCapture(stream, this.cache, {
        digest: receipt.binding.digest,
        blobs: receipt.wanted,
      });
      receipt.transfer = received.metrics;
      receipt.state = 'uploaded';
    } catch (error) {
      receipt.state = 'upload-rejected';
      receipt.error = { code: error.code ?? 'Failed', message: error.message };
      await saveState(this.operationFile(operationId), receipt);
      throw error;
    }
    await saveState(this.operationFile(operationId), receipt);
    return receipt;
  }
  async commit({ operationId }) {
    const receipt = await readState(this.operationFile(operationId));
    if (!receipt || receipt.state !== 'uploaded')
      throw failure(
        'Unknown',
        'Apply and Commit are never repeated after dispatch or an uncertain response.',
      );
    const { binding } = receipt;
    const namespace = await readState(this.namespaceFile(binding.workspaceId));
    if (
      namespace.pending !== operationId ||
      namespace.generation + 1 !== binding.generation ||
      namespace.head !== binding.parentCheckpoint ||
      (await this.head(namespace.branch)) !== binding.parentCheckpoint
    )
      throw failure(
        'Conflict',
        'The operation no longer names the current generation and parent.',
      );
    const capture = {
      root: this.cache,
      manifest: receipt.manifest,
      digest: binding.digest,
    };
    const resource = await this.mounted(namespace.branch);
    receipt.state = 'apply-dispatched';
    receipt.sandbox = resource.sandbox;
    await saveState(this.operationFile(operationId), receipt);
    try {
      const applyStarted = performance.now();
      receipt.apply = await this.runHelper(
        resource,
        'apply',
        capture,
        namespace.digest,
        neededBlobs(namespace.manifest, receipt.manifest),
      );
      receipt.applyTransportMs = performance.now() - applyStarted;
      receipt.state = 'applied';
      await saveState(this.operationFile(operationId), receipt);
      if (namespace.digest === binding.digest)
        receipt.checkpoint = { kind: 'UpToDate', head: namespace.head };
      else {
        receipt.state = 'commit-dispatched';
        await saveState(this.operationFile(operationId), receipt);
        const started = performance.now();
        receipt.checkpoint = await this.client.workspace.commit(
          resource.workspace,
          `camp:${binding.operationId}:${binding.generation}:${binding.digest}`,
        );
        receipt.commitMs = performance.now() - started;
        if (
          !['Committed', 'UpToDate'].includes(receipt.checkpoint.kind) ||
          !checkpointPattern.test(receipt.checkpoint.head ?? '')
        )
          throw failure(
            'Unknown',
            'LayerFS returned no acknowledged checkpoint identity.',
          );
      }
      receipt.state = 'commit-acknowledged';
      await saveState(this.operationFile(operationId), receipt);
      await this.release(resource);
      return await this.verify(receipt, namespace);
    } catch (error) {
      receipt.error = { code: error.code ?? 'Unknown', message: error.message };
      await saveState(this.operationFile(operationId), receipt);
      throw error;
    }
  }
  async verify(receipt, namespace) {
    const started = performance.now();
    if ((await this.head(namespace.branch)) !== receipt.checkpoint.head)
      throw failure(
        'Conflict',
        'The checkpoint head changed before independent readback.',
      );
    const reader = await this.mounted(namespace.branch);
    const verified = await this.runHelper(reader, 'scan');
    await this.release(reader);
    if (
      verified.digest !== receipt.binding.digest ||
      (await this.head(namespace.branch)) !== receipt.checkpoint.head
    )
      throw failure(
        'Mismatch',
        'Independent checkpoint remount did not match its frozen snapshot and head.',
      );
    receipt.readbackMs = performance.now() - started;
    receipt.state = 'verified';
    receipt.verifiedDigest = verified.digest;
    await saveState(this.operationFile(receipt.binding.operationId), receipt);
    namespace.generation = receipt.binding.generation;
    namespace.head = receipt.checkpoint.head;
    namespace.digest = receipt.binding.digest;
    namespace.manifest = receipt.manifest;
    namespace.pending = null;
    await saveState(this.namespaceFile(receipt.binding.workspaceId), namespace);
    return receipt;
  }
  async observe({ operationId }) {
    const receipt = await readState(this.operationFile(operationId));
    if (!receipt)
      throw failure('NotFound', 'No receipt is known for this operation.');
    const namespace = await readState(
      this.namespaceFile(receipt.binding.workspaceId),
    );
    if (receipt.state === 'verified') {
      if (namespace.pending === operationId) {
        namespace.generation = receipt.binding.generation;
        namespace.head = receipt.checkpoint.head;
        namespace.digest = receipt.binding.digest;
        namespace.manifest = receipt.manifest;
        namespace.pending = null;
        await saveState(
          this.namespaceFile(receipt.binding.workspaceId),
          namespace,
        );
      }
      return receipt;
    }
    if (receipt.state === 'commit-dispatched') {
      const history = await this.client.branch.list_commits(
        receipt.branch,
        1000,
        0,
      );
      const message = `camp:${receipt.binding.operationId}:${receipt.binding.generation}:${receipt.binding.digest}`;
      const committed = history.items?.find((item) => item.message === message);
      if (
        committed &&
        committed.parent === receipt.binding.parentCheckpoint &&
        (await this.head(receipt.branch)) === committed.id
      ) {
        receipt.checkpoint = { kind: 'Committed', head: committed.id };
        return this.verify(receipt, namespace);
      }
    }
    if (receipt.state === 'commit-acknowledged')
      return this.verify(receipt, namespace);
    return receipt;
  }
  async export({ operationId }, output, respond) {
    const receipt = await this.observe({ operationId });
    if (receipt.state !== 'verified')
      throw failure('Unknown', 'An unverified operation cannot be restored.');
    const resource = await this.mounted(receipt.branch);
    const scanned = await this.runHelper(resource, 'scan');
    if (
      scanned.digest !== receipt.binding.digest ||
      (await this.head(receipt.branch)) !== receipt.checkpoint.head
    )
      throw failure(
        'Conflict',
        'This branch no longer names the requested checkpoint; fork retained history explicitly.',
      );
    const capture = {
      manifest: receipt.manifest,
      digest: receipt.binding.digest,
    };
    const child = this.command(resource, 'export', capture.digest);
    const errors = [];
    child.stderr.on('data', (chunk) => {
      if (errors.reduce((sum, row) => sum + row.length, 0) < 8192)
        errors.push(chunk);
    });
    const ended = new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('close', resolve);
    });
    child.stdin.end();
    await respond({ receipt, binaryBytes: captureWireBytes(capture) });
    for await (const chunk of child.stdout) await writeBytes(output, chunk);
    const code = await ended;
    await this.release(resource);
    if (code !== 0)
      throw failure(
        'Unknown',
        `Checkpoint readback export failed: ${Buffer.concat(errors).toString('utf8')}`,
      );
  }
  async close() {
    const errors = [];
    for (const resource of this.mounts)
      try {
        await this.release(resource);
      } catch (error) {
        errors.push(error);
      }
    await this.client.close();
    await this.runtime.close();
    if (errors.length)
      throw failure(
        'Unknown',
        'Some administrative mounts remain retained after shutdown.',
      );
  }
}
