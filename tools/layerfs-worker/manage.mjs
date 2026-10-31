import { Buffer } from 'node:buffer';
import process from 'node:process';
import { administer } from './administration.mjs';

const parts = [];
let bytes = 0;
try {
  for await (const chunk of process.stdin) {
    bytes += chunk.length;
    if (bytes > 64 << 10)
      throw new Error('Checkpoint administration input exceeds 64 KiB.');
    parts.push(chunk);
  }
  const result = await administer(
    JSON.parse(Buffer.concat(parts).toString('utf8')),
  );
  process.stdout.write(`${JSON.stringify(result)}\n`);
} catch (error) {
  process.stdout.write(
    `${JSON.stringify({ error: { code: error.code ?? 'Unknown', message: error.message, recoveryId: error.recoveryId } })}\n`,
  );
  process.exitCode = 1;
}
