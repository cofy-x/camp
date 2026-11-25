import type { DesktopAPI } from '@cofy-x/camp-platinum';

export async function reportError(desktop: DesktopAPI, error: unknown) {
  await desktop.dialog({
    title: 'Unable to complete the operation',
    message: error instanceof Error ? error.message : String(error),
    buttons: [{ label: 'OK', value: 'ok', default: true }],
  });
}

export async function runCommand(
  desktop: DesktopAPI,
  work: () => Promise<void>,
) {
  try {
    await work();
  } catch (error) {
    await reportError(desktop, error);
  }
}
