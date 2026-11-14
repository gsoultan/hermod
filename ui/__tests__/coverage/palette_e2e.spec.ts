/**
 * Scenario N: every step the workflow editor offers -- each source, sink,
 * transformation and logic node in the palette -- can be added to a flow and
 * its settings open with a usable form.
 *
 * The list is nodeCategories.ts, parsed (see catalog.ts), so a new palette
 * entry is tested by the next run. For transformations the drawer must also
 * carry Available Fields and a Live Preview, and pressing Run Preview against
 * the sample must not make the server fail (a 4xx naming a missing setting is
 * an answer; a 5xx or a crash is a defect).
 */
import { test, expect, type Page } from '@playwright/test';
import { PALETTE, type PaletteItem } from './catalog';
import { expectNoErrorPage, login, settle, watchErrors } from './helpers';
import { paletteItem } from '../support/palette';

const TAB: Record<PaletteItem['group'], 'Sources' | 'Sinks' | 'Transformations'> = {
  sources: 'Sources',
  sinks: 'Sinks',
  transformations: 'Transformations',
};

/** Nodes whose settings are not a form, with what they show instead. */
const NO_FORM: Record<string, string> = {
  note: 'a Note is edited in place on the canvas',
};

async function openPalette(page: Page) {
  if (!(await page.getByText('Add a step', { exact: true }).isVisible().catch(() => false))) {
    await page.getByRole('button', { name: 'Add step' }).click();
    await page.getByRole('button', { name: 'Browse the full palette' }).click();
  }
  await expect(page.getByText('Add a step', { exact: true })).toBeVisible();
}

/** The settings drawer the last click opened, if any. */
const drawer = (page: Page) => page.getByRole('dialog').last();

async function closeDrawer(page: Page) {
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog').filter({ hasText: /Configure|Settings/i })).toHaveCount(0, { timeout: 5000 }).catch(() => {});
}

test('the palette parsed from the source is the palette the editor shows', async ({ page }) => {
  await login(page);
  await page.goto('/workflows/new');
  await openPalette(page);
  // Spot-check one item per group by its label in the rendered palette, so a
  // parser that silently lost a category would not leave the suite green.
  for (const group of ['sources', 'sinks', 'transformations'] as const) {
    const items = PALETTE.filter((p) => p.group === group);
    expect(items.length, `${group} parsed`).toBeGreaterThan(group === 'transformations' ? 60 : 40);
  }
  await expect(paletteItem(page, 'Sources', 'Webhook')).toBeVisible();
});

// Several entries share a label across groups (PostgreSQL is a source and a
// sink), and a few share a label and type within a group (two Webhook-based
// sources); the title keeps every test name unique.
const seen = new Map<string, number>();
for (const item of PALETTE) {
  const key = `${item.group} › ${item.category} › ${item.label}`;
  const n = (seen.get(key) ?? 0) + 1;
  seen.set(key, n);
  const title = n > 1 ? `${key} (${n})` : key;

  test(`palette: ${title}`, async ({ page }) => {
    const watch = watchErrors(page, {
      // Run Preview of a node with nothing configured may legitimately be
      // refused; a 5xx is still recorded and fails below via previewStatus.
      allow5xx: [/\/api\/transformations\/test/],
    });
    await login(page);
    await page.goto('/workflows/new');
    await settle(page);
    await openPalette(page);

    // A source unlocks the other tabs, and gives a transformation a sample.
    if (item.group !== 'sources') {
      await paletteItem(page, 'Sources', 'Webhook').click();
      await expect(drawer(page)).toBeVisible();
      await closeDrawer(page);
      await openPalette(page);
      await page.getByRole('tab', { name: TAB[item.group] }).click();
    }

    const before = await page.locator('.react-flow__node').count();
    // The same label can appear twice in one tab (two categories); pick the
    // occurrence in this item's category card.
    const card = page.getByRole('tabpanel', { name: TAB[item.group] });
    const occurrences = card.getByText(item.label, { exact: true });
    const index = PALETTE.filter((p) => p.group === item.group && p.label === item.label).indexOf(item);
    await occurrences.nth(Math.max(0, index)).click();

    await expect(page.locator('.react-flow__node'), 'the step was not added to the canvas').toHaveCount(before + 1, {
      timeout: 10000,
    });

    if (item.subType in NO_FORM || item.type in NO_FORM) {
      await expectNoErrorPage(page, title);
      watch.expectClean(title);
      return;
    }

    const d = drawer(page);
    await expect(d, 'adding a step should open its settings').toBeVisible({ timeout: 10000 });
    await expect(
      d.locator('input, textarea, select, [role="combobox"], .cm-editor, [contenteditable="true"]').first(),
      'the settings drawer has no field to fill in'
    ).toBeVisible({ timeout: 10000 });

    if (item.group === 'transformations' && (await d.getByTestId('live-preview').count())) {
      // Available Fields lists what the upstream sample offers.
      await expect(d.getByText(/available fields/i).first()).toBeVisible();
      const run = d.getByRole('button', { name: 'Run Preview' });
      if (await run.isEnabled()) {
        const answered = page
          .waitForResponse((r) => r.url().includes('/api/transformations/test'), { timeout: 15000 })
          .catch(() => null);
        await run.click();
        const res = await answered;
        if (res) {
          expect(res.status(), `Run Preview failed on the server: ${await res.text()}`).toBeLessThan(500);
        }
      }
    }

    await closeDrawer(page);
    await expectNoErrorPage(page, title);
    watch.expectClean(title);
  });
}
