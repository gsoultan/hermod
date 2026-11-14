/**
 * Two Hermods for the promote suite: staging, which sends, and production,
 * which receives.
 *
 * They must differ by hostname, not only by port. Both set a host-only session
 * cookie of the same name, and a browser keeps cookies per host, not per port:
 * staging on `localhost` and production on `127.0.0.1` keep two sessions; two
 * ports of `localhost` overwrite each other's and sign one of them out.
 *
 * Start staging as the usual dev stack and production as a second one, in
 * its own state directory and on its own ports (docs/testing/e2e-scenarios.md):
 *
 *   ./scripts/dev.sh --sqlite --reset --detach
 *   HERMOD_DEV_DIR=$PWD/.dev-prod HERMOD_DEV_API_PORT=4006 HERMOD_DEV_GRPC_PORT=50052 \
 *     HERMOD_DEV_UI_PORT=5176 ./scripts/dev.sh --sqlite --reset --detach
 *
 * Both are signed in as the administrator first-run setup creates. Its vhosts
 * are null, not ["*"], and that difference hid the worst of the promote bugs,
 * so do not swap in a test user here.
 */
import { expect, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { apiRequest, E2E_PASS, E2E_USER } from '../support/auth';

/**
 * Each instance's API port, which serves the built UI as well. Not the Vite
 * dev servers: scripts/dev.sh stops every Vite in the checkout when it starts,
 * so starting the second stack takes the first one's UI down.
 */
export const STAGING_URL = (process.env.HERMOD_E2E_STAGING_URL || 'http://localhost:4005').replace(/\/+$/, '');

/** Production's address, as staging is told it in Connect a server. */
export const PROD_URL = (process.env.HERMOD_E2E_PROD_URL || 'http://127.0.0.1:4006').replace(/\/+$/, '');

/** The vhost the flow lives in. Not `default`, which every vhost check lets through. */
export const VHOST = 'hr';

export interface Instance {
  name: 'staging' | 'production';
  page: Page;
  /** Absolute URL of a path on this instance. */
  url(path: string): string;
  api<T = any>(method: string, path: string, body?: unknown): Promise<T>;
  raw(method: string, path: string, body?: unknown): Promise<{ status: number; body: string }>;
}

function instance(name: Instance['name'], page: Page, base: string): Instance {
  const raw = (method: string, path: string, body?: unknown) => apiRequest(page, path, { method, body });
  return {
    name,
    page,
    url: (path) => `${base}${path}`,
    raw,
    async api(method, path, body) {
      const res = await raw(method, path, body);
      if (res.status >= 400) throw new Error(`${name}: ${method} ${path} answered ${res.status}: ${res.body.slice(0, 500)}`);
      return (res.body ? JSON.parse(res.body) : null) as any;
    },
  };
}

/** Sign in on the page's current origin as `user` (the setup admin by default). */
export async function signIn(page: Page, base: string, user = E2E_USER, pass = E2E_PASS) {
  await page.goto(`${base}/login`);
  await page.getByPlaceholder('Your username').fill(user);
  await page.getByPlaceholder('Your password').fill(pass);
  await page.getByRole('button', { name: 'Sign In' }).click();
  await expect(page, `${user} could not sign in to ${base}`).not.toHaveURL(/\/login/, { timeout: 30000 });
}

/** A page signed in to each instance, in one browser context, so the popup in Connect a server shares production's session. */
export async function openBoth(browser: Browser, stagingBase: string): Promise<{ ctx: BrowserContext; staging: Instance; prod: Instance }> {
  const ctx = await browser.newContext({ baseURL: stagingBase });
  const sp = await ctx.newPage();
  const pp = await ctx.newPage();
  await signIn(sp, stagingBase);
  await signIn(pp, PROD_URL);
  return { ctx, staging: instance('staging', sp, stagingBase), prod: instance('production', pp, PROD_URL) };
}

/** Another user on `on`, signed in on a page of its own context. */
export async function asUser(
  browser: Browser,
  on: Instance,
  base: string,
  user: { username: string; role: 'Administrator' | 'Editor' | 'Viewer'; vhosts: string[] },
): Promise<Instance> {
  const password = 'Promote-Passw0rd!';
  const existing = await on.api<any>('GET', '/api/users');
  const list: any[] = Array.isArray(existing) ? existing : existing?.data ?? [];
  if (!list.some((u) => u.username === user.username)) {
    await on.api('POST', '/api/users', {
      ...user,
      password,
      full_name: user.username,
      email: `${user.username}@example.com`,
    });
  }
  const ctx = await browser.newContext({ baseURL: base });
  const page = await ctx.newPage();
  await signIn(page, base, user.username, password);
  return instance(on.name, page, base);
}
