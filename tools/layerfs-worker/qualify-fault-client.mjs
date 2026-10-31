import { readFile } from 'node:fs/promises';
import process from 'node:process';
import { openHome } from './home.mjs';

const config = JSON.parse(await readFile(process.argv[2], 'utf8'));
const active = await openHome(config);
process.stdout.write(
  `${JSON.stringify({ phase: 'ready', container: active.worker.name, linuxHome: active.worker.config.home })}\n`,
);
await active.worker.request({
  op: 'qualification.fault',
  args: { point: 'delay-commit-reply' },
});
const request = active.worker.request.bind(active.worker);
active.worker.request = async (input, options) => {
  if (input.op === 'commit')
    process.stdout.write(
      `${JSON.stringify({ phase: 'commit-dispatched', operationId: input.args.operationId })}\n`,
    );
  return request(input, options);
};
try {
  await active.checkpoints.checkpoint(
    config.source,
    config.workspaceId,
    'coordinator-restart',
    { quiescent: true },
  );
} finally {
  await active.worker.close();
}
