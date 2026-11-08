import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { TaskAllowanceNotice } from './TaskAllowanceNotice';
import type { Task } from './api';

const paused = {
  status: 'needs_attention' as Task['status'],
  activeRunId: '',
  modelRequestsUsed: 40,
  maxModelRequests: 40,
};

function render(task = paused, canContinue = true) {
  return renderToStaticMarkup(
    createElement(TaskAllowanceNotice, { task, canContinue }),
  );
}

describe('collaboration request allowance notice', () => {
  it('shows the exhausted counters and explicit continuation step', () => {
    expect(render()).toContain(
      'Model request allowance exhausted (40/40). Add model requests in Continue to resume.',
    );
    expect(render()).toContain('role="status"');
    expect(render({ ...paused, status: 'waiting_for_person' })).toContain(
      'allowance exhausted',
    );
  });

  it('does not claim that the last allowed request cannot finish work', () => {
    expect(render({ ...paused, activeRunId: 'last-allowed-run' })).toBe('');
    for (const status of [
      'queued',
      'running',
      'publishing',
      'completed',
      'cancelling',
      'cancelled',
      'interrupted',
    ] as const)
      expect(render({ ...paused, status })).toBe('');
  });

  it('stays quiet when requests remain or no positive ceiling is recorded', () => {
    expect(render({ ...paused, modelRequestsUsed: 39 })).toBe('');
    expect(render({ ...paused, maxModelRequests: 0 })).toBe('');
  });

  it('identifies the grant owner without offering an unauthorized operation', () => {
    expect(render(paused, false)).toContain(
      'The owner or an administrator can add model requests.',
    );
    expect(render(paused, false)).not.toContain('<button');
  });
});
