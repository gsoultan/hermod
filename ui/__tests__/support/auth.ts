import { expect, type Page } from '@playwright/test';

/**
 * One place that knows how to log in.
 *
 * Credentials used to be typed into each spec by hand, and they drifted: the
 * three specs CI actually runs said `admin/admin` (correct, and what
 * scripts/dev.sh seeds) while every spec CI does *not* run said
 * `admin/admin123`. Nothing executed them, so nothing reported that they could
 * no longer authenticate — the suite looked like coverage while testing
 * nothing. Wiring the specs into CI only helps if they can log in, so the
 * credential lives here and comes from the environment.
 *
 * Note the endpoint is `/api/login`, not `/api/auth/login`.
 */
export const E2E_USER = process.env.HERMOD_E2E_USER || 'admin';
export const E2E_PASS = process.env.HERMOD_E2E_PASS || 'admin';

/**
 * Log in through the UI and wait until the app has left the login route.
 * Idempotent: returns immediately if the session is already authenticated.
 */
export async function login(page: Page): Promise<void> {
  await page.goto('/login');

  // An existing session redirects away from /login on its own.
  if (!/\/login/.test(page.url())) return;

  await page.getByPlaceholder('Your username').fill(E2E_USER);
  await page.getByPlaceholder('Your password').fill(E2E_PASS);
  await page.click('button:has-text("Sign In")');

  await expect(page, 'login failed — check HERMOD_E2E_USER / HERMOD_E2E_PASS')
    .not.toHaveURL(/login/, { timeout: 30000 });
}

/**
 * Call the API as the logged-in user.
 *
 * Authentication is the HttpOnly session cookie, which `credentials: 'include'`
 * sends. There is deliberately no way to obtain a bearer token here: the session
 * token is no longer readable from JavaScript, which is the point — a helper
 * that could still reach it would mean the browser could too.
 */
export async function apiRequest(
  page: Page,
  path: string,
  init?: { method?: string; body?: unknown }
): Promise<{ status: number; body: string }> {
  return page.evaluate(
    async ([p, method, body]) => {
      const headers: Record<string, string> = { 'Content-Type': 'application/json' };

      // State-changing requests need the CSRF token echoed from its cookie.
      // Reading it here rather than importing the app's helper keeps this
      // usable from page.evaluate, which runs outside the bundle.
      const verb = ((method as string) || 'GET').toUpperCase();
      if (!['GET', 'HEAD', 'OPTIONS'].includes(verb)) {
        const match = document.cookie.match(/(?:^|;\s*)hermod_csrf=([^;]*)/);
        if (match) headers['X-CSRF-Token'] = decodeURIComponent(match[1]);
      }

      const res = await fetch(p as string, {
        method: verb,
        credentials: 'include',
        headers,
        body: body ? (body as string) : undefined,
      });
      return { status: res.status, body: await res.text() };
    },
    [path, init?.method ?? 'GET', init?.body ? JSON.stringify(init.body) : ''] as const
  );
}
