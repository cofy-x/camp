import { spawn, spawnSync } from 'node:child_process';
import { setTimeout, clearTimeout } from 'node:timers';
import { createHash, randomUUID } from 'node:crypto';
import { lstat, mkdir, readFile, realpath } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { performance } from 'node:perf_hooks';
import {
  body,
  captureWireBytes,
  readMessage,
  writeBytes,
  writeMessage,
} from './protocol.mjs';
import { failure, readState, saveState } from './state.mjs';
import { ByteReader, encodeCapture, receiveCapture } from './transfer.mjs';

export class WorkerClient {
  static async open({
    home,
    socket,
    installation = path.join(
      import.meta.dirname,
      'node_modules/.camp-worker/installed.json',
    ),
    timeoutMs = 180_000,
    dockerBinary = 'docker',
    qualification = false,
  }) {
    home = path.resolve(home);
    await mkdir(home, { recursive: true, mode: 0o700 });
    home = await realpath(home);
    const owner = await lstat(home);
    if (
      !owner.isDirectory() ||
      owner.uid !== process.getuid() ||
      owner.mode & 0o077
    )
      throw failure(
        'Unauthorized',
        'Worker configuration requires a private, owner-controlled directory.',
      );
    if (!socket || !path.isAbsolute(socket))
      throw failure('InvalidInput', 'Select an explicit local Docker socket.');
    socket = await realpath(socket);
    if (!(await lstat(socket)).isSocket())
      throw failure(
        'InvalidInput',
        'The selected Docker endpoint is not a local socket.',
      );
    const installed = JSON.parse(await readFile(installation, 'utf8'));
    const pin = JSON.parse(
      await readFile(path.join(import.meta.dirname, 'pin.json'), 'utf8'),
    );
    const layerfs = JSON.parse(
      await readFile(
        path.join(import.meta.dirname, '../layerfs-runtime/pin.json'),
        'utf8',
      ),
    );
    if (
      installed.commit !== layerfs.commit ||
      installed.protocol !== pin.protocol ||
      installed.platform !== pin.platform ||
      !/^sha256:[a-f0-9]{64}$/.test(installed.image)
    )
      throw failure(
        'Conflict',
        'The worker installation differs from the pinned runtime.',
      );
    const environment = {
      PATH: process.env.PATH,
      HOME: home,
      DOCKER_HOST: `unix://${socket}`,
      DOCKER_CONFIG: path.join(home, 'docker-client'),
    };
    await saveState(path.join(environment.DOCKER_CONFIG, 'config.json'), {
      auths: {},
    });
    const docker = (args, json = true) => {
      const result = spawnSync(dockerBinary, args, {
        env: environment,
        encoding: 'utf8',
        maxBuffer: 96 << 20,
      });
      if (result.error || result.status !== 0)
        throw failure(
          'Docker',
          result.error?.message ?? result.stderr.slice(0, 8192),
        );
      return json ? JSON.parse(result.stdout) : result.stdout.trim();
    };
    const image = docker(['image', 'inspect', installed.image])[0];
    if (
      image.Architecture !== 'arm64' ||
      image.Os !== 'linux' ||
      image.Config.Labels?.['camp.layerfs.commit'] !== layerfs.commit ||
      image.Config.Labels?.['camp.runtime'] !== 'layerfs-worker'
    )
      throw failure(
        'Conflict',
        'The actual worker image does not match its installation.',
      );
    const info = docker(['info', '--format', '{{json .}}']);
    if (!['aarch64', 'arm64'].includes(info.Architecture))
      throw failure(
        'Unsupported',
        'The selected Linux engine must be native ARM64.',
      );
    const file = path.join(home, 'worker.json');
    let identity = await readState(file);
    if (!identity) {
      const id = randomUUID();
      identity = {
        schema: 1,
        id,
        volume: `camp-worker-${id}`,
        status: 'creating',
        socket,
        image: installed.image,
        engineId: info.ID,
      };
      await saveState(file, identity);
      docker(
        [
          'volume',
          'create',
          '--label',
          'cofy.camp.role=checkpoint-home',
          '--label',
          `cofy.camp.worker=${id}`,
          identity.volume,
        ],
        false,
      );
      identity.status = 'ready';
      await saveState(file, identity);
    }
    if (
      identity.status !== 'ready' ||
      identity.socket !== socket ||
      identity.image !== installed.image
    )
      throw failure(
        'Unknown',
        'An incomplete or incompatible worker installation is retained for explicit recovery.',
      );
    if (identity.engineId && identity.engineId !== info.ID)
      throw failure(
        'LostStore',
        'The selected Docker engine identity changed; preserve existing captures and recover explicitly.',
      );
    const owned = docker(['volume', 'inspect', identity.volume])[0];
    if (
      owned.Driver !== 'local' ||
      Object.keys(owned.Options ?? {}).length ||
      owned.Labels?.['cofy.camp.role'] !== 'checkpoint-home' ||
      owned.Labels?.['cofy.camp.worker'] !== identity.id
    )
      throw failure('Conflict', 'The worker home volume identity changed.');
    const running = docker(
      [
        'ps',
        '--filter',
        `label=cofy.camp.worker=${identity.id}`,
        '--format',
        '{{json .}}',
      ],
      false,
    );
    if (running)
      throw failure(
        'Busy',
        'An existing worker for this home is still running; observe and stop that owned worker before reopening.',
      );
    const name = `camp-worker-${randomUUID()}`;
    const prefix = `cw-${createHash('sha256').update(identity.id).digest('hex').slice(0, 12)}-`;
    const config = {
      home: `/var/lib/docker/volumes/${identity.volume}/_data`,
      image: installed.image,
      protocol: pin.protocol,
      commit: installed.commit,
      prefix,
      qualification: qualification === true,
    };
    const child = spawn(
      dockerBinary,
      [
        'run',
        '--rm',
        '-i',
        '--init',
        '--name',
        name,
        '--label',
        'cofy.camp.role=checkpoint-worker',
        '--label',
        `cofy.camp.worker=${identity.id}`,
        '--platform',
        'linux/arm64',
        '--pull=never',
        '--restart=no',
        '--network=host',
        '--read-only',
        '--memory=4g',
        '--cpus=4',
        '--pids-limit=256',
        '--tmpfs',
        '/tmp:rw,nosuid,nodev,mode=1777',
        '--mount',
        'type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock',
        '--mount',
        `type=bind,source=${info.DockerRootDir}/volumes,target=/var/lib/docker/volumes`,
        installed.image,
        '/opt/camp/layerfs/worker/worker.mjs',
      ],
      { env: environment, stdio: ['pipe', 'pipe', 'pipe'] },
    );
    const client = new WorkerClient({
      home,
      socket,
      installed,
      identity,
      config,
      child,
      name,
      docker,
      timeoutMs,
      input: new ByteReader(child.stdout),
    });
    client.finished = new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('close', (code) => {
        client.closed = true;
        resolve(code);
      });
    });
    client.finished.catch(() => {});
    child.stderr.on('data', (chunk) => {
      client.diagnostic = `${client.diagnostic ?? ''}${chunk}`.slice(-8192);
    });
    child.stdin.on('error', () => {});
    try {
      const started = performance.now();
      client.status = await client.request({ op: 'open', config });
      if (
        client.status.protocol !== pin.protocol ||
        client.status.sourceCommit !== installed.commit ||
        client.status.image !== installed.image ||
        client.status.sdkVersion !== installed.version ||
        client.status.platform !== 'linux/arm64'
      )
        throw failure(
          'Conflict',
          'The actual checkpoint worker SDK, image or protocol differs from its installation.',
        );
      client.startupMs = performance.now() - started;
      const cli = docker(['version', '--format', '{{json .Client}}']);
      client.engine = {
        id: info.ID,
        dockerVersion: info.ServerVersion,
        operatingSystem: info.OperatingSystem,
        dataRoot: info.DockerRootDir,
        architecture: info.Architecture,
        kernel: info.KernelVersion,
        driver: info.Driver,
        cpus: info.NCPU,
        memoryBytes: info.MemTotal,
        client: {
          version: cli.Version,
          go: cli.GoVersion,
          commit: cli.GitCommit,
          os: cli.Os,
          architecture: cli.Arch,
        },
      };
      await saveState(path.join(home, 'last-start.json'), {
        status: client.status,
        engine: client.engine,
        startupMs: client.startupMs,
      });
      return client;
    } catch (error) {
      child.stdin.destroy();
      await client.finished;
      throw error;
    }
  }

  constructor(fields) {
    Object.assign(this, fields);
  }
  async request(
    request,
    {
      binary,
      receive,
      beforeDispatch,
      completeExport = false,
      retainedCapture,
    } = {},
  ) {
    if (this.closed || this.busy || this.uncertain)
      throw failure(
        'Unavailable',
        'The worker connection is closed, busy or uncertain.',
      );
    this.busy = true;
    const id = randomUUID();
    let timer;
    let phase = 'dispatch';
    const operation = async () => {
      await beforeDispatch?.();
      await writeMessage(this.child.stdin, { id, ...request });
      if (binary)
        for await (const chunk of binary)
          await writeBytes(this.child.stdin, chunk);
      phase = 'response';
      const response = await readMessage(this.input);
      if (response?.id !== id)
        throw failure(
          'Protocol',
          'Worker response identity differs from its request.',
        );
      if (response.error)
        throw failure(response.error.code, response.error.message);
      phase = 'receive';
      const value = receive
        ? await receive(response.value, this.input)
        : response.value;
      if (completeExport) {
        // The serialized worker reads status only after export, helper exit and
        // release settle. Keep the same admission lock and deadline across both
        // messages; complete bytes alone never confirm a successful export.
        phase = 'export-release';
        const barrier = randomUUID();
        await writeMessage(this.child.stdin, { id: barrier, op: 'status' });
        const completion = await readMessage(this.input);
        if (completion?.id !== barrier || completion.error)
          throw failure(
            'Protocol',
            'The worker did not acknowledge export release.',
          );
        for (const field of ['storeId', 'image', 'protocol', 'sourceCommit'])
          if (completion.value?.[field] !== this.status[field])
            throw failure(
              'Protocol',
              'The worker identity changed before export release completed.',
            );
      }
      return value;
    };
    try {
      return await Promise.race([
        operation(),
        new Promise((_, reject) => {
          timer = setTimeout(() => {
            this.uncertain = true;
            this.child.stdin.destroy();
            this.child.kill();
            reject(
              failure(
                'Unknown',
                'The worker call timed out after dispatch; it may still complete. No mutation will be replayed.',
              ),
            );
          }, this.timeoutMs);
        }),
      ]);
    } catch (error) {
      if (
        this.uncertain ||
        (completeExport && phase === 'export-release') ||
        ['Truncated', 'EPIPE', 'ERR_STREAM_DESTROYED', 'Protocol'].includes(
          error.code,
        )
      ) {
        this.uncertain = true;
        const outcome = failure(
          'Unknown',
          `Worker ${request.op} did not confirm ${phase}; retain the snapshot and observe the operation before further work.`,
        );
        this.failedRequest = { id, operation: request.op, phase };
        try {
          await saveState(path.join(this.home, 'failures', `${id}.json`), {
            schema: 1,
            request: id,
            operation: request.op,
            operationId: request.args?.operationId,
            phase,
            state: 'unknown',
            retainedCapture,
            observedAt: new Date().toISOString(),
            error: {
              code: String(error.code ?? 'Unknown').slice(0, 128),
              message: String(error.message).slice(0, 2048),
            },
            diagnostic: this.diagnostic?.slice(-8192),
          });
        } catch (recordError) {
          outcome.message += ` Failure evidence could not be saved: ${String(recordError.code ?? 'Unknown').slice(0, 128)}.`;
        }
        throw outcome;
      }
      throw error;
    } finally {
      clearTimeout(timer);
      this.busy = false;
    }
  }
  async upload(operationId, capture, wanted, metrics = {}) {
    return this.request(
      {
        op: 'upload',
        args: { operationId },
        binaryBytes: captureWireBytes(capture, wanted),
      },
      { binary: encodeCapture(capture, wanted, metrics) },
    );
  }
  async export(operationId, destination) {
    return this.request(
      { op: 'export', args: { operationId } },
      {
        completeExport: true,
        retainedCapture: destination,
        receive: async (value, reader) => {
          const captured = await receiveCapture(
            body(reader, value.binaryBytes),
            destination,
            { digest: value.receipt.binding.digest, smallFilePack: true },
          );
          return { root: destination, ...captured, receipt: value.receipt };
        },
      },
    );
  }
  async close() {
    const retainDrainFailure = async () => {
      try {
        await saveState(
          path.join(this.home, 'failures', `${randomUUID()}.json`),
          {
            schema: 1,
            state: 'unknown',
            phase: 'drain',
            observedAt: new Date().toISOString(),
            failedRequest: this.failedRequest,
            diagnostic: this.diagnostic?.slice(-8192),
          },
        );
      } catch (error) {
        return ` Drain evidence could not be saved: ${String(error.code ?? 'Unknown').slice(0, 128)}.`;
      }
      return '';
    };
    if (this.closed) {
      const code = await this.finished;
      if (code !== 0) {
        const note = await retainDrainFailure();
        throw failure(
          'Unknown',
          `The worker exited without a clean drain confirmation: ${this.diagnostic ?? ''}${note}`,
        );
      }
      return;
    }
    this.child.stdin.end();
    let timer;
    try {
      const code = await Promise.race([
        this.finished,
        new Promise((_, reject) => {
          timer = setTimeout(
            () =>
              reject(
                failure(
                  'Unknown',
                  'Worker drain was not confirmed; its owned resources are retained.',
                ),
              ),
            30_000,
          );
        }),
      ]);
      if (code !== 0) {
        const note = await retainDrainFailure();
        throw failure(
          'Unknown',
          `The worker did not shut down cleanly: ${this.diagnostic ?? ''}${note}`,
        );
      }
    } finally {
      clearTimeout(timer);
    }
  }
}
