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
  // A flow page now puts the canvas in a card with the page's own gutter
  // around it, so the panel must sit on the card's floor and the card must end
  // exactly one gutter above the screen's foot. Measured rather than read from
  // CSS, so any layer that adds a gap back fails here.
  const logFrame = (page: Page) =>
    page.getByRole('button', { name: /^live logs/i }).evaluate((el) => {
      const panel = el.closest('.mantine-Paper-root')!.getBoundingClientRect();
      const cardEl = el.closest('.hermod-flow-canvas')!;
      const card = cardEl.getBoundingClientRect();
      const pageEl = el.closest('.hermod-flow-page')!;
      return {
        panelBottom: panel.bottom,
        cardFloor: card.bottom - cardEl.clientTop,
        cardBottom: card.bottom,
        pageBottom: pageEl.getBoundingClientRect().bottom,
        pageGutter: parseFloat(getComputedStyle(pageEl).paddingBottom),
      };
    });

  test('live log panel reaches the bottom of the canvas card', async ({ page }) => {
    await login(page);
    await page.goto('/workflows/new');

    const header = page.getByRole('button', { name: /^live logs/i });
    await header.waitFor({ state: 'visible', timeout: 30000 });

    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 1920, height: 1080 },
    ]) {
      await page.setViewportSize(viewport);
      await page.waitForTimeout(400);

      // Open as well as folded: the panel grows upward, its foot stays put.
      for (let i = 0; i < 2; i++) {
        const f = await logFrame(page);
        expect(Math.round(f.pageBottom)).toBe(viewport.height);
        expect(Math.round(f.cardBottom)).toBe(Math.round(f.pageBottom - f.pageGutter));
        expect(Math.round(f.panelBottom)).toBe(Math.round(f.cardFloor));
        await header.click();
        await page.waitForTimeout(400);
      }
    }
  });

  // On a phone the flow's action bar takes the foot of the screen in place of
  // the bottom tabs, so the canvas card, and the panel inside it, should sit
  // right on top of it: no gap, and not tucked underneath.
  test('live log panel sits on the action bar on a phone', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await login(page);
    await page.goto('/workflows/new');

    await page.getByRole('button', { name: /^live logs/i }).waitFor({ state: 'visible', timeout: 30000 });
    const f = await logFrame(page);
    const barTop = await page
      .getByRole('region', { name: 'Flow actions' })
      .evaluate((el) => el.getBoundingClientRect().top);

    expect(await page.getByRole('navigation', { name: 'Bottom tabs' }).count()).toBe(0);
    expect(Math.round(f.cardBottom)).toBe(Math.round(barTop));
    expect(Math.round(f.panelBottom)).toBe(Math.round(f.cardFloor));
    // Nothing to scroll: the editor fits the screen above the bar.
    expect(await page.evaluate(() => document.scrollingElement!.scrollHeight)).toBe(844);
  });
});
