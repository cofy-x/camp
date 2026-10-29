import { createHash } from 'node:crypto';
import { lstat, mkdir, realpath } from 'node:fs/promises';
import path from 'node:path';

export class BridgeError extends Error {
  constructor(code, message, details) {
    super(message);
    this.code = code;
    this.details = details;
  }
}

export function string(value, name, max = 4096) {
  if (
    typeof value !== 'string' ||
    value.length === 0 ||
    value.length > max ||
    value.includes('\0')
  )
    throw new BridgeError(
      'InvalidInput',
      `${name} must be a nonempty string of at most ${max} characters.`,
    );
  return value;
}

export function identity(prefix, values) {
  return `${prefix}-${createHash('sha256').update(JSON.stringify(values)).digest('hex').slice(0, 40)}`;
}

export function inside(root, target) {
  const relative = path.relative(root, target);
  return (
    relative === '' ||
    (!relative.startsWith(`..${path.sep}`) &&
      relative !== '..' &&
      !path.isAbsolute(relative))
  );
}

export async function ownedDirectory(root, input, create = false) {
  string(input, 'directory');
  if (!path.isAbsolute(input) || !inside(root, input))
    throw new BridgeError(
      'InvalidInput',
      'The directory must be inside the configured Camp data root.',
    );
  if (create) {
    let ancestor = input;
    for (;;) {
      try {
        if (!inside(root, await realpath(ancestor)))
          throw new BridgeError(
            'InvalidInput',
            'The directory has an ancestor outside the Camp data root.',
          );
        break;
      } catch (error) {
        if (error.code !== 'ENOENT') throw error;
        ancestor = path.dirname(ancestor);
      }
    }
    await mkdir(input, { recursive: true, mode: 0o700 });
  }
  const resolved = await realpath(input);
  if (!inside(root, resolved) || !(await lstat(resolved)).isDirectory())
    throw new BridgeError(
      'InvalidInput',
      'The directory must resolve inside the configured Camp data root.',
    );
  return resolved;
}

export async function ownedOutput(root, input) {
  string(input, 'targetArchive');
  if (!path.isAbsolute(input) || !inside(root, input))
    throw new BridgeError(
      'InvalidInput',
      'The output must be inside the configured Camp data root.',
    );
  const directory = await ownedDirectory(root, path.dirname(input), true);
  return path.join(directory, path.basename(input));
}

export function shellQuote(value) {
  return `'${String(value).replaceAll("'", "'\\''")}'`;
}

export function errorRecord(error) {
  return {
    code: typeof error?.code === 'string' ? error.code : 'Failed',
    message:
      error instanceof Error ? error.message : 'LayerFS operation failed.',
    ...(error?.details !== undefined || error?.detail !== undefined
      ? { details: { ...error.details, native: error.detail ?? null } }
      : {}),
  };
}
