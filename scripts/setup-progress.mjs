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
