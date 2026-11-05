import { describe, expect, it } from 'vitest';
import { matchesCreationDraft } from './creationDraft';

describe('creation draft cleanup', () => {
  const original = {
    goal: 'Prepare the proposal',
    budget: { maxTasks: 4, maxActions: 8 },
    participantAgentIds: ['writer', 'reviewer'],
  };
  const snapshot = JSON.stringify(original);

  it('recognizes the submitted private draft after JSON storage reorders keys', () => {
    expect(
      matchesCreationDraft(
        {
          participantAgentIds: ['writer', 'reviewer'],
          budget: { maxActions: 8, maxTasks: 4 },
          goal: 'Prepare the proposal',
        },
        snapshot,
      ),
    ).toBe(true);
  });

  it('preserves a newer draft even when its goal has not changed', () => {
    expect(
      matchesCreationDraft(
        { ...original, budget: { maxTasks: 5, maxActions: 8 } },
        snapshot,
      ),
    ).toBe(false);
    expect(
      matchesCreationDraft(
        { ...original, participantAgentIds: ['reviewer', 'writer'] },
        snapshot,
      ),
    ).toBe(false);
  });

  it('preserves a different goal or an unidentifiable legacy draft', () => {
    expect(
      matchesCreationDraft(
        { ...original, goal: 'Prepare a different proposal' },
        snapshot,
      ),
    ).toBe(false);
    expect(matchesCreationDraft(original)).toBe(false);
    expect(matchesCreationDraft(original, 'invalid JSON')).toBe(false);
  });
});
