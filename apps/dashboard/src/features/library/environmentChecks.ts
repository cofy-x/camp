import type { EnvironmentCheck } from '../workspace/api';

export interface EditableCheck {
  id: string;
  name: string;
  script: string;
  directory: string;
  arguments: string[];
}

export function editableCheck(check?: EnvironmentCheck): EditableCheck {
  return {
    id: crypto.randomUUID(),
    name: check?.name ?? '',
    script: check?.script ?? '',
    directory: check?.directory ?? '',
    arguments: [...(check?.arguments ?? [])],
  };
}

export function configuredChecks(checks: EditableCheck[]): EnvironmentCheck[] {
  return checks.map((check) => ({
    name: check.name,
    ...(check.script ? { script: check.script } : {}),
    ...(check.directory ? { directory: check.directory } : {}),
    ...(check.arguments.length ? { arguments: [...check.arguments] } : {}),
  }));
}
