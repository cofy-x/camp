import type { Environment } from '../workspace/api';

export interface EnvironmentSnapshot {
  observedKey: string;
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

function observationKey(environment: Environment | null, revision: string) {
  return JSON.stringify([revision, key(environment)]);
}

export function initialEnvironment(
  environment: Environment | null,
  revision: string,
): EnvironmentSnapshot {
  return {
    observedKey: observationKey(environment, revision),
    baselineKey: key(environment),
    current: environment,
    saved: false,
  };
}

export function observeEnvironment(
  snapshot: EnvironmentSnapshot,
  environment: Environment | null,
  revision: string,
): EnvironmentSnapshot {
  const observedKey = observationKey(environment, revision);
  if (observedKey === snapshot.observedKey) return snapshot;
  return {
    ...snapshot,
    observedKey,
    current: environment,
    saved: snapshot.saved && key(environment) === snapshot.baselineKey,
  };
}

export function confirmEnvironmentSave(
  snapshot: EnvironmentSnapshot,
  environment: Environment | null,
): EnvironmentSnapshot {
  // Keep the last observed key until the parent's refresh acknowledges the write.
  return {
    ...snapshot,
    baselineKey: key(environment),
    current: environment,
    saved: true,
  };
}

export function reloadEnvironment(
  snapshot: EnvironmentSnapshot,
): EnvironmentSnapshot {
  return {
    ...snapshot,
    baselineKey: key(snapshot.current),
    saved: false,
  };
}

export function environmentChanged(snapshot: EnvironmentSnapshot): boolean {
  return snapshot.baselineKey !== key(snapshot.current);
}
