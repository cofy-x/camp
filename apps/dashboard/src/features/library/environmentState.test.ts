import { describe, expect, it } from 'vitest';
import type { Environment } from '../workspace/api';
import {
  confirmEnvironmentSave,
  environmentChanged,
  initialEnvironment,
  observeEnvironment,
  reloadEnvironment,
} from './environmentState';

const node: Environment = {
  kind: 'node',
  nodeVersion: '24.16.0',
  packageManager: 'node',
  checks: [{ name: 'Tests', script: 'test' }],
};
const commands: Environment = {
  kind: 'command',
  checks: [{ name: 'Tests', arguments: ['python3', '-m', 'unittest'] }],
};

describe('environment form baseline', () => {
  it('accepts the empty-object wire format for an unconfigured resource', () => {
    const empty = initialEnvironment({}, '1');
    expect(empty.current).toEqual({});
    expect(environmentChanged(empty)).toBe(false);
    expect(observeEnvironment(empty, null, '1')).toBe(empty);

    const saved = confirmEnvironmentSave(initialEnvironment(node, '1'), {});
    expect(saved.current).toEqual({});
    expect(saved.saved).toBe(true);
    expect(environmentChanged(saved)).toBe(false);
    const acknowledged = observeEnvironment(saved, {}, '2');
    expect(acknowledged.saved).toBe(true);
    expect(environmentChanged(acknowledged)).toBe(false);
  });

  it('accepts omitted and null checks without inventing a check configuration', () => {
    const omitted: Environment = { kind: 'command' };
    const empty = initialEnvironment(omitted, '1');
    expect(
      observeEnvironment(empty, { kind: 'command', checks: null }, '1'),
    ).toBe(empty);
    expect(
      observeEnvironment(empty, { kind: 'command', checks: [] }, '1'),
    ).toBe(empty);
    expect(environmentChanged(empty)).toBe(false);
  });

  it('requires an explicit reload when server settings change', () => {
    const initial = initialEnvironment(node, '1');
    const refreshed = observeEnvironment(initial, commands, '2');
    expect(refreshed.baselineKey).toBe(initial.baselineKey);
    expect(refreshed.current).toEqual(commands);
    expect(environmentChanged(refreshed)).toBe(true);
    expect(refreshed.saved).toBe(false);

    const reloaded = reloadEnvironment(refreshed);
    expect(reloaded.current).toEqual(commands);
    expect(environmentChanged(reloaded)).toBe(false);
    expect(reloaded.saved).toBe(false);
  });

  it('keeps a confirmed save truthful while its parent refresh is pending', () => {
    const saved = confirmEnvironmentSave(initialEnvironment(null, '1'), node);
    expect(saved.current).toEqual(node);
    expect(saved.saved).toBe(true);
    expect(environmentChanged(saved)).toBe(false);
    expect(observeEnvironment(saved, null, '1')).toBe(saved);

    const acknowledged = observeEnvironment(saved, node, '2');
    expect(acknowledged.saved).toBe(true);
    expect(environmentChanged(acknowledged)).toBe(false);

    const revertedElsewhere = observeEnvironment(acknowledged, null, '3');
    expect(revertedElsewhere.current).toBeNull();
    expect(revertedElsewhere.saved).toBe(false);
    expect(environmentChanged(revertedElsewhere)).toBe(true);
  });

  it('detects an external revert even before its own save is observed in props', () => {
    const saved = confirmEnvironmentSave(initialEnvironment(null, '1'), node);
    const revertedElsewhere = observeEnvironment(saved, null, '3');
    expect(revertedElsewhere.current).toBeNull();
    expect(revertedElsewhere.saved).toBe(false);
    expect(environmentChanged(revertedElsewhere)).toBe(true);
  });

  it('clears an old save receipt when another session changes the configuration', () => {
    const saved = confirmEnvironmentSave(initialEnvironment(node, '1'), node);
    const updated = observeEnvironment(saved, commands, '2');
    expect(updated.saved).toBe(false);
    expect(environmentChanged(updated)).toBe(true);
    expect(updated.baselineKey).toBe(saved.baselineKey);
  });

  it('ignores JSON property ordering but detects changed check arguments', () => {
    const initial = initialEnvironment(commands, '1');
    expect(
      observeEnvironment(
        initial,
        {
          checks: [{ arguments: ['python3', '-m', 'unittest'], name: 'Tests' }],
          kind: 'command',
        },
        '1',
      ),
    ).toBe(initial);
    const changed = observeEnvironment(
      initial,
      {
        ...commands,
        checks: [{ name: 'Tests', arguments: ['python3', '-m', 'pytest'] }],
      },
      '2',
    );
    expect(environmentChanged(changed)).toBe(true);
  });
});
