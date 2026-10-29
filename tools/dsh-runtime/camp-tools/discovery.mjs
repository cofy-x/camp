import { Buffer } from 'node:buffer';
import { defineTool } from '@deepseek-ai/dsh-tools';

export const name = 'camp-file-discovery';
export const inject = ['fs', 'tools'];

const MAX_ENTRIES = 2000;
const MAX_MATCHES = 100;
const MAX_FILE_BYTES = 256 * 1024;
const MAX_SEARCH_BYTES = 8 * 1024 * 1024;
const MAX_OUTPUT_BYTES = 32 * 1024;

function objectArgs(args) {
  if (!args || typeof args !== 'object' || Array.isArray(args))
    throw new Error('Expected an argument object.');
  if (
    args.path !== undefined &&
    (typeof args.path !== 'string' || args.path.length === 0)
  )
    throw new Error('path must be non-empty text.');
  return args;
}

async function* walk(fs, path, recursive, signal, state) {
  const pending = [await fs.resolve(path, { signal })];
  const seen = new Set();
  while (pending.length) {
    signal?.throwIfAborted();
    const directory = pending.shift();
    if (seen.has(directory.targetKey)) continue;
    seen.add(directory.targetKey);
    const entries = await fs.listDir(directory, signal);
    for (const entry of entries) {
      if (++state.visited > MAX_ENTRIES) {
        state.truncated = true;
        return;
      }
      yield entry;
      if (recursive && entry.type === 'directory') pending.push(entry.target);
    }
  }
}

export async function listFiles(fs, raw, signal) {
  const args = objectArgs(raw);
  if (args.recursive !== undefined && typeof args.recursive !== 'boolean')
    throw new Error('recursive must be a boolean.');
  const state = { visited: 0, truncated: false };
  const lines = [];
  let outputBytes = 0;
  for await (const entry of walk(
    fs,
    args.path ?? '/workspace',
    args.recursive ?? false,
    signal,
    state,
  )) {
    const line = `${entry.target.displayPath}${entry.type === 'directory' ? '/' : ''}`;
    outputBytes += Buffer.byteLength(line) + 1;
    if (outputBytes > MAX_OUTPUT_BYTES) {
      state.truncated = true;
      break;
    }
    lines.push(line);
  }
  return {
    text: lines.join('\n') || 'No entries.',
    truncated: state.truncated,
  };
}

export async function searchFiles(fs, raw, signal) {
  const args = objectArgs(raw);
  if (typeof args.query !== 'string' || !args.query || args.query.length > 1000)
    throw new Error('query must contain 1–1000 characters.');
  if (
    args.caseSensitive !== undefined &&
    typeof args.caseSensitive !== 'boolean'
  )
    throw new Error('caseSensitive must be a boolean.');
  const sensitive = args.caseSensitive ?? false;
  const query = sensitive ? args.query : args.query.toLowerCase();
  const state = { visited: 0, truncated: false };
  const lines = [];
  let bytes = 0;
  let outputBytes = 0;
  let skipped = 0;
  for await (const entry of walk(
    fs,
    args.path ?? '/workspace',
    true,
    signal,
    state,
  )) {
    if (entry.type !== 'file') continue;
    if (entry.size === undefined || entry.size > MAX_FILE_BYTES) {
      skipped++;
      continue;
    }
    bytes += entry.size;
    if (bytes > MAX_SEARCH_BYTES) {
      state.truncated = true;
      break;
    }
    let text;
    try {
      text = await fs.readText(entry.target, signal);
    } catch (error) {
      if (['FS_NOT_TEXT', 'FS_TOO_LARGE'].includes(error.code)) {
        skipped++;
        continue;
      }
      throw error;
    }
    const content = text.split('\n');
    for (let i = 0; i < content.length; i++) {
      if (!(sensitive ? content[i] : content[i].toLowerCase()).includes(query))
        continue;
      const line = `${entry.target.displayPath}:${i + 1}: ${content[i].slice(0, 500)}`;
      outputBytes += Buffer.byteLength(line) + 1;
      if (lines.length === MAX_MATCHES || outputBytes > MAX_OUTPUT_BYTES) {
        state.truncated = true;
        break;
      }
      lines.push(line);
    }
    if (state.truncated) break;
  }
  const suffix = skipped
    ? `\n${skipped} binary, unknown-size or large files skipped.`
    : '';
  return {
    text: (lines.join('\n') || 'No matching lines.') + suffix,
    truncated: state.truncated,
  };
}

export function apply(ctx) {
  const output = {
    schema: {
      type: 'object',
      additionalProperties: false,
      properties: {
        text: { type: 'string', required: true },
        truncated: { type: 'boolean', required: true },
      },
    },
    render: (_args, value) => [
      {
        type: 'text',
        text:
          value.text +
          (value.truncated
            ? '\nResults limited. Narrow the search path or query.'
            : ''),
      },
    ],
  };
  ctx.tools.register(
    defineTool({
      name: 'list_files',
      description:
        'List files in the Camp workspace or attached references. No host filesystem access.',
      parameters: {
        path: { type: 'string', description: 'Directory, default /workspace.' },
        recursive: {
          type: 'boolean',
          description: 'Include nested directories, default false.',
        },
      },
      output,
      execute: (args, exec) => listFiles(ctx.fs, args, exec.signal),
    }),
  );
  ctx.tools.register(
    defineTool({
      name: 'search_files',
      description:
        'Find literal text in Camp files. Searches bounded UTF-8 text files without running a shell.',
      parameters: {
        path: { type: 'string', description: 'Directory, default /workspace.' },
        query: {
          type: 'string',
          required: true,
          description: 'Literal text to find, not a regular expression.',
        },
        caseSensitive: {
          type: 'boolean',
          description: 'Match case exactly, default false.',
        },
      },
      output,
      execute: (args, exec) => searchFiles(ctx.fs, args, exec.signal),
    }),
  );
}
