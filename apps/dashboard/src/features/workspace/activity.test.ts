import { describe, expect, it } from 'vitest';
import { summarizeActivity } from './activity';
import type { RunActivityItem } from './api';

const event = (type: string, name = '', detail = ''): RunActivityItem => ({
  seq: 1,
  type,
  name,
  detail,
  createdAt: '',
});

describe('observed activity summaries', () => {
  it('describes a reported action without claiming its outcome', () => {
    expect(
      summarizeActivity(event('tool_call', 'bash', 'node --test')),
    ).toEqual({
      seq: 1,
      label: 'Running a command',
      detail: 'node --test',
    });
    expect(summarizeActivity(event('completion')).label).toBe(
      'Finishing the response',
    );
    expect(
      summarizeActivity(event('error', '', 'The process stopped.')).detail,
    ).toBe('The process stopped.');
  });
  it('retains unknown tool attribution and handles an unknown state without inventing success', () => {
    expect(summarizeActivity(event('tool_call', 'custom_tool')).label).toBe(
      'Using custom_tool',
    );
    expect(summarizeActivity(event('tool_call')).label).toBe('Using a tool');
    expect(summarizeActivity(event('status', 'checking')).label).toBe(
      'Checking',
    );
    expect(summarizeActivity(event('status', 'unrecognized')).label).toBe(
      'Status updated',
    );
  });
});
