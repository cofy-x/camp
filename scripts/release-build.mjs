import assert from 'node:assert/strict';
import { join } from 'node:path';
import { command, root } from './lib.mjs';
import { releaseIdentity } from './release.mjs';

assert.equal(
  process.getuid(),
  0,
  'Native release acceptance requires the dedicated privileged builder.',
);
const identity = await releaseIdentity();
assert.ok(identity.published);
await command('make', [
  'install',
  'install-dsh',
  'install-project-runtime',
  'install-layerfs',
]);
await command('make', ['package-daemon']);
await command('make', ['test-remote-native']);
await command('make', ['smoke-bundle']);
await command('docker', [
  'build',
  '--target',
  'release',
  '--build-arg',
  `VERSION=${identity.version}`,
  '--build-arg',
  `SOURCE_COMMIT=${identity.sourceCommit}`,
  '-t',
  `camp-server:${identity.version}`,
  '.',
]);
await command(process.execPath, [
  join(root, 'scripts/smoke-server-image.mjs'),
  `camp-server:${identity.version}`,
]);
await command(process.execPath, [join(root, 'scripts/release-stage.mjs')]);
await command('docker', [
  'image',
  'save',
  '--output',
  join(root, 'dist/server-image.tar'),
  `camp-server:${identity.version}`,
]);
