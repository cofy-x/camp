import process from 'node:process';
import { defineTool } from '@deepseek-ai/dsh-tools';
import { BridgeClient, boundedText } from './transport.mjs';

export const name = 'camp-task-inputs';
export const inject = ['tools'];

export async function readTaskInputs(bridge, args, signal) {
  if (
    !args ||
    typeof args !== 'object' ||
    Array.isArray(args) ||
    Object.keys(args).some((key) => !['after', 'limit'].includes(key)) ||
    !Number.isSafeInteger(args.after) ||
    args.after < 0 ||
    !Number.isInteger(args.limit ?? 20) ||
    (args.limit ?? 20) < 1 ||
    (args.limit ?? 20) > 20
  )
    throw new Error(
      'Choose a nonnegative input cursor and a limit from 1 to 20.',
    );
  const response = await bridge.request(
    '/task-inputs',
    { after: args.after, limit: args.limit ?? 20 },
    signal,
  );
  const body = JSON.parse(await boundedText(response, 64 * 1024 + 32, signal));
  if (!response.ok || body.error)
    throw new Error('Task history is unavailable for this execution or range.');
  const page = body.value;
  if (
    !page ||
    !Array.isArray(page.items) ||
    page.items.length > (args.limit ?? 20) ||
    !Number.isSafeInteger(page.through) ||
    page.through < args.after ||
    typeof page.hasMore !== 'boolean'
  )
    throw new Error('Camp returned an invalid input page.');
  return page;
}

export function apply(ctx) {
  const bridge = new BridgeClient({
    CAMP_TOOL_ENDPOINT: process.env.CAMP_COORDINATION_ENDPOINT,
    CAMP_TOOL_TOKEN: process.env.CAMP_COORDINATION_TOKEN,
  });
  ctx.effect(() =>
    ctx.tools.register(
      defineTool({
        name: 'camp_read_task_inputs',
        description:
          'Read whole original collaboration inputs through this round’s frozen frontier. Use after=0 for the oldest originals, then nextAfter while hasMore. No later inputs, other tasks or extra execution allowance are available. Reading does not acknowledge inputs.',
        parameters: {
          after: {
            type: 'integer',
            required: true,
            description: 'Exclusive task input sequence, starting at 0.',
          },
          limit: {
            type: 'integer',
            description:
              'Maximum whole originals, 1–20, default 20; pages also have a 64 KiB JSON budget.',
          },
        },
        output: {
          schema: { type: 'object', additionalProperties: true },
          render: (_args, value) => [
            { type: 'text', text: JSON.stringify(value) },
          ],
        },
        execute: (args, exec) => readTaskInputs(bridge, args, exec.signal),
      }),
    ),
  );
}
