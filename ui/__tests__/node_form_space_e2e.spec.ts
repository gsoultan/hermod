import { test, expect, type Page } from '@playwright/test';
import { login } from './support/auth';
import { paletteItem } from './support/palette';

/**
 * Every field in a node's settings drawer gets room to show what is in it.
 *
 * Two layout rules squeezed them. The drawer was a fixed share of the screen,
 * 62vw, which on a 390px phone is 242px: a source's Name input was 110px wide
 * and every placeholder was cut in half. And the rows inside it broke on
 * *screen* width, so a 1280px laptop got the wide-monitor split -- source
 * data, settings and preview side by side -- leaving the settings ~350px and
 * then halving that again for two-field rows ("Tag only (set met",
 * "Statistical Validatio").
 *
 * The assertion is on what the user sees: the rendered width of each input,
 * and whether its value or placeholder fits in it. CSS can regress through any
 * of several layers; measuring the result catches all of them.
 */

// Wide enough for a typical value ("e.g. price, amount, latency") and a Select
// label, at the app's font size.
const MIN_FIELD_PX = 180;

type Cramped = { label: string; width: number; reason: string };

const crampedFields = (page: Page) =>
  page.evaluate((minPx) => {
    const drawer = [...document.querySelectorAll('.mantine-Drawer-content')]
      .find((d) => d.getBoundingClientRect().width > 0);
    if (!drawer) return [{ label: '(no drawer)', width: 0, reason: 'drawer not open' }];
    const ctx = document.createElement('canvas').getContext('2d')!;
    const out: { label: string; width: number; reason: string }[] = [];
    const inputs = drawer.querySelectorAll<HTMLInputElement>(
      'input:not([type=hidden]):not([type=checkbox]):not([type=radio])',
    );
    for (const el of inputs) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      const label = (el.closest('.mantine-InputWrapper-root')?.querySelector('label')?.textContent
        || el.getAttribute('aria-label') || el.placeholder || '').trim();
      if (r.width < minPx) out.push({ label, width: Math.round(r.width), reason: 'narrow' });
      const cs = getComputedStyle(el);
      const room = el.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
      ctx.font = cs.font;
      const text = el.value || el.placeholder;
      if (text && ctx.measureText(text).width > room + 1) {
        out.push({ label, width: Math.round(r.width), reason: `cuts "${text}"` });
      }
    }
    return out;
  }, MIN_FIELD_PX) as Promise<Cramped[]>;

const openEditor = async (page: Page) => {
  await login(page);
  await page.goto('/workflows/new', { waitUntil: 'networkidle' });
  if (!(await page.getByText('Add a step', { exact: true }).isVisible().catch(() => false))) {
    await page.getByRole('button', { name: 'Add step' }).click();
    await page.getByRole('button', { name: 'Browse the full palette' }).click();
  }
  await expect(page.getByText('Add a step', { exact: true })).toBeVisible();
};

const openNode = async (page: Page, tab: 'Sources' | 'Transformations', label: string) => {
  if (!(await page.getByText('Add a step', { exact: true }).isVisible().catch(() => false))) {
    await page.getByRole('button', { name: 'Add step' }).click();
    await page.getByRole('button', { name: 'Browse the full palette' }).click();
  }
  await page.getByRole('tab', { name: tab }).click();
  await paletteItem(page, tab, label).click();
  await expect(page.locator('.mantine-Drawer-content')).toBeVisible();
  // The settings render lazily; wait for the first field before measuring.
  await expect(page.locator('.mantine-Drawer-content input').first()).toBeVisible();
  await page.waitForTimeout(500);
};

const closeDrawer = async (page: Page) => {
  await page.keyboard.press('Escape');
  await expect(page.locator('.mantine-Drawer-content')).toBeHidden();
};

// One source and the transformations whose rows were squeezed hardest: two
// fields with descriptions (Statistical Validation), four coordinates (Geo),
// condition rows with a delete button (Filter).
const TRANSFORMATIONS = ['Statistical Validation', 'Geo', 'Filter'];

for (const { name, viewport } of [
  { name: 'phone', viewport: { width: 390, height: 844 } },
  { name: 'laptop', viewport: { width: 1280, height: 800 } },
]) {
  test.describe(`node settings on a ${name}`, () => {
    test.use({ viewport });

    test('every field has room for its value and placeholder', async ({ page }) => {
      test.setTimeout(120000);
      await openEditor(page);

      await openNode(page, 'Sources', 'PostgreSQL');
      if (name === 'phone') {
        const width = await page.locator('.mantine-Drawer-content').evaluate((d) => d.getBoundingClientRect().width);
        expect(width, 'on a phone the drawer should take the whole screen').toBeGreaterThanOrEqual(viewport.width - 1);
      }
      expect(await crampedFields(page), 'PostgreSQL source').toEqual([]);
      await closeDrawer(page);

      for (const label of TRANSFORMATIONS) {
        await openNode(page, 'Transformations', label);
        expect(await crampedFields(page), label).toEqual([]);
        await closeDrawer(page);
      }
    });
  });
}
