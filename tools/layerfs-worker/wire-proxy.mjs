// Byte counter only. Never records the administrative request contents.
import { chmod, mkdir, mkdtemp, rm, rmdir } from 'node:fs/promises';
import { createServer, connect } from 'node:net';
import path from 'node:path';
import process from 'node:process';

let directory;
if (process.argv[3]) {
  const selected = process.argv[3];
  if (
    !/^\/(?:private\/)?tmp\/camp-wire-[a-zA-Z0-9]+\/docker\.sock$/.test(
      selected,
    )
  )
    throw new Error(
      'Rechecks require the retained private counter socket name.',
    );
  directory = path.dirname(selected);
  await mkdir(directory, { mode: 0o700 }); // Exclusive: never replace an active counter.
} else directory = await mkdtemp('/tmp/camp-wire-');
await chmod(directory, 0o700);
const socket = path.join(directory, 'docker.sock');
const totals = { sent: 0, received: 0, connections: 0 };
const peers = new Set();
const server = createServer({ allowHalfOpen: true }, (front) => {
  const back = connect({ path: process.argv[2], allowHalfOpen: true });
  totals.connections++;
  peers.add(front);
  peers.add(back);
  front.on('data', (chunk) => {
    totals.sent += chunk.length;
  });
  back.on('data', (chunk) => {
    totals.received += chunk.length;
  });
  front.pipe(back);
  back.pipe(front);
  front.on('error', () => back.destroy());
  back.on('error', () => front.destroy());
  front.once('close', () => {
    peers.delete(front);
    if (!front.writableFinished) back.destroy();
  });
  // pipe() ends the destination after its queued writes drain. Destroying it
  // when the source closes can truncate the final chunk of a large response.
  back.once('close', () => {
    peers.delete(back);
    if (!back.readableEnded) front.destroy();
  });
});
await new Promise((resolve) => server.listen(socket, resolve));
await chmod(socket, 0o600);
process.send({ socket });
process.on('message', async (request) => {
  if (request.action === 'stats') process.send({ ...totals });
  if (request.action === 'close') {
    for (const peer of peers) peer.destroy();
    await new Promise((resolve) => server.close(resolve));
    await rm(socket, { force: true });
    await rmdir(directory);
    process.disconnect();
  }
});
