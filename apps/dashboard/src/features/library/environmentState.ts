import type { Environment } from '../workspace/api';

export interface EnvironmentSnapshot {
  currentRevision: number;
  baselineRevision: number;
  baselineKey: string;
  current: Environment | null;
  saved: boolean;
}

function key(environment: Environment | null): string {
  if (!environment?.kind) return 'none';
  return JSON.stringify([
    environment.kind,
    environment.nodeVersion ?? '',
    environment.packageManager ?? '',
    (environment.checks ?? []).map((check) => [
      check.name,
      check.script ?? '',
      check.arguments ?? [],
      check.directory ?? '',
    ]),
  ]);
}

export function sameEnvironment(
  left: Environment | null,
  right: Environment | null,
) {
  return key(left) === key(right);
}

export function initialEnvironment(
  environment: Environment | null,
  revision: number,
): EnvironmentSnapshot {
  return {
    currentRevision: revision,
    baselineRevision: revision,
    baselineKey: key(environment),
    current: environment,
    saved: false,
  };
}

export function observeEnvironment(
  snapshot: EnvironmentSnapshot,
  environment: Environment | null,
  revision: number,
): EnvironmentSnapshot {
  if (revision <= snapshot.currentRevision) return snapshot;
  const unchanged = key(environment) === snapshot.baselineKey;
  return {
    ...snapshot,
    currentRevision: revision,
    baselineRevision: unchanged ? revision : snapshot.baselineRevision,
    current: environment,
    saved: snapshot.saved && unchanged,
  };
}

export function confirmEnvironmentSave(
  snapshot: EnvironmentSnapshot,
  environment: Environment | null,
  revision: number,
): EnvironmentSnapshot {
  const observed = observeEnvironment(snapshot, environment, revision);
  const stillCurrent = sameEnvironment(observed.current, environment);
  return {
    ...observed,
    baselineKey: key(environment),
    baselineRevision: stillCurrent ? observed.currentRevision : revision,
    saved: stillCurrent,
  };
}

export function reloadEnvironment(
  snapshot: EnvironmentSnapshot,
): EnvironmentSnapshot {
  return {
    ...snapshot,
    baselineKey: key(snapshot.current),
    baselineRevision: snapshot.currentRevision,
    saved: false,
  };
}

export function environmentChanged(snapshot: EnvironmentSnapshot): boolean {
  return snapshot.baselineKey !== key(snapshot.current);
}
