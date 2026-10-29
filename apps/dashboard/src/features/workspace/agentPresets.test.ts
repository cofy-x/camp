import { describe, expect, it } from 'vitest';
import { suggestAgentHandle } from './agentPresets';

describe('suggested agent handles', () => {
  it('uses the full name and normalizes readable names to valid handles', () => {
    expect(suggestAgentHandle('W', [])).toBe('w');
    expect(suggestAgentHandle('Writer', [])).toBe('writer');
    expect(suggestAgentHandle('  Café reviewer!  ', [])).toBe('cafe-reviewer');
    expect(suggestAgentHandle('42 reports', [])).toBe('agent-42-reports');
    expect(suggestAgentHandle('作家', [])).toBe('agent');
    expect(suggestAgentHandle(' ', [])).toBe('');
  });

  it('avoids existing identities and keeps long suffixed handles within the server limit', () => {
    expect(suggestAgentHandle('Writer', ['Writer', 'writer-2'])).toBe(
      'writer-3',
    );
    const name = 'Long document reviewer '.repeat(4);
    const first = suggestAgentHandle(name, []);
    const second = suggestAgentHandle(name, [first]);
    expect(first).toMatch(/^[a-z][a-z0-9_-]{0,47}$/);
    expect(second).toMatch(/^[a-z][a-z0-9_-]{0,47}$/);
    expect(second).not.toBe(first);
    expect(second).toHaveLength(48);
    expect(second).toMatch(/-2$/);
  });
});
