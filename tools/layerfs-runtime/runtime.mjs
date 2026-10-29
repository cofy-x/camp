import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { open, readFile, readdir, realpath, rm, stat } from 'node:fs/promises';
import { createRequire } from 'node:module';
import path from 'node:path';
import process from 'node:process';
import { finished } from 'node:stream/promises';
import { setTimeout as pause } from 'node:timers/promises';
import { pathToFileURL } from 'node:url';
import {
  BridgeError,
  identity,
  ownedDirectory,
  ownedOutput,
  shellQuote,
  string,
} from './validation.mjs';
import { readState, saveState } from './state.mjs';

export class CampLayerFS {
  static async open(config) {
    if (process.platform !== 'linux')
      throw new BridgeError(
        'Unsupported',
        'Camp LayerFS currently requires Linux.',
      );
    const installation =
      config.installation ?? path.join(import.meta.dirname, 'installed.json');
    const installed = JSON.parse(await readFile(installation, 'utf8'));
    // Release bundles use relative paths and remain movable after extraction.
    for (const field of ['source', 'native', 'daemon']) {
      installed[field] = path.resolve(
        path.dirname(installation),
        string(installed[field], field),
      );
    }
    const pin = JSON.parse(
      await readFile(path.join(import.meta.dirname, 'pin.json'), 'utf8'),
    );
    if (
      installed.commit !== pin.commit ||
      !/^sha256:[0-9a-f]{64}$/.test(installed.image)
    )
      throw new BridgeError(
        'InvalidInput',
        'The installed LayerFS runtime does not match its source pin.',
      );
    const dataRoot = await realpath(string(config.dataRoot, 'dataRoot'));
    const home = await ownedDirectory(
      dataRoot,
      string(config.home, 'home'),
      true,
    );
    const maxArchiveBytes = config.maxArchiveBytes ?? 256 * 1024 * 1024;
    if (
      !Number.isSafeInteger(maxArchiveBytes) ||
      maxArchiveBytes < 1024 ||
      maxArchiveBytes > 512 * 1024 * 1024
    )
      throw new BridgeError(
        'InvalidInput',
        'Archive byte budget must be between 1 KiB and 512 MiB.',
      );
    process.env.LAYERFS_NATIVE = installed.native;
    process.env.LAYERFS_CONSTRUCTION_WORKERS = '1';
    const require = createRequire(
      path.join(installed.source, 'api/package.json'),
    );
    const { LayerFS } = await import(
      pathToFileURL(require.resolve('@layerfs/agent'))
    );
    const { LayerFSBackend } = await import(
      pathToFileURL(require.resolve('@layerfs/backend'))
    );
    const { LayerFSRuntime } = await import(
      pathToFileURL(require.resolve('@layerfs/runtime'))
    );
    const containerPrefix = `camp-${identity('home', [home]).slice(5, 17)}-`;
    const options = {
      home,
      image: installed.image,
      daemon: installed.daemon,
      docker: config.docker ?? '/var/run/docker.sock',
      volumes: config.volumes ?? '/var/lib/docker/volumes',
      containerPrefix,
      sweep: false,
    };
    const runtime = await LayerFSRuntime.open(options);
    return new CampLayerFS({
      dataRoot,
      home,
      installed,
      options,
      runtime,
      LayerFS,
      LayerFSBackend,
      maxArchiveBytes,
    });
  }

  constructor(fields) {
    Object.assign(this, fields);
    this.clients = new Map();
    this.resetState = 'fresh';
  }

  async status() {
    return {
      ...(await this.runtime.status()),
      sourceCommit: this.installed.commit,
      image: this.installed.image,
      storeProfile: 'Disposable',
      crashDurability: false,
      resetState: this.resetState,
      maxArchiveBytes: this.maxArchiveBytes,
    };
  }

  async project(projectId) {
    const project = (await this.runtime.project.list()).find(
      (item) => item.name === projectId && item.initialized,
    );
    if (!project?.volume)
      throw new BridgeError(
        'NoSuchProject',
        'The Camp LayerFS project is not available.',
      );
    return project;
  }

  async client(projectId) {
    this.use();
    if (this.clients.has(projectId)) return this.clients.get(projectId);
    const project = await this.project(projectId);
    const settings = {
      HOME: this.home,
      STORE_PATH: path.join(
        this.options.volumes,
        project.volume,
        '_data/global/store.sqlite',
      ),
      VOLUME: project.volume,
      IMAGE: project.image ?? this.installed.image,
      DAEMON: this.installed.daemon,
      DOCKER: this.options.docker,
      CONTAINER_PREFIX: this.options.containerPrefix,
    };
    const saved = {};
    let opening;
    for (const [key, value] of Object.entries(settings)) {
      saved[key] = process.env[`LAYERFS_${key}`];
      process.env[`LAYERFS_${key}`] = value;
    }
    try {
      opening = this.LayerFS.open(projectId);
    } finally {
      for (const [key, value] of Object.entries(saved)) {
        if (value === undefined) delete process.env[`LAYERFS_${key}`];
        else process.env[`LAYERFS_${key}`] = value;
      }
    }
    const client = await opening;
    this.clients.set(projectId, client);
    return client;
  }

  async prepare({ sourceDir, baselineKey, environmentKey = 'files' }) {
    this.use();
    const source = await ownedDirectory(this.dataRoot, sourceDir);
    string(baselineKey, 'baselineKey');
    string(environmentKey, 'environmentKey');
    const projectId = identity('p', [
      this.installed.commit,
      this.installed.image,
      baselineKey,
      environmentKey,
    ]);
    const existing = (await this.runtime.project.list()).find(
      (project) => project.name === projectId,
    );
    if (existing) {
      if (!existing.initialized || !existing.volume)
        throw new BridgeError(
          'Unknown',
          'A previous project preparation is incomplete; it will not be repeated automatically.',
        );
      return {
        projectId,
        baselineKey,
        environmentKey,
        reused: true,
        image: this.installed.image,
      };
    }
    const job = await this.runtime.project.start_init(projectId, source, {
      timeout: 180_000,
    });
    let state = job;
    while (!state.ended) {
      await pause(100);
      state = await this.runtime.project.get_init(job.id);
      if (!state)
        throw new BridgeError(
          'Unknown',
          'The LayerFS preparation outcome is unavailable.',
        );
    }
    if (state.error)
      throw new BridgeError(state.error.code, state.error.message);
    return {
      projectId,
      baselineKey,
      environmentKey,
      reused: false,
      image: this.installed.image,
    };
  }

  workspaceFile(workspaceId) {
    if (!/^w-[0-9a-f]{40}$/.test(workspaceId))
      throw new BridgeError('InvalidInput', 'Invalid Camp workspace ID.');
    return path.join(this.home, '.camp', 'workspaces', `${workspaceId}.json`);
  }

  async workspace(workspaceId) {
    const record = await readState(this.workspaceFile(workspaceId));
    if (!record)
      throw new BridgeError('NotMounted', 'The Camp workspace does not exist.');
    if (record.status !== 'ready')
      throw new BridgeError(
        record.status === 'closed' ? 'NotMounted' : 'Unknown',
        `The Camp workspace is ${record.status}; it will not be started again.`,
      );
    return record;
  }

  async create({ projectId, baseCommit = 'main', runId }) {
    this.use();
    string(projectId, 'projectId', 63);
    string(baseCommit, 'baseCommit', 256);
    string(runId, 'runId', 128);
    const workspaceId = identity('w', [projectId, runId]);
    const file = this.workspaceFile(workspaceId);
    const existing = await readState(file);
    if (existing) {
      if (existing.baseCommit !== baseCommit)
        throw new BridgeError(
          'Conflict',
          'This Run already has a different resource baseline.',
        );
      return this.workspace(workspaceId);
    }
    const client = await this.client(projectId);
    const record = {
      workspaceId,
      projectId,
      runId,
      baseCommit,
      branch: identity('run', [runId]),
      status: 'creating',
      image: this.installed.image,
    };
    await saveState(file, record);
    try {
      await client.branch.fork(record.branch, baseCommit);
      const sandbox = await client.sandbox.create();
      record.sandboxId = sandbox.sandbox;
      await saveState(file, record);
      const publicSandboxes = await client.project.list_sandboxes(1000, 0);
      const publicSandbox = publicSandboxes.items.find(
        (item) => item.sandbox === sandbox.sandbox,
      );
      if (!publicSandbox)
        throw new BridgeError(
          'Unknown',
          'The newly created Sandbox was not listed by LayerFS.',
        );
      record.containerName = publicSandbox.name;
      await saveState(file, record);
      const mounted = await client.workspace.mount(
        sandbox.sandbox,
        record.branch,
      );
      record.mountPath = mounted.path;
      await saveState(file, record);
      const identityResult = await client.workspace.exec(
        mounted,
        'id -u; id -g',
        'Observe the isolated command identity',
      );
      const numbers = identityResult.stdout?.trim().split(/\s+/).map(Number);
      if (
        identityResult.exitCode !== 0 ||
        numbers?.length !== 2 ||
        numbers.some((value) => !Number.isSafeInteger(value) || value <= 0)
      )
        throw new BridgeError(
          'Unsupported',
          'LayerFS did not provide a non-root command identity.',
        );
      [record.uid, record.gid] = numbers;
      record.status = 'ready';
      await saveState(file, record);
      return record;
    } catch (error) {
      error.details = { workspace: record };
      throw error;
    }
  }

  async checkpoint({ workspaceId, message = 'Camp checkpoint', quiescent }) {
    if (quiescent !== true)
      throw new BridgeError(
        'InvalidInput',
        'The command owner must confirm quiescence before a checkpoint.',
      );
    const record = await this.workspace(workspaceId);
    const client = await this.client(record.projectId);
    const result = await client.workspace.commit(
      { sandbox: record.sandboxId, path: record.mountPath },
      string(message, 'message'),
    );
    return { workspaceId, ...result };
  }

  async export({ workspaceId, targetArchive, quiescent, excludeNames = [] }) {
    if (quiescent !== true)
      throw new BridgeError(
        'InvalidInput',
        'The command owner must confirm quiescence before exporting files.',
      );
    const record = await this.workspace(workspaceId);
    if (
      !Array.isArray(excludeNames) ||
      excludeNames.length > 64 ||
      excludeNames.some(
        (name) =>
          typeof name !== 'string' ||
          !/^[A-Za-z0-9_.-]{1,128}$/.test(name) ||
          name === '.' ||
          name === '..',
      )
    )
      throw new BridgeError(
        'InvalidInput',
        'Archive exclusions must be bounded file basenames.',
      );
    const archivePath = await ownedOutput(this.dataRoot, targetArchive);
    const client = await this.client(record.projectId);
    const archive = await open(archivePath, 'wx', 0o600);
    const output = archive.createWriteStream();
    const completion = finished(output);
    completion.catch(() => {});
    try {
      const result = await client.workspace.exec(
        { sandbox: record.sandboxId, path: record.mountPath },
        `/usr/local/bin/node /opt/camp/layerfs/export.mjs ${shellQuote(JSON.stringify({ maxBytes: this.maxArchiveBytes, excludeNames }))}`,
        'Export the settled Camp result',
        { stdout: output },
      );
      output.end();
      await completion;
      if (result.exitCode !== 0)
        throw new BridgeError(
          result.exitCode === 74
            ? 'ArchiveLimit'
            : result.exitCode === 75
              ? 'Timeout'
              : 'Failed',
          `Result export failed: ${result.stderr ?? 'tar failed'}`,
        );
      const hash = createHash('sha256');
      for await (const chunk of createReadStream(archivePath))
        hash.update(chunk);
      return {
        archivePath,
        bytes: (await stat(archivePath)).size,
        sha256: hash.digest('hex'),
      };
    } catch (error) {
      output.destroy();
      await completion.catch(() => {});
      await rm(archivePath, { force: true });
      throw error;
    }
  }

  async closeWorkspace({ workspaceId, discard = false }) {
    const record = await readState(this.workspaceFile(workspaceId));
    if (!record)
      throw new BridgeError('NotMounted', 'The Camp workspace does not exist.');
    if (record.status === 'closed') return { workspaceId, closed: true };
    const client = await this.client(record.projectId);
    try {
      if (record.sandboxId) {
        const mounts = await client.sandbox.list_workspaces(
          record.sandboxId,
          1000,
          0,
        );
        for (const mount of mounts.items)
          await client.workspace.unmount(
            { sandbox: record.sandboxId, path: mount.path },
            discard,
          );
        await client.sandbox.close(record.sandboxId);
      }
    } catch (error) {
      if (error.code !== 'NoSuchSandbox') throw error;
      await this.runtime.sandbox.reclaim(record.projectId);
    }
    record.status = 'closed';
    await saveState(this.workspaceFile(workspaceId), record);
    return { workspaceId, closed: true };
  }

  async history({ projectId }) {
    return this.LayerFSBackend.of(
      await this.client(projectId),
    ).store.project.get_history();
  }

  async removeProject({ projectId }) {
    this.use();
    if (!/^p-[0-9a-f]{40}$/.test(projectId))
      throw new BridgeError('InvalidInput', 'Invalid Camp project ID.');
    const receipts = (await this.workspaces()).filter(
      (record) => record.projectId === projectId,
    );
    if (receipts.some((record) => record.status !== 'closed'))
      throw new BridgeError(
        'Busy',
        'Close all Camp workspaces before removing their project.',
      );
    const client = this.clients.get(projectId);
    if (client) {
      await client.close();
      this.clients.delete(projectId);
    }
    const result = await this.runtime.project.remove(projectId, {
      confirm: projectId,
      stopSandboxes: false,
    });
    // Receipts guard against replay while a project exists. SQLite owns completed
    // Run identity after eviction; only discard receipts after Store removal succeeds.
    for (const record of receipts)
      await rm(this.workspaceFile(record.workspaceId));
    if (receipts.length) {
      const directory = await open(
        path.join(this.home, '.camp', 'workspaces'),
        'r',
      );
      try {
        await directory.sync();
      } finally {
        await directory.close();
      }
    }
    return result;
  }

  async workspaces() {
    const directory = path.join(this.home, '.camp', 'workspaces');
    let names;
    try {
      names = await readdir(directory);
    } catch (error) {
      if (error.code === 'ENOENT') return [];
      throw error;
    }
    return Promise.all(
      names
        .filter((name) => /^w-[0-9a-f]{40}\.json$/.test(name))
        .map((name) => readState(path.join(directory, name))),
    );
  }

  use() {
    if (this.resetState === 'resetting' || this.resetState === 'failed')
      throw new BridgeError(
        'Unavailable',
        'Startup cleanup has not been confirmed; reopen the bridge and complete cleanup before preparing work.',
      );
    this.resetState = 'used';
  }

  async beginReset() {
    if (this.resetState !== 'fresh')
      throw new BridgeError(
        'Busy',
        'Reset is available only before this bridge admits project operations.',
      );
    this.resetState = 'resetting';
    try {
      return await this.runtime.sandbox.list();
    } catch (error) {
      this.resetState = 'failed';
      throw error;
    }
  }

  async reset() {
    if (this.resetState !== 'resetting')
      throw new BridgeError(
        'Busy',
        'Begin startup cleanup before discarding disposable projects.',
      );
    try {
      const sandboxes = await this.runtime.sandbox.list();
      if (sandboxes.some((sandbox) => sandbox.running))
        throw new BridgeError(
          'Busy',
          'The command owner must confirm all Sandbox containers are stopped before reset.',
        );
      const projects = await this.runtime.project.list();
      const volumes = (await this.runtime.volume.list()).filter(
        (volume) => volume.owner === 'this',
      );
      const removed = [];
      for (const project of projects) {
        if (!/^p-[0-9a-f]{40}$/.test(project.name))
          throw new BridgeError(
            'Unknown',
            'The private LayerFS home contains an unexpected project; cleanup stopped.',
          );
        const candidates = volumes.filter(
          (volume) => volume.project === project.name,
        );
        const volume =
          project.volume ??
          (candidates.length === 1 ? candidates[0].name : undefined);
        if (!volume)
          throw new BridgeError(
            'Unknown',
            'An incomplete LayerFS project has no unambiguous public volume identity; cleanup stopped.',
          );
        await this.runtime.project.remove(project.name, {
          confirm: project.name,
          volume,
          stopSandboxes: false,
        });
        removed.push(project.name);
      }
      const leftovers = await this.runtime.sandbox.list_leftovers();
      if (leftovers.length) {
        const result = await this.runtime.sandbox.clean_leftovers(
          leftovers.map((sandbox) => sandbox.name),
        );
        if (result.results.some((entry) => !entry.ok))
          throw new BridgeError(
            'Unknown',
            'Some leftover Sandbox containers could not be removed.',
            result,
          );
      }
      const orphanVolumes = (await this.runtime.volume.list()).filter(
        (volume) => volume.owner === 'this',
      );
      if (orphanVolumes.length) {
        const result = await this.runtime.volume.remove(
          orphanVolumes.map((volume) => volume.name),
        );
        if (result.results.some((entry) => !entry.ok))
          throw new BridgeError(
            'Unknown',
            'Some disposable Store volumes could not be removed.',
            result,
          );
      }
      if (
        (await this.runtime.sandbox.list()).length ||
        (await this.runtime.project.list()).length
      )
        throw new BridgeError(
          'Unknown',
          'LayerFS cleanup did not reach an empty runtime.',
        );
      await rm(path.join(this.home, '.camp', 'workspaces'), {
        recursive: true,
        force: true,
      });
      this.resetState = 'ready';
      return { reset: true, removedProjects: removed };
    } catch (error) {
      this.resetState = 'failed';
      throw error;
    }
  }

  async close() {
    for (const client of this.clients.values()) await client.close();
    this.clients.clear();
    await this.runtime.close();
  }
}
