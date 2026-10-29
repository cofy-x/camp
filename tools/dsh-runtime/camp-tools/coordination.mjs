import process from 'node:process';
import { defineTool } from '@deepseek-ai/dsh-tools';
import { BridgeClient } from './transport.mjs';

export const name = 'camp-coordination';
export const inject = ['tools'];

/** Model tools choose a decision, while Camp authorizes and persists its scope. */
export function apply(ctx) {
  const bridge = new BridgeClient({
    CAMP_TOOL_ENDPOINT: process.env.CAMP_COORDINATION_ENDPOINT,
    CAMP_TOOL_TOKEN: process.env.CAMP_COORDINATION_TOKEN,
  });
  let concluded = false;
  // Tool result context may have queued another step. Consume it without a new
  // model request after the successful durable decision ends this execution.
  ctx.on('agent/pre-step', (_input, next) =>
    concluded ? Promise.resolve({ kind: 'enter', messages: [] }) : next(),
  );
  const artifactPaths = {
    type: 'array',
    items: { type: 'string' },
    description: 'Relevant workspace files using relative paths. Maximum 128.',
  };
  const specs = [
    {
      name: 'camp_handoff',
      kind: 'handoff',
      description:
        'Finish this round and hand the task to one approved participant. Camp saves the draft, releases execution resources, and schedules that Agent. Ordinary @mentions do not hand off.',
      parameters: {
        targetAgentId: {
          type: 'string',
          required: true,
          description:
            'An Agent ID from the approved participants in the task.',
        },
        instruction: {
          type: 'string',
          required: true,
          description:
            'The next concrete step and findings that participant needs.',
        },
        artifactPaths,
      },
    },
    {
      name: 'camp_ask_person',
      kind: 'ask_person',
      description:
        'Pause this task and ask a person for missing information or a decision. Camp saves the draft and releases resources until someone answers.',
      parameters: {
        question: {
          type: 'string',
          required: true,
          description: 'The question that blocks further useful work.',
        },
        options: {
          type: 'array',
          items: { type: 'string' },
          description: 'Up to eight suggested answers, each at most 512 bytes.',
        },
        artifactPaths,
      },
    },
    {
      name: 'camp_finish',
      kind: 'finish',
      description:
        'Finish the collaboration goal. Camp checks and publishes the final draft only after execution cleanup succeeds. Describe actual results and remaining limitations; use handoff if another participant must work first.',
      parameters: {
        summary: {
          type: 'string',
          required: true,
          description:
            'The result for people in the Room, including actual validation and limitations.',
        },
        artifactPaths,
      },
    },
  ];
  for (const spec of specs) {
    ctx.effect(() =>
      ctx.tools.register(
        defineTool({
          name: spec.name,
          description: spec.description,
          parameters: spec.parameters,
          output: {
            schema: {
              type: 'object',
              additionalProperties: false,
              properties: { accepted: { type: 'boolean', required: true } },
            },
            render: () => [
              {
                type: 'text',
                text: 'Camp saved the coordination decision. This execution has ended.',
              },
            ],
          },
          async execute(args, exec) {
            const receipt = await bridge.json(
              '/intent',
              {
                ...args,
                artifactPaths: args.artifactPaths ?? [],
                callId: exec.callId,
                kind: spec.kind,
              },
              exec.signal,
            );
            if (receipt?.accepted !== true)
              throw new Error(
                'Camp did not acknowledge the coordination decision.',
              );
            concluded = true;
            exec.concludeTurn();
            return { accepted: true };
          },
        }),
      ),
    );
  }
}
