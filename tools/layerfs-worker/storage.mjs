import { Buffer } from 'node:buffer';
import process from 'node:process';
import { inspectStorage } from './retention.mjs';

try {
  const parts = [];
  let bytes = 0;
  for await (const part of process.stdin) {
    bytes += part.length;
    if (bytes > 64 << 10)
      throw new Error('Storage inspection input exceeds its bound.');
    parts.push(part);
  }
  const report = await inspectStorage(
    JSON.parse(Buffer.concat(parts).toString('utf8')),
  );
  process.stdout.write(`${JSON.stringify(report)}\n`);
} catch (error) {
  process.stdout.write(
    `${JSON.stringify({ error: { code: error.code ?? 'Unknown', message: 'Checkpoint storage inspection could not complete. Retained data is unchanged.' } })}\n`,
  );
  process.exitCode = 1;
}
