import { describe, expect, it } from 'vitest';
import { goalTitle } from './goal-title';

describe('goalTitle', () => {
  it('keeps a short goal and uses the first meaningful line or sentence for longer instructions', () => {
    expect(goalTitle('Prepare a handover')).toBe('Prepare a handover');
    expect(goalTitle('\n# Prepare a handover\n\nUse the room files.')).toBe(
      'Prepare a handover',
    );
    expect(goalTitle('Update notes.md. Use the archived draft.')).toBe(
      'Update notes.md.',
    );
    expect(goalTitle('整理交接清单。请阅读房间里的原始说明。')).toBe(
      '整理交接清单。',
    );
  });

  it('bounds a long title without splitting words or surrogate pairs', () => {
    const words = 'Prepare the handover '.repeat(16);
    expect(goalTitle(words)).toBe(words.slice(0, 95).trimEnd() + '…');
    expect(goalTitle('📄'.repeat(110))).toBe('📄'.repeat(95) + '…');
  });
});
