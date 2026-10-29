import { join } from 'node:path';
import { command, root } from './lib.mjs';

await command(process.execPath, [join(root, 'scripts/package-linux.mjs')]);
console.log(
  'Linux Runner installer: dist/camp-runner-linux-amd64. Setup installs its fixed runtime once. The offline package includes the identical runtime. No service is installed automatically.',
);
