import assert from 'node:assert/strict';
import { lstat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';

// Keep public acceptance evidence to known phase names, outcomes and durations.
// Setup's full output can contain private registration and host diagnostics.
const phases = new Set([
  'Checking Runner host prerequisites',
  'Checking installed runtime cache',
  'Checking Camp Runner installation',
  'Copying included offline runtime',
  'Runtime download',
  'Verifying runtime archive',
  'Validating and extracting runtime archive',
  'Validating fixed runtime files',
  'Committing verified runtime',
  'Preparing fixed Linux execution image',
  'Preparing optional checkpoint worker image',
  'Runner self-test',
]);

export function offlineSetupOnly(mode, value) {
  if (value === undefined || value === '') return false;
  if (value !== '1' || mode !== 'offline')
    throw new Error('CAMP_SMOKE_SETUP_ONLY=1 requires explicit offline mode.');
  return true;
}

export const offlineSetupChecks = [
  'archive-checksum',
  'fixed-runtime-installation',
  'relocation',
  'repeat-setup-preserves-identity',
  'native-self-test-without-model',
];

export function completeOfflineSetupReport(
  report,
  { cleanupConfirmed, modelRequests },
) {
  const complete =
    report.setupVerified === true &&
    cleanupConfirmed === true &&
    modelRequests === 0 &&
    offlineSetupChecks.every((check) => report.checks?.includes(check));
  return {
    ...report,
    status: complete ? 'passed' : 'failed',
    cleanupConfirmed: cleanupConfirmed === true,
    model: { paid: false, requests: modelRequests },
    completedAt: new Date().toISOString(),
  };
}

export function setupPhases(output) {
  const result = [];
  for (const line of output.split('\n')) {
    const match = line.match(
      /^([^:]+): (complete|failed|cancelled);(?: [^;\r\n]+;)* elapsed ([\d.hms]+)$/,
    );
    if (!match || !phases.has(match[1])) continue;
    const duration = match[3].match(
      /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+(?:\.\d+)?)s)?(?:(\d+)ms)?$/,
    );
    if (!duration) continue;
    const [, hours, minutes, seconds, milliseconds] = duration;
    const elapsedMs =
      Number(hours ?? 0) * 3600000 +
      Number(minutes ?? 0) * 60000 +
      Number(seconds ?? 0) * 1000 +
      Number(milliseconds ?? 0);
    if (!Number.isFinite(elapsedMs)) continue;
    result.push({ phase: match[1], status: match[2], elapsedMs });
  }
  return result;
}

// Raw setup output stays in the current private campaign. Each explicit attempt
// gets new files, so a later reconstruction/setup cannot replace its failure.
export async function retainSetupAttempt(directory, stage, result) {
  assert.ok(['baseline', 'initial', 'post-reconstruction'].includes(stage));
  const owner = await lstat(directory);
  assert.ok(
    owner.isDirectory() &&
      owner.uid === process.getuid() &&
      !(owner.mode & 0o077),
  );
  assert.ok(Number.isFinite(result.elapsedMs) && result.elapsedMs >= 0);
  assert.ok(result.exit === null || Number.isInteger(result.exit));
  const privateLog = `${stage}-setup.log`;
  const evidence = {
    stage,
    exit: result.exit,
    elapsedMs: result.elapsedMs,
    phases: setupPhases(result.output),
    privateLog,
  };
  await writeFile(path.join(directory, privateLog), result.output, {
    flag: 'wx',
    mode: 0o600,
  });
  await writeFile(
    path.join(directory, `${stage}-setup.json`),
    `${JSON.stringify(evidence, null, 2)}\n`,
    { flag: 'wx', mode: 0o600 },
  );
  return evidence;
}
