import { describe, expect, it } from 'vitest';
import {
  checkCommandSummary,
  configuredChecks,
  editableCheck,
} from './environmentCheckCommands';

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

  it('makes literal whitespace and empty arguments visible without changing execution input', () => {
    const source = {
      name: 'literal',
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
    };
    const draft = editableCheck(source);
    expect(checkCommandSummary(draft, 'command', 'node')).toBe(
      'node --test "test folder/a.test.mjs" "" "\\"quoted\\"" "$HOME; echo nope" "first\\nsecond" " leading and trailing "',
    );
    expect(configuredChecks([draft])).toEqual([source]);
    expect(checkCommandSummary(editableCheck(), 'command', 'node')).toBe('');
  });

  it('distinguishes Node defaults, named scripts and an incompatible script draft', () => {
    const defaultCheck = editableCheck({ name: 'unit' });
    expect(checkCommandSummary(defaultCheck, 'node', 'node')).toBe(
      'node --test',
    );
    expect(checkCommandSummary(defaultCheck, 'node', 'pnpm@12.11.2')).toBe(
      'node --test',
    );
    const script = editableCheck({ name: 'unit', script: 'test:unit' });
    expect(checkCommandSummary(script, 'node', 'pnpm@12.11.2')).toBe(
      'pnpm run test:unit',
    );
    expect(checkCommandSummary(script, 'node', 'node')).toBe(
      'Package script: test:unit',
    );
  });
});
