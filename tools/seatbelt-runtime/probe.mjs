import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { spawn } from 'node:child_process';
import {
  mkdir,
  mkdtemp,
  readFile,
  realpath,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { createServer } from 'node:net';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { setTimeout, clearTimeout } from 'node:timers';
import { URL } from 'node:url';
import { SandboxManager } from '@anthropic-ai/sandbox-runtime';

if (process.platform !== 'darwin')
  throw new Error('This probe requires actual macOS.');
const directory = await realpath(
  await mkdtemp(path.join(os.tmpdir(), 'camp-seatbelt-')),
);
const workspace = path.join(directory, 'work[1]');
const privateData = path.join(directory, 'private');
const references = path.join(directory, 'references');
const home = path.join(workspace, '.home');
const temporary = path.join(workspace, '.tmp');
for (const dir of [workspace, privateData, references, home, temporary])
  await mkdir(dir, { recursive: true, mode: 0o700 });
await writeFile(path.join(workspace, 'input'), 'allowed');
await writeFile(path.join(privateData, '.env'), 'synthetic-private-sentinel');
await writeFile(path.join(references, 'evidence'), 'reference');
await symlink(path.join(privateData, '.env'), path.join(workspace, 'outside'));
const node = await realpath(process.execPath);
const literal = (value) => ({ path: value, literal: true });
const config = {
  network: {
    allowedDomains: [],
    deniedDomains: ['*'],
    allowUnixSockets: [],
    allowAllUnixSockets: false,
    allowLocalBinding: false,
    allowMachLookup: [],
  },
  filesystem: {
    denyRead: ['/'],
    allowRead: [
      workspace,
      references,
      '/System/Library',
      '/usr/lib',
      '/usr/share',
      '/usr/bin',
      '/bin',
      node,
    ].map(literal),
    allowWrite: [workspace].map(literal),
    denyWrite: [
      references,
      privateData,
      '/tmp/claude',
      '/private/tmp/claude',
    ].map(literal),
  },
  enableWeakerNetworkIsolation: false,
  allowAppleEvents: false,
};
const output = {
  reference: JSON.parse(await readFile(new URL('./pin.json', import.meta.url))),
  platform: process.platform,
  architecture: process.arch,
  checks: [],
  cleanup:
    'NOT_ESTABLISHED: upstream cleanupAfterCommand is a macOS no-op; Camp process ownership is qualified separately',
  passed: false,
};
const quote = (value) => `'${value.replaceAll("'", "'\\''")}'`;

async function run(code, current = config) {
  const wrapped = await SandboxManager.wrapWithSandboxArgv(
    `${quote(node)} --input-type=module -e ${quote(code)}`,
    '/bin/bash',
    current,
    undefined,
    workspace,
  );
  return new Promise((resolve, reject) => {
    const child = spawn(wrapped.argv[0], wrapped.argv.slice(1), {
      cwd: workspace,
      env: {
        PATH: '/usr/bin:/bin',
        HOME: home,
        TMPDIR: temporary,
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    const chunks = [];
    child.stdout.on('data', (chunk) => chunks.push(chunk));
    child.stderr.on('data', (chunk) => chunks.push(chunk));
    const timer = setTimeout(() => {
      child.kill('SIGKILL');
      reject(new Error('Bounded permission probe timed out'));
    }, 15_000);
    child.once('error', reject);
    child.once('close', (exitCode) => {
      clearTimeout(timer);
      resolve({ exitCode, text: Buffer.concat(chunks).toString('utf8') });
    });
  });
}

async function check(name, code) {
  const result = await run(code);
  assert.equal(result.exitCode, 0, `${name}: ${result.text}`);
  output.checks.push(name);
}

const server = createServer((socket) => socket.destroy());
const unix = createServer((socket) => socket.destroy());
try {
  await SandboxManager.initialize(config);
  const defaults = {
    ...config,
    filesystem: { ...config.filesystem, denyRead: [], allowRead: [] },
  };
  const exposed = await run(
    `import fs from 'node:fs'; if(fs.readFileSync(${JSON.stringify(path.join(privateData, '.env'))},'utf8')!=='synthetic-private-sentinel')process.exit(1)`,
    defaults,
  );
  assert.equal(
    exposed.exitCode,
    0,
    'The default read policy observation changed; review the dependency.',
  );
  output.checks.push(
    'The default read policy permits unrelated synthetic private data; it is not used as Camp policy.',
  );
  await check(
    'Literal workspace and read-only reference reads succeed.',
    `import fs from 'node:fs'; if(fs.readFileSync('input','utf8')!=='allowed'||fs.readFileSync(${JSON.stringify(path.join(references, 'evidence'))},'utf8')!=='reference')process.exit(1);fs.writeFileSync('result','native');`,
  );
  await check(
    'Private data, outside symlinks and reference writes are denied.',
    `import fs from 'node:fs'; for(const p of ${JSON.stringify([path.join(privateData, '.env'), path.join(workspace, 'outside')])}) {try{fs.readFileSync(p);process.exit(1)}catch(e){if(!['EPERM','EACCES'].includes(e.code))throw e}} try{fs.writeFileSync(${JSON.stringify(path.join(references, 'evidence'))},'changed');process.exit(1)}catch(e){if(!['EPERM','EACCES'].includes(e.code))throw e}`,
  );
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const socket = path.join(privateData, 'control.sock');
  await new Promise((resolve) => unix.listen(socket, resolve));
  await check(
    'Direct loopback and unrelated Unix sockets are denied.',
    `import net from 'node:net';for(const options of [{host:'127.0.0.1',port:${server.address().port}},{path:${JSON.stringify(socket)}}]) await new Promise((resolve,reject)=>{const s=net.connect(options);s.setTimeout(1000,()=>{s.destroy();reject(new Error('unexpected socket timeout'))});s.once('connect',()=>{s.destroy();reject(new Error('unexpected connection'))});s.once('error',resolve)})`,
  );
  await check(
    'Direct IPv4, IPv6 and UDP socket operations are denied.',
    `import net from 'node:net';import dgram from 'node:dgram';for(const host of ['192.0.2.1','::1'])await new Promise((resolve,reject)=>{const s=net.connect({host,port:443});s.setTimeout(1000,()=>{s.destroy();reject(new Error('socket was admitted'))});s.once('connect',()=>reject(new Error('unexpected connection')));s.once('error',resolve)});await new Promise((resolve,reject)=>{const s=dgram.createSocket('udp4');s.once('error',()=>{s.close();resolve()});s.send(Buffer.from('x'),53,'192.0.2.1',e=>{if(e){s.close();resolve()}else{s.close();reject(new Error('UDP send admitted'))}})})`,
  );
  output.passed = true;
} finally {
  server.close();
  unix.close();
  await SandboxManager.reset();
  const evidence = path.resolve('dist/native-mac');
  await mkdir(evidence, { recursive: true });
  await writeFile(
    path.join(evidence, 'sandbox-runtime.json'),
    `${JSON.stringify(output, null, 2)}\n`,
  );
  process.stdout.write(`${JSON.stringify(output, null, 2)}\n`);
}
