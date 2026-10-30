import { expect, test } from '@playwright/test';
import { watchPageErrors } from './page-errors';

const flows = [
  { mode: 'setup', heading: 'Welcome to Camp', action: 'Create Team' },
  { mode: 'login', heading: 'Welcome back', action: 'Sign In' },
  { mode: 'accept-invite', heading: 'Join your team', action: 'Join Team' },
] as const;

for (const { mode, heading, action } of flows) {
  test(`username constraints prevent malformed ${mode} requests`, async ({
    page,
    browserName,
    isMobile,
  }) => {
    const errors = watchPageErrors(page, browserName);
    const patternErrors: string[] = [];
    page.on('console', (message) => {
      if (/pattern|regular expression|regexp/i.test(message.text()))
        patternErrors.push(message.text());
    });
    await page.route('**/api/auth/status', (route) =>
      route.fulfill({
        json: {
          setupRequired: mode === 'setup',
          user: null,
          team: { id: 'default', name: 'Camp', updatedAt: '' },
        },
      }),
    );
    const requests: { path: string; body: unknown }[] = [];
    await page.route(/\/api\/auth\/(setup|login|accept-invite)$/, (route) => {
      requests.push({
        path: new URL(route.request().url()).pathname,
        body: route.request().postDataJSON(),
      });
      return route.fulfill({
        status: 400,
        json: {
          code: 'validation_test',
          error: 'Authentication request recorded for validation test.',
        },
      });
    });
    await page.goto(
      mode === 'accept-invite' ? '/#invite=browser-test-invite' : '/',
    );
    await expect(
      page.getByRole('heading', { name: heading, exact: true }),
    ).toBeVisible();
    if (mode === 'setup') {
      await page.getByLabel('Team name', { exact: true }).fill('Browser Team');
      await page.getByLabel(/^Setup token/).fill('browser-test-setup-token');
    }
    if (mode !== 'login')
      await page
        .getByLabel('Your name', { exact: true })
        .fill('Browser Member');
    const password = 'Browser validation test password';
    await page.getByLabel(/^Password/).fill(password);

    const username = page.getByLabel('Username', { exact: true });
    const submit = page.getByRole('button', { name: action, exact: true });
    await expect(username).toHaveAttribute('minlength', '3');
    await expect(username).toHaveAttribute('maxlength', '32');
    for (const value of [
      'abc',
      'valid-user',
      'valid_user',
      'a1_-',
      'a'.repeat(32),
    ]) {
      await username.fill(value);
      expect(
        await username.evaluate((input: HTMLInputElement) =>
          input.checkValidity(),
        ),
        value,
      ).toBe(true);
    }
    // Native editing also enforces the maximum when another character is typed.
    await username.pressSequentially('b');
    await expect(username).toHaveValue('a'.repeat(32));

    for (const value of [
      'invalid!name',
      '1starts-with-number',
      '-starts-with-hyphen',
      '_starts-with-underscore',
      'Uppercase',
      'with space',
      'with/slash',
      '用户abc',
      'ab',
      '',
    ]) {
      await username.fill(value);
      expect(
        await username.evaluate((input: HTMLInputElement) => ({
          valid: input.checkValidity(),
          patternMismatch: input.validity.patternMismatch,
          valueMissing: input.validity.valueMissing,
        })),
        value,
      ).toEqual({
        valid: false,
        patternMismatch: value !== '',
        valueMissing: value === '',
      });
      if (isMobile) await submit.tap();
      else await submit.click();
      await expect(username).toBeFocused();
      await expect(username).toHaveValue(value);
      await expect(page.getByLabel(/^Password/)).toHaveValue(password);
      expect(requests, value).toEqual([]);
    }
    // A programmatic value must still obey the pattern beyond maxlength.
    await username.evaluate((input: HTMLInputElement) => {
      input.value = 'a'.repeat(33);
    });
    expect(
      await username.evaluate((input: HTMLInputElement) => ({
        valid: input.checkValidity(),
        patternMismatch: input.validity.patternMismatch,
      })),
    ).toEqual({ valid: false, patternMismatch: true });
    await username.press('Enter');
    expect(requests).toEqual([]);

    await expect(username).toHaveAccessibleDescription(
      '3–32 lowercase letters, numbers, hyphens or underscores. Start with a letter.',
    );
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await username.fill('valid-user');
    await username.press('Enter');
    await expect(page.getByRole('alert')).toHaveText(
      'Authentication request recorded for validation test.',
    );
    expect(requests).toEqual([
      {
        path: `/api/auth/${mode}`,
        body: {
          username: 'valid-user',
          password,
          ...(mode === 'setup' ? { teamName: 'Browser Team' } : {}),
          ...(mode !== 'login'
            ? {
                name: 'Browser Member',
                token:
                  mode === 'setup'
                    ? 'browser-test-setup-token'
                    : 'browser-test-invite',
              }
            : {}),
        },
      },
    ]);
    expect(patternErrors).toEqual([]);
    expect(errors.errors).toEqual([]);
  });
}
