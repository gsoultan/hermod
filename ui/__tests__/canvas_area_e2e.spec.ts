import { test, expect, type Page } from '@playwright/test';
import { E2E_USER, E2E_PASS } from './support/auth';

/**
 * Regression guard for workflow-editor canvas real estate.
 *
 * The editor was once nested five containers deep — AppShell navbar + padding,
 * a rounded inner card with its own padding and border, a hard-coded
 * `calc(100vh - 120px)`, two stacked header bars, and an inline 400px palette.
 * That chrome cost exactly 744px horizontally, leaving the canvas roughly 20%
 * of a 1440x900 screen while n8n gives its canvas the whole viewport.
 *
 * These assertions measure the rendered canvas rather than inspecting CSS, so
 * the chrome cannot creep back in unnoticed no matter which layer reintroduces
 * it.
 */

const login = async (page: Page) => {
  await page.goto('/login');
  await page.getByPlaceholder('Your username').fill(E2E_USER);
  await page.getByPlaceholder('Your password').fill(E2E_PASS);
  await page.getByRole('button', { name: /sign in|login/i }).click();
  await page.waitForURL((url) => !url.pathname.startsWith('/login'), { timeout: 30000 });
};

/** Fraction of the viewport area occupied by the React Flow canvas. */
const canvasAreaRatio = async (page: Page) => {
  const pane = page.locator('.react-flow__renderer, .react-flow').first();
  await pane.waitFor({ state: 'visible', timeout: 30000 });

  const box = await pane.boundingBox();
  if (!box) throw new Error('canvas has no bounding box');

  const viewport = page.viewportSize();
  if (!viewport) throw new Error('no viewport size');

  return {
    ratio: (box.width * box.height) / (viewport.width * viewport.height),
    box,
    viewport,
  };
};

test.describe('Workflow editor canvas real estate', () => {
  // Sized to the laptop the original 20% measurement was taken on, so the
  // numbers here are directly comparable to that baseline.
  test.use({ viewport: { width: 1440, height: 900 } });

  test('canvas occupies the majority of the viewport with overlays closed', async ({ page }) => {
    await login(page);
    await page.goto('/workflows/new');

    const { ratio, box, viewport } = await canvasAreaRatio(page);
    console.log(
      `canvas ${Math.round(box.width)}x${Math.round(box.height)} of ` +
        `${viewport.width}x${viewport.height} = ${(ratio * 100).toFixed(1)}%`,
    );

    // Before the layout work this was ~20%. The palette and log panel are now
    // overlays and the editor route is full-bleed, so the canvas should hold
    // the large majority of the screen.
    expect(ratio).toBeGreaterThan(0.6);
  });

  test('canvas keeps most of its width when the node palette is open', async ({ page }) => {
    await login(page);
    await page.goto('/workflows/new');

    const before = await canvasAreaRatio(page);

    // The palette floats over the canvas rather than displacing it, so opening
    // it must not shrink the canvas box at all.
    const addNode = page.getByRole('button', { name: /add node|nodes|components/i }).first();
    if (await addNode.isVisible().catch(() => false)) {
      await addNode.click();
      await page.waitForTimeout(500);
    }

    const after = await canvasAreaRatio(page);
    console.log(`palette open: ${(after.ratio * 100).toFixed(1)}% (was ${(before.ratio * 100).toFixed(1)}%)`);

    expect(after.ratio).toBeGreaterThan(0.6);
    // Overlay, not inline: the canvas width must not shrink by the palette's
    // 400px when it opens.
    expect(after.box.width).toBeGreaterThanOrEqual(before.box.width - 1);
  });

  test('canvas scales with the viewport', async ({ page }) => {
    await login(page);
    await page.goto('/workflows/new');

    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 1920, height: 1080 },
      { width: 2560, height: 1440 },
    ]) {
      await page.setViewportSize(viewport);
      await page.waitForTimeout(400);

      const { ratio } = await canvasAreaRatio(page);
      console.log(`${viewport.width}x${viewport.height}: ${(ratio * 100).toFixed(1)}%`);
      expect(ratio).toBeGreaterThan(0.6);
    }
  });

  // The editor sized itself as "the viewport minus the header", with 60px
  // assumed when no header said otherwise. The redesign dropped the header, so
  // the 60px was taken anyway and left a blank strip under the live log panel.
  // Measured rather than read from CSS, so any layer that adds the gap back
  // fails here.
  test('live log panel reaches the bottom of the screen', async ({ page }) => {
    await login(page);
    await page.goto('/workflows/new');

    const header = page.getByRole('button', { name: /live workflow logs/i });
    await header.waitFor({ state: 'visible', timeout: 30000 });
    const panelBottom = () =>
      header.evaluate((el) => el.closest('.mantine-Paper-root')!.getBoundingClientRect().bottom);

    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 1920, height: 1080 },
    ]) {
      await page.setViewportSize(viewport);
      await page.waitForTimeout(400);
      expect(Math.round(await panelBottom())).toBe(viewport.height);

      // Open as well as folded: the panel grows upward, its foot stays put.
      await header.click();
      await page.waitForTimeout(400);
      expect(Math.round(await panelBottom())).toBe(viewport.height);
      await header.click();
      await page.waitForTimeout(400);
    }
  });

  // On a phone the bottom tabs take the foot of the screen, so the panel
  // should sit right on top of them: no gap, and not tucked underneath.
  test('live log panel sits on the bottom tabs on a phone', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await login(page);
    await page.goto('/workflows/new');

    const header = page.getByRole('button', { name: /live workflow logs/i });
    await header.waitFor({ state: 'visible', timeout: 30000 });
    const panelBottom = await header.evaluate(
      (el) => el.closest('.mantine-Paper-root')!.getBoundingClientRect().bottom,
    );
    const tabsTop = await page
      .getByRole('navigation', { name: 'Bottom tabs' })
      .evaluate((el) => el.getBoundingClientRect().top);

    expect(Math.round(panelBottom)).toBe(Math.round(tabsTop));
    // Nothing to scroll: the editor fits the screen above the tabs.
    expect(await page.evaluate(() => document.scrollingElement!.scrollHeight)).toBe(844);
  });
});
