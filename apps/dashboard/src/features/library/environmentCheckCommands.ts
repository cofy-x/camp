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

function displayArgument(argument: string) {
  return /^[A-Za-z0-9_./:@%+=,-]+$/.test(argument)
    ? argument
    : JSON.stringify(argument);
}

/** A read-only summary; execution always uses the separate original arguments. */
export function checkCommandSummary(
  check: EditableCheck,
  kind: string,
  manager: string,
): string {
  if (kind === 'command')
    return check.arguments[0]
      ? check.arguments.map(displayArgument).join(' ')
      : '';
  if (check.script)
    return manager === 'pnpm@12.11.2'
      ? `pnpm run ${displayArgument(check.script)}`
      : `Package script: ${displayArgument(check.script)}`;
  return 'node --test';
}
