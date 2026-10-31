import process from 'node:process';
import { LinuxWorker } from './linux.mjs';
import { ByteReader } from './transfer.mjs';
import { body, readMessage, writeMessage } from './protocol.mjs';

// The only control channel is inherited stdin/stdout over the selected local
// Docker socket. No Agent environment, model capability or network listener.
const input = new ByteReader(process.stdin);
let worker,
  request,
  streaming = false;
try {
  request = await readMessage(input);
  if (request.op !== 'open')
    throw new Error('The worker requires an explicit startup request.');
  worker = await LinuxWorker.open(request.config);
  await writeMessage(process.stdout, {
    id: request.id,
    value: worker.status(),
  });
  for (;;) {
    request = await readMessage(input, true);
    if (!request) break;
    streaming = false;
    try {
      let value;
      switch (request.op) {
        case 'status':
          value = worker.status();
          break;
        case 'prepare':
          value = await worker.prepare(request.args);
          break;
        case 'upload':
          value = await worker.upload(
            request.args,
            body(input, request.binaryBytes),
          );
          break;
        case 'commit':
          value = await worker.commit(request.args);
          break;
        case 'observe':
          value = await worker.observe(request.args);
          break;
        case 'export':
          await worker.export(
            request.args,
            process.stdout,
            async (response) => {
              streaming = true;
              await writeMessage(process.stdout, {
                id: request.id,
                value: response,
              });
            },
          );
          continue;
        default:
          throw Object.assign(
            new Error('Unknown checkpoint worker operation.'),
            { code: 'InvalidInput' },
          );
      }
      await writeMessage(process.stdout, { id: request.id, value });
    } catch (error) {
      if (streaming || request.op === 'upload') throw error;
      await writeMessage(process.stdout, {
        id: request.id,
        error: { code: error.code ?? 'Unknown', message: error.message },
      });
    }
  }
} catch (error) {
  if (!streaming)
    await writeMessage(process.stdout, {
      id: request?.id,
      error: { code: error.code ?? 'Unknown', message: error.message },
    }).catch(() => {});
  process.exitCode = 1;
} finally {
  await worker?.close().catch(() => {
    process.exitCode = 1;
  });
  process.stdin.destroy();
}
