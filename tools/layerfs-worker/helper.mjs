import { mkdtemp, rm } from 'node:fs/promises';
import console from 'node:console';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { applyTree } from './apply.mjs';
import { captureTree, scanTree } from './tree.mjs';
import { encodeCapture, receiveCapture } from './transfer.mjs';
import { writeBytes } from './protocol.mjs';

const [operation, directory, digest, previous] = process.argv.slice(2);
if (
  !['scan', 'apply', 'export'].includes(operation) ||
  !/^\/workspaces\/[^/\0]{1,128}$/.test(directory)
)
  throw new Error('The helper requires an exact SDK-mounted Workspace.');
let temporary;
try {
  if (operation === 'scan')
    process.stdout.write(JSON.stringify(await scanTree(directory)));
  else {
    temporary = await mkdtemp(path.join(os.tmpdir(), 'camp-checkpoint-'));
    if (operation === 'apply') {
      const captured = await receiveCapture(process.stdin, temporary, {
        digest,
      });
      process.stdout.write(
        JSON.stringify(
          await applyTree(directory, previous, {
            root: temporary,
            ...captured,
          }),
        ),
      );
    } else {
      const captured = await captureTree(directory, temporary);
      if (captured.digest !== digest)
        throw new Error('Exported tree differs from the confirmed checkpoint.');
      for await (const chunk of encodeCapture(captured))
        await writeBytes(process.stdout, chunk);
    }
  }
} catch (error) {
  console.error(
    JSON.stringify({ code: error.code ?? 'Failed', message: error.message }),
  );
  process.exitCode = 1;
} finally {
  if (temporary) await rm(temporary, { recursive: true, force: true });
}
