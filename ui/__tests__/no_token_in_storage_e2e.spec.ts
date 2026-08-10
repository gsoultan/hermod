import { test, expect } from '@playwright/test';
import { E2E_USER, E2E_PASS } from './support/auth';

/**
 * The session credential is never readable from JavaScript.
 *
 * The UI used to keep a copy of the session JWT in localStorage, because it
 * decoded the role out of the claims and put the token in WebSocket URLs. That
 * made the HttpOnly cookie decorative: any XSS could read a 24-hour credential
 * straight out of storage.
 *
 * Both needs are gone — the role comes from GET /api/me and streams
 * authenticate with the cookie — so the token should now exist nowhere a script
 * can reach. This asserts that end to end, because it is the kind of property
 * that quietly regresses: one `setToken` call in a new login path and the
 * exposure is back with nothing failing.
 */

test.describe('Session storage', () => {
  test('logging in leaves no credential in web storage', async ({ page }) => {
    const loginBodies: string[] = [];
    page.on('response', async (response) => {
      if (response.url().includes('/api/login')) {
        loginBodies.push(await response.text().catch(() => ''));
      }
    });

    await page.goto('/login');
    await page.getByPlaceholder('Your username').fill(E2E_USER);
    await page.getByPlaceholder('Your password').fill(E2E_PASS);
    await page.click('button:has-text("Sign In")');
    await expect(page).not.toHaveURL(/login/, { timeout: 30000 });
    await expect(page.getByText('Hermod', { exact: true }).first()).toBeVisible({
      timeout: 15000,
    });

    // The response that establishes the session must not carry the token.
    expect(loginBodies.length, 'no /api/login response observed').toBeGreaterThan(0);
    for (const body of loginBodies) {
      expect(body, '/api/login must not return the session token in its body').not.toContain(
        'token',
      );
    }

    // Nothing resembling a JWT may sit in either storage.
    const storage = await page.evaluate(() => ({
      local: Object.entries(localStorage).map(([k, v]) => [k, String(v)]),
      session: Object.entries(sessionStorage).map(([k, v]) => [k, String(v)]),
    }));

    const looksLikeJWT = (value: string) => /^ey[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\./.test(value);

    for (const [key, value] of [...storage.local, ...storage.session]) {
      expect(
        looksLikeJWT(value),
        `web storage key "${key}" holds something shaped like a JWT; the session ` +
          `credential must stay in the HttpOnly cookie where scripts cannot reach it`,
      ).toBe(false);
      expect(key).not.toBe('hermod_token');
    }

    // And the cookie that does hold it must be unreadable from JS.
    const documentCookie = await page.evaluate(() => document.cookie);
    expect(
      documentCookie,
      'hermod_session must be HttpOnly and therefore absent from document.cookie',
    ).not.toContain('hermod_session');

    const cookies = await page.context().cookies();
    const session = cookies.find((c) => c.name === 'hermod_session');
    expect(session, 'no hermod_session cookie was set').toBeTruthy();
    expect(session!.httpOnly, 'hermod_session must be HttpOnly').toBe(true);
  });

  test('the app still knows who is logged in', async ({ page }) => {
    // Without the token to decode, the role comes from GET /api/me. If that
    // wiring is broken the shell renders but admin-only navigation disappears,
    // which is a silent downgrade rather than an error.
    await page.goto('/login');
    await page.getByPlaceholder('Your username').fill(E2E_USER);
    await page.getByPlaceholder('Your password').fill(E2E_PASS);
    await page.click('button:has-text("Sign In")');
    await expect(page).not.toHaveURL(/login/, { timeout: 30000 });

    // Users is an Administrator-only destination; the seeded dev account is one.
    await expect(page.getByText('Users', { exact: true }).first()).toBeVisible({
      timeout: 15000,
    });
  });

  test('logout ends the session server-side', async ({ page }) => {
    // Logout used to only delete the localStorage copy, leaving the cookie
    // valid for its full 24 hours — the session merely looked ended. Now that
    // the cookie is the whole session, that would be no logout at all.
    await page.goto('/login');
    await page.getByPlaceholder('Your username').fill(E2E_USER);
    await page.getByPlaceholder('Your password').fill(E2E_PASS);
    await page.click('button:has-text("Sign In")');
    await expect(page).not.toHaveURL(/login/, { timeout: 30000 });

    const status = await page.evaluate(async () => {
      const res = await fetch('/api/logout', { method: 'POST', credentials: 'include' });
      return res.status;
    });
    expect(status).toBe(200);

    // The cookie is gone, so a protected call is refused.
    const meStatus = await page.evaluate(async () => {
      const res = await fetch('/api/me', { credentials: 'include' });
      return res.status;
    });
    expect(meStatus, 'the session cookie survived logout').toBe(401);
  });
});
