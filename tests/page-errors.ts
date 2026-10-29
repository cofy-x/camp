import type { Page } from '@playwright/test';

export function isNavigationCancellation(
  error: Error,
  browserName: string,
  departingOrigin: string | null,
) {
  if (browserName !== 'webkit' || !departingOrigin) return false;
  const match = error.stack
    ?.split('\n')[0]
    .match(
      /^Fetch API cannot load (https?:\/\/\S+) due to access control checks\.$/,
    );
  if (!match) return false;
  let url: URL;
  try {
    url = new URL(match[1]);
  } catch {
    return false;
  }
  return (
    error.name === `Fetch API cannot load ${url.protocol.slice(0, -1)}` &&
    url.origin === departingOrigin &&
    url.pathname.startsWith('/api/')
  );
}

export function watchPageErrors(
  page: Page,
  browserName: string,
  errors: string[] = [],
) {
  const navigationCancellations: string[] = [];
  let departingOrigin: string | null = null;
  // WebKit emits native loader cancellation diagnostics as pageerror events:
  // https://github.com/microsoft/playwright/issues/43120
  // Recognize only same-origin API diagnostics in the departing document.
  // The new document and all ordinary exceptions remain subject to the guard.
  page.on('framenavigated', (frame) => {
    if (frame === page.mainFrame()) departingOrigin = null;
  });
  page.on('pageerror', (error) => {
    const target = isNavigationCancellation(error, browserName, departingOrigin)
      ? navigationCancellations
      : errors;
    target.push(error.stack ?? error.message);
  });
  return {
    errors,
    navigationCancellations,
    async reload() {
      departingOrigin = new URL(page.url()).origin;
      try {
        await page.reload();
      } finally {
        departingOrigin = null;
      }
    },
  };
}
