import { connect, createServer } from 'node:net';

// A private loopback fixture can lose Runner completion replies while allowing
// the Server to commit the outcome. Never inspect or log authenticated bytes.
export async function runnerReplyProxy(targetPort) {
  const sockets = new Set();
  const control = new Map();
  let held = false;
  const server = createServer((client) => {
    const upstream = connect(targetPort, '127.0.0.1');
    sockets.add(client);
    sockets.add(upstream);
    let headers = '';
    const observe = (chunk) => {
      headers += chunk.toString('latin1');
      if (headers.length > 65536) {
        client.destroy();
        return;
      }
      if (!headers.includes('\r\n\r\n')) return;
      client.removeListener('data', observe);
      if (headers.startsWith('GET /api/daemon/connect ')) {
        control.set(upstream, client);
        if (held) upstream.unpipe(client);
      }
      headers = '';
    };
    client.on('data', observe);
    client.pipe(upstream);
    upstream.pipe(client);
    for (const [socket, peer] of [
      [client, upstream],
      [upstream, client],
    ]) {
      socket.on('error', () => peer.destroy());
      socket.on('close', () => {
        sockets.delete(socket);
        control.delete(upstream);
        peer.destroy();
      });
    }
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  return {
    origin: `http://127.0.0.1:${server.address().port}`,
    holdReplies() {
      held = true;
      for (const [upstream, client] of control) upstream.unpipe(client);
    },
    releaseReplies() {
      held = false;
      for (const [upstream, client] of control) upstream.pipe(client);
    },
    async close() {
      for (const socket of sockets) socket.destroy();
      await new Promise((resolve) => server.close(resolve));
    },
  };
}
