import assert from 'node:assert/strict';
import { appendFile, stat } from 'node:fs/promises';

const endpoint = new URL(process.env.DOCKER_HOST);
assert.equal(
  endpoint.protocol,
  'unix:',
  'Native release acceptance requires a local engine.',
);
assert.ok(
  !endpoint.host && !endpoint.search && !endpoint.hash,
  'Use an absolute local Docker socket.',
);
const socket = decodeURIComponent(endpoint.pathname);
assert.ok(socket.startsWith('/') && !/[\0\r\n]/.test(socket));
assert.ok(
  (await stat(socket)).isSocket(),
  'The selected Docker endpoint is not a local socket.',
);
await appendFile(process.env.GITHUB_OUTPUT, `path=${socket}\n`);
