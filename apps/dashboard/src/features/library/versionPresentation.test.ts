import { describe, expect, it } from 'vitest';
import {
  versionChecksLabel,
  visibleVersionChecks,
} from './versionPresentation';

describe('shared version checks', () => {
  it.each([
    ['not_run', 'Checks not run'],
    ['not_configured', 'Checks not configured'],
  ])(
    'keeps %s available in details without repeating it in every row',
    (status, label) => {
      expect(visibleVersionChecks(status)).toBeNull();
      expect(versionChecksLabel(status)).toBe(label);
    },
  );

  it.each([
    ['passed', 'Checks passed'],
    ['failed', 'Checks: failed'],
    ['interrupted', 'Checks: interrupted'],
    ['future-status', 'Checks: future-status'],
    ['', 'Checks not reported'],
  ])(
    'keeps reported %s visible without treating it as not run',
    (status, label) => {
      expect(visibleVersionChecks(status)).toBe(label);
      expect(versionChecksLabel(status)).toBe(label);
    },
  );
});
