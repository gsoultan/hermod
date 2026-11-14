/**
 * Scenario P: every page, and every tab on it, opens cleanly.
 *
 * "Cleanly" is measured, not eyeballed: no uncaught exception, no console
 * error, no 5xx from the API, and not the router's error page. Each route in
 * router.tsx must have an entry in PAGES below -- the first test fails when a
 * new route ships without one, so the list cannot fall behind the app.
 */
import { test, expect, type Page } from '@playwright/test';
import { ROUTES } from './catalog';
import { Cleanup, createBasicFixtures, expectNoErrorPage, login, settle, watchErrors, type Fixtures } from './helpers';

type Url = (f: Fixtures) => string | null;

/**
 * Where each route is opened. `null` means it is covered by another spec, and
 * the reason says which.
 */
const PAGES: Record<string, Url | { skip: string }> = {
  '/': () => '/',
  '/sources': () => '/sources',
  '/sources/new': () => '/sources/new',
  '/sources/$sourceId/edit': (f) => `/sources/${f.sourceId}/edit`,
  '/sinks': () => '/sinks',
  '/sinks/new': () => '/sinks/new',
  '/sinks/$sinkId/edit': (f) => `/sinks/${f.sinkId}/edit`,
  '/vhosts': () => '/vhosts',
  '/vhosts/new': () => '/vhosts/new',
  '/vhosts/$vhostId/edit': (f) => `/vhosts/${f.vhostId}/edit`,
  '/workers': () => '/workers',
  '/workers/new': () => '/workers/new',
  '/workers/$workerId/edit': (f) => (f.workerId ? `/workers/${f.workerId}/edit` : null),
  '/workflows': () => '/workflows',
  '/workflows/$id': (f) => `/workflows/${f.workflowId}`,
  '/workflows/$id/edit': (f) => `/workflows/${f.workflowId}/edit`,
  '/workflows/new': () => '/workflows/new',
  '/users': () => '/users',
  '/users/new': () => '/users/new',
  '/users/$userId/edit': (f) => `/users/${f.userId}/edit`,
  '/about': () => '/about',
  '/updates': () => '/updates',
  '/profile': () => '/profile',
  '/settings': () => '/settings',
  '/connections': () => '/connections',
  '/activity': () => '/activity',
  '/developer': () => '/developer',
  '/logs': () => '/logs',
  '/schemas': () => '/schemas',
  '/secrets': () => '/secrets',
  '/ml/models': () => '/ml/models',
  '/ai/budget': () => '/ai/budget',
  '/audit-logs': () => '/audit-logs',
  '/lineage': () => '/lineage',
  '/health': () => '/health',
  '/compliance': () => '/compliance',
  '/marketplace': () => '/marketplace',
  '/approvals': () => '/approvals',
  '/environments': () => '/environments',
  '/promotions': () => '/promotions',
  // Pairing links another instance sends. With no peer paired the page has to
  // say so rather than crash.
  '/environments/accept/$from': () => '/environments/accept/coverage-peer',
  '/environments/accept/$from/$vhost': () => '/environments/accept/coverage-peer/default',
  '/dead-letters': () => '/dead-letters',
  '/login': { skip: 'opened logged out in "public pages" below' },
  '/forgot-password': { skip: 'opened logged out in "public pages" below' },
  '/setup': { skip: 'first-run only; scripts/dev.sh completes it before the suite runs, after which it redirects' },
};

test('every route in router.tsx has a page scenario', () => {
  const missing = ROUTES.filter((r) => !(r in PAGES));
  expect(missing, 'add these routes to PAGES in pages_e2e.spec.ts').toEqual([]);
  const stale = Object.keys(PAGES).filter((r) => !ROUTES.includes(r));
  expect(stale, 'these PAGES entries name routes that no longer exist').toEqual([]);
});

/** Click every tab the page shows, one after another, checking each. */
async function visitTabs(page: Page, where: string, watch: ReturnType<typeof watchErrors>) {
  const tabs = page.getByRole('tab');
  const count = await tabs.count();
  const names: string[] = [];
  for (let i = 0; i < count; i++) {
    const tab = tabs.nth(i);
    if (!(await tab.isVisible()) || (await tab.isDisabled())) continue;
    const name = ((await tab.textContent()) || `#${i}`).trim();
    names.push(name);
    await tab.click();
    await settle(page, 250);
    await expectNoErrorPage(page, `${where} › tab "${name}"`);
    watch.expectClean(`${where} › tab "${name}"`);
  }
  return names;
}

test.describe('every page opens cleanly, tabs included', () => {
  test.describe.configure({ mode: 'serial' });
  let fixtures: Fixtures;
  const cleanup = new Cleanup();

  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage();
    await login(page);
    fixtures = await createBasicFixtures(page, cleanup);
    // Cleanup runs through this page's session, so keep it open.
    test.info().annotations.push({ type: 'fixtures', description: JSON.stringify(fixtures) });
    (globalThis as any).__coveragePage = page;
  });

  test.afterAll(async () => {
    await cleanup.run();
    await (globalThis as any).__coveragePage?.close();
  });

  for (const [route, spec] of Object.entries(PAGES)) {
    if ('skip' in (spec as object)) continue;
    test(`page ${route}`, async ({ page }) => {
      const url = (spec as Url)(fixtures);
      test.skip(url === null, 'no instance of this to open on this server');
      const watch = watchErrors(page);
      await login(page);
      watch.reset();
      await page.goto(url!);
      await settle(page);
      await expect(page.locator('body')).not.toBeEmpty();
      await expectNoErrorPage(page, route);
      watch.expectClean(route);
      const tabs = await visitTabs(page, route, watch);
      test.info().annotations.push({ type: 'tabs', description: tabs.join(', ') || '(none)' });
    });
  }
});

test.describe('public pages', () => {
  for (const path of ['/login', '/forgot-password']) {
    test(`page ${path} (logged out)`, async ({ page }) => {
      const watch = watchErrors(page);
      await page.goto(path);
      await settle(page);
      await expectNoErrorPage(page, path);
      await expect(page.getByRole('button').first()).toBeVisible();
      watch.expectClean(path);
    });
  }

  test('a protected page sends a logged-out visitor to the login page', async ({ page }) => {
    await page.goto('/workflows');
    await expect(page).toHaveURL(/\/login/);
  });
});
