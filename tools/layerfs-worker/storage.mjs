import { Buffer } from 'node:buffer';
import process from 'node:process';
import { inspectStorage } from './retention.mjs';
import {
  observeBudget,
  sampleStorage,
  setStoragePolicy,
  storagePolicy,
} from './storage-budget.mjs';
import {
  applyCleanupPlan,
  cancelCleanup,
  cleanupStatus,
  createCleanupPlan,
} from './cleanup.mjs';

try {
  const parts = [];
  let bytes = 0;
  for await (const part of process.stdin) {
    bytes += part.length;
    if (bytes > 64 << 10)
      throw new Error('Storage inspection input exceeds its bound.');
    parts.push(part);
  }
  const input = JSON.parse(Buffer.concat(parts).toString('utf8'));
  let report;
  switch (input.command ?? 'inspect') {
    case 'sample':
      report = await sampleStorage(input);
      break;
    case 'budget':
      report = await setStoragePolicy(input);
      break;
    case 'budget-show':
      report = {
        schema: 1,
        kind: 'checkpoint-storage-policy',
        ...(await storagePolicy(input)),
      };
      break;
    case 'plan':
      report = await createCleanupPlan(input);
      break;
    case 'apply':
      report = await applyCleanupPlan(input);
      break;
    case 'cancel':
      report = await cancelCleanup(input);
      break;
    case 'cleanup-status':
      report = await cleanupStatus(input);
      break;
    case 'inspect': {
      report = await inspectStorage(input);
      const policy = await storagePolicy(input);
      report.budget = {
        policy,
        observation: await observeBudget({
          dataRoot: input.dataRoot,
          logicalBytes: report.totals.logicalBytes,
          complete: report.complete,
          policy,
        }),
      };
      report.quiescent = input.quiescent !== false;
      if (!report.quiescent) {
        report.retention.previewAllowed = false;
        for (const row of report.captures)
          if (row.disposition === 'unreferenced') {
            row.disposition = 'blocked';
            row.reason =
              'Live usage is an observation, not a stopped cleanup preview.';
          }
        report.retention.blocked += report.retention.unreferenced;
        report.retention.unreferenced = 0;
        report.retention.unreferencedLogicalBytes = 0;
      }
      break;
    }
    default:
      throw Object.assign(new Error('Unknown storage operation.'), {
        code: 'InvalidInput',
      });
  }
  process.stdout.write(`${JSON.stringify(report)}\n`);
} catch (error) {
  const known = new Set([
    'InvalidInput',
    'InvalidIdentity',
    'Unauthorized',
    'UnsafeMetadata',
    'JournalUnavailable',
    'PendingOperations',
    'RecoveryRequired',
    'NoCandidates',
    'StalePlan',
    'Conflict',
    'ChangedCapture',
    'OperationPending',
    'ConfirmationRequired',
    'MissingBatch',
    'UnknownOutcome',
    'MissingCapture',
    'Capacity',
    'UnsafeCapture',
    'ChangedDuringInspection',
  ]);
  process.stdout.write(
    `${JSON.stringify({ error: { code: error.code ?? 'Unknown', message: known.has(error.code) ? error.message : 'Storage administration did not complete. Inspect the retained plan/operation and resume or cancel that exact ID before starting another operation.' } })}\n`,
  );
  process.exitCode = 1;
}
