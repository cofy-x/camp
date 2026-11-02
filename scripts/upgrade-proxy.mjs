import { createServer, request } from 'node:http';
import { connect } from 'node:net';

// Private loopback fault injection: commit completion HTTP requests but retain
// their replies and subsequent receipt readback. Control grants, coordination,
// model streaming and unrelated HTTP traffic continue normally.
export async function runnerReplyProxy(targetPort) {
  let held = false;
  const completed = new Set();
  const replies = new Map();
  const sockets = new Set();
  const forward = (source, target) => {
    target.writeHead(source.statusCode, source.headers);
    source.pipe(target);
  };
  const server = createServer((incoming, response) => {
    const upstream = request(
      {
        host: '127.0.0.1',
        port: targetPort,
        method: incoming.method,
        path: incoming.url,
        headers: incoming.headers,
      },
      (source) => {
        const operation = incoming.url.match(
          /^\/api\/daemon\/operations\/([^/]+)\/(complete|finalizing)$/,
        );
        const drop =
          held &&
          operation &&
          (operation[2] === 'complete' || completed.has(operation[1]));
        if (drop) {
          completed.add(operation[1]);
          replies.set(source, response);
          response.once('close', () => {
            replies.delete(source);
            source.destroy();
          });
        } else forward(source, response);
      },
    );
    upstream.on('error', () => {
      if (!response.headersSent) response.writeHead(502);
      response.end();
    });
    response.once('close', () => upstream.destroy());
    incoming.pipe(upstream);
  });
  // Forward authenticated WebSocket bytes without decoding or retaining them.
  server.on('upgrade', (incoming, client, head) => {
    const upstream = connect(targetPort, '127.0.0.1', () => {
      const headers = incoming.rawHeaders.reduce(
        (out, value, index, all) =>
          index % 2 === 0 ? out + `${value}: ${all[index + 1]}\r\n` : out,
        '',
      );
      upstream.write(
        `${incoming.method} ${incoming.url} HTTP/${incoming.httpVersion}\r\n${headers}\r\n`,
      );
      if (head.length) upstream.write(head);
      client.pipe(upstream);
      upstream.pipe(client);
    });
    sockets.add(client);
    sockets.add(upstream);
    for (const [socket, peer] of [
      [client, upstream],
      [upstream, client],
    ]) {
      socket.on('error', () => peer.destroy());
      socket.on('close', () => {
        sockets.delete(socket);
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
    },
    releaseReplies() {
      held = false;
      for (const [source, target] of replies)
        if (!target.destroyed) forward(source, target);
      replies.clear();
    },
    async close() {
      for (const socket of sockets) socket.destroy();
      for (const [source, target] of replies) {
        source.destroy();
        target.destroy();
      }
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    },
  };
}
