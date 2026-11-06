import { describe, expect, it } from 'vitest';
import { configuredChecks, editableCheck } from './environmentChecks';

describe('structured environment checks', () => {
  it('round-trips literal command arguments without shell parsing or trimming', () => {
    const checks = [
      {
        name: 'integration',
        arguments: [
          'node',
          '--test',
          'test folder/a.test.mjs',
          '',
          '"quoted"',
          '$HOME; echo nope',
          'first\nsecond',
          ' leading and trailing ',
        ],
        directory: 'packages/application',
      },
      { name: 'lint', arguments: ['/tool path/check', '-c', 'a\\b'] },
    ];
    const editable = checks.map(editableCheck);
    expect(configuredChecks(editable)).toEqual(checks);
    editable[0].arguments[0] = 'changed';
    expect(checks[0].arguments[0]).toBe('node');
  });

  it('keeps script and default Node checks distinct and excludes editor identity', () => {
    const checks = [
      { name: 'unit', script: 'test:unit', directory: 'web' },
      { name: 'default-node' },
    ];
    expect(configuredChecks(checks.map(editableCheck))).toEqual(checks);
    expect(editableCheck().id).not.toBe(editableCheck().id);
  });

  it('keeps an empty argument and independent copies after reading saved settings', () => {
    const original = { name: 'literal', arguments: ['node', ''] };
    const draft = editableCheck(original);
    const saved = configuredChecks([draft]);
    expect(saved[0].arguments).toEqual(['node', '']);
    saved[0].arguments!.push('later');
    expect(draft.arguments).toEqual(['node', '']);
  });
});
