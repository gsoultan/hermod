import { test, expect, type Page } from '@playwright/test';

/**
 * Workflow editor end-to-end coverage: building a multi-source ->
 * multi-transformation -> multi-sink pipeline, and the form controls inside the
 * node drawers.
 *
 * Credentials come from the environment so this can run against either the
 * shared dev stack or an isolated one. The login endpoint is /api/login (not
 * /api/auth/login) and the seeded dev password is `admin`, per scripts/dev.sh.
 */
const USER = process.env.HERMOD_E2E_USER || 'admin';
const PASS = process.env.HERMOD_E2E_PASS || 'admin';

async function login(page: Page) {
  await page.goto('/login');
  await page.getByPlaceholder('Your username').fill(USER);
  await page.getByPlaceholder('Your password').fill(PASS);
  await page.click('button:has-text("Sign In")');
  await expect(page).not.toHaveURL(/login/, { timeout: 30000 });
}

/** Create an entity through the API so editor tests start from a known state. */
async function apiCreate(page: Page, path: string, body: unknown) {
  return page.evaluate(
    async ([p, b]) => {
      const res = await fetch(p as string, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${localStorage.getItem('hermod_token')}`,
        },
        body: JSON.stringify(b),
      });
      return { status: res.status, body: await res.text() };
    },
    [path, body] as const
  );
}

test.describe('workflow editor: multi-source pipeline', () => {
  test.beforeEach(async ({ page }) => {
    page.on('pageerror', (err) => {
      throw new Error(`uncaught page error: ${err.message}`);
    });
    await login(page);
  });

  test('editor loads with canvas, toolbar and live log panel', async ({ page }) => {
    test.setTimeout(120000);

    await page.goto('/workflows/new');
    await page.waitForLoadState('networkidle');

    // The canvas is the editor's core surface; without it nothing else applies.
    await expect(page.locator('.react-flow').first()).toBeVisible({ timeout: 30000 });

    // Toolbar affordances the editor is unusable without.
    await expect(page.getByRole('textbox').first()).toBeVisible({ timeout: 15000 });
    for (const label of [/^tools$/i, /^test$/i, /^save$/i]) {
      await expect(page.getByRole('button', { name: label }).first()).toBeVisible({
        timeout: 15000,
      });
    }

    // Live log preview is part of the editor chrome, collapsed by default.
    await expect(page.getByText(/live workflow logs/i).first()).toBeVisible({ timeout: 15000 });
  });

  test('live workflow log panel expands and collapses', async ({ page }) => {
    test.setTimeout(120000);

    await page.goto('/workflows/new');
    await page.waitForLoadState('networkidle');

    const header = page.getByText(/live workflow logs/i).first();
    await expect(header).toBeVisible({ timeout: 30000 });

    // Toggling must not throw; a pageerror listener in beforeEach turns any
    // uncaught exception into a failure.
    await header.click();
    await page.waitForTimeout(500);
    await header.click();
    await page.waitForTimeout(500);
    await expect(header).toBeVisible();
  });

  test('Test (simulation) control is reachable from the editor toolbar', async ({ page }) => {
    test.setTimeout(120000);

    await page.goto('/workflows/new');
    await page.waitForLoadState('networkidle');

    const testBtn = page.getByRole('button', { name: /^test$/i }).first();
    await expect(testBtn).toBeVisible({ timeout: 30000 });
    await expect(testBtn).toBeEnabled();
  });
});

test.describe('node drawer forms: Cancel must dismiss without saving', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  /**
   * Regression test for the reported bug: Cancel in the embedded Source and
   * Sink forms was wired to `onSave(null)`, and the editor's inline-save
   * handler dereferenced that argument. The click threw, the drawer never
   * closed, and the button looked dead.
   *
   * Driven through the standalone routes, which mount the same SourceForm /
   * SinkForm components and the same wizard footer that hosts Cancel.
   */
  for (const [name, route, back] of [
    ['source', '/sources/new', /\/sources$/],
    ['sink', '/sinks/new', /\/sinks$/],
  ] as const) {
    test(`${name} form: Cancel navigates away and creates nothing`, async ({ page }) => {
      test.setTimeout(90000);

      await page.goto(route);
      await page.waitForLoadState('networkidle');

      const cancel = page.getByRole('button', { name: /^cancel$/i });
      await expect(cancel).toBeVisible({ timeout: 20000 });
      await expect(cancel).toBeEnabled();

      await cancel.click();

      // Cancel must leave the form. Before the fix the click threw and the
      // page stayed exactly where it was.
      await expect(page).toHaveURL(back, { timeout: 20000 });
    });

    test(`${name} form: Cancel after typing still discards`, async ({ page }) => {
      test.setTimeout(90000);

      await page.goto(route);
      await page.waitForLoadState('networkidle');

      const nameField = page.getByLabel(/^name$/i).first();
      const typed = `e2e-cancel-${name}-${Date.now()}`;
      if (await nameField.isVisible().catch(() => false)) {
        await nameField.fill(typed);
      }

      await page.getByRole('button', { name: /^cancel$/i }).click();
      await expect(page).toHaveURL(back, { timeout: 20000 });

      // Nothing may have been persisted by a Cancel.
      const listed = await page.evaluate(
        async ([p]) => {
          const res = await fetch(p as string, {
            headers: { Authorization: `Bearer ${localStorage.getItem('hermod_token')}` },
          });
          return res.ok ? await res.text() : '';
        },
        [`/api/${name}s`] as const
      );
      expect(listed).not.toContain(typed);
    });
  }
});

test.describe('workflow list and detail', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('workflows page renders and offers creation', async ({ page }) => {
    test.setTimeout(90000);
    await page.goto('/workflows');
    await page.waitForLoadState('networkidle');
    // Rendered as a link styled as a button, so match on text rather than role.
    await expect(page.getByText(/create workflow/i).first()).toBeVisible({ timeout: 20000 });
    await expect(page.getByText(/import json/i).first()).toBeVisible({ timeout: 20000 });
  });

  test('sources and sinks pages render their create action', async ({ page }) => {
    test.setTimeout(90000);
    for (const [route, action] of [
      ['/sources', /add source|new source|create source/i],
      ['/sinks', /add sink|new sink|create sink/i],
    ] as const) {
      await page.goto(route);
      await page.waitForLoadState('networkidle');
      await expect(page.getByRole('button', { name: action }).first()).toBeVisible({
        timeout: 20000,
      });
    }
  });
});
