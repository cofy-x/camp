import { describe, expect, it } from 'vitest';
import { summarizeActivity } from './activity';
import type { RunEvent } from './api';

const event = (type: string, text: string): RunEvent => ({
  runId: 'run',
  seq: 1,
  type,
  text,
  createdAt: '',
});

describe('observed activity summaries', () => {
  it('uses actual tool arguments without claiming their outcome', () => {
    expect(
      summarizeActivity(
        event(
          'tool_call',
          JSON.stringify({
            name: 'bash',
            arguments: JSON.stringify({ command: 'node --test\n' }),
          }),
        ),
      ),
    ).toEqual({ seq: 1, label: 'Running a command', detail: 'node --test ' });
    // Tool results can be flattened text; they cannot establish success or
    // failure and remain available in the original diagnostic record.
    expect(
      summarizeActivity(event('tool_result', 'Command failed, exit 1')),
    ).toBeNull();
    expect(summarizeActivity(event('completion', 'completed'))?.label).toBe(
      'Finishing the response',
    );
  });
  it('bounds untrusted details and handles malformed or unknown events', () => {
    expect(
      summarizeActivity(
        event(
          'tool_call',
          JSON.stringify({
            name: 'x'.repeat(500),
            arguments: { path: 'y'.repeat(500) },
          }),
        ),
      )?.detail,
    ).toHaveLength(160);
    expect(summarizeActivity(event('tool_call', '{bad'))?.label).toBe(
      'Using a tool',
    );
    expect(
      summarizeActivity(
        event('tool_call', JSON.stringify({ name: 'bash', arguments: '{bad' })),
      )?.label,
    ).toBe('Running a command');
    expect(
      summarizeActivity(event('assistant', 'Private response')),
    ).toBeNull();
    expect(summarizeActivity(event('output', 'Raw logs'))).toBeNull();
    expect(summarizeActivity(event('tool_result', 'File read'))).toBeNull();
  });
});
