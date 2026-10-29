import { Buffer } from 'node:buffer';
import { readFile } from 'node:fs/promises';
import { createInterface } from 'node:readline';
import process from 'node:process';
import { CampLayerFS } from './runtime.mjs';
import { BridgeError, errorRecord } from './validation.mjs';

if (process.argv.length !== 3)
  throw new Error('Usage: node bridge.mjs /absolute/path/to/config.json');
const config = JSON.parse(await readFile(process.argv[2], 'utf8'));
const runtime = await CampLayerFS.open(config);
const methods = {
  'runtime.status': () => runtime.status(),
  'runtime.reset.begin': () => runtime.beginReset(),
  'runtime.reset': () => runtime.reset(),
  'project.prepare': (params) => runtime.prepare(params),
  'project.history': (params) => runtime.history(params),
  'project.remove': (params) => runtime.removeProject(params),
  'workspace.create': (params) => runtime.create(params),
  'workspace.checkpoint': (params) => runtime.checkpoint(params),
  'workspace.export': (params) => runtime.export(params),
  'workspace.close': (params) => runtime.closeWorkspace(params),
  'workspace.list': () => runtime.workspaces(),
};
const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
try {
  for await (const line of lines) {
    let request;
    try {
      if (Buffer.byteLength(line) > 64 * 1024)
        throw new BridgeError(
          'InvalidInput',
          'The bridge request exceeds 64 KiB.',
        );
      request = JSON.parse(line);
      if (
        !request ||
        typeof request.id !== 'string' ||
        !request.id ||
        request.id.length > 128 ||
        typeof request.method !== 'string'
      )
        throw new BridgeError(
          'InvalidInput',
          'A bridge request requires a string id and method.',
        );
      if (request.method === 'shutdown') {
        await runtime.close();
        process.stdout.write(
          `${JSON.stringify({ id: request.id, result: { closed: true } })}\n`,
        );
        break;
      }
      const method = Object.hasOwn(methods, request.method)
        ? methods[request.method]
        : undefined;
      if (!method)
        throw new BridgeError(
          'UnknownMethod',
          'This LayerFS bridge operation is not supported.',
        );
      const result = await method(request.params ?? {});
      process.stdout.write(`${JSON.stringify({ id: request.id, result })}\n`);
    } catch (error) {
      process.stdout.write(
        `${JSON.stringify({ id: typeof request?.id === 'string' ? request.id : null, error: errorRecord(error) })}\n`,
      );
    }
  }
} finally {
  lines.close();
  await runtime.close();
}
