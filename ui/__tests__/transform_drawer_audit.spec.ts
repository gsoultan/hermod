import { test, expect, type Page } from '@playwright/test';
import { mkdirSync } from 'fs';
import { E2E_USER, E2E_PASS } from './support/auth';

const SHOTS = 'audit-shots';

const login = async (page: Page) => {
  await page.goto('/login');
  await page.getByPlaceholder('Your username').fill(E2E_USER);
  await page.getByPlaceholder('Your password').fill(E2E_PASS);
  await page.getByRole('button', { name: /sign in|login/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/login'), { timeout: 30000 });
};

const drawerGeometry = (page: Page) =>
  page.evaluate(() => {
    const d = document.querySelector('[class*=mantine-Drawer-content], [role=dialog]');
    if (!d) return { error: 'no drawer' } as any;
    const r = d.getBoundingClientRect();
    return {
      width: Math.round(r.width),
      pctOfScreen: Math.round((r.width / window.innerWidth) * 100),
      fields: d.querySelectorAll('input, textarea, select, .cm-editor').length,
    };
  });

test('node config drawers size to their content', async ({ page }) => {
  test.setTimeout(240000);
  mkdirSync(SHOTS, { recursive: true });
  await page.setViewportSize({ width: 1440, height: 900 });
  await login(page);

  await page.goto('/workflows/new', { waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);

  // This used to drive the palette by mouse coordinates -- click (1370, 356) and
  // hope a PostgreSQL source was still there. Adding a search box to the panel
  // moved everything down by its height, so the clicks landed on nothing: the
  // drawer was never opened, no nodes reached the canvas, and the run still
  // reported success because the measurements were logged rather than asserted
  // and the transform half sat behind `if (count > 1)`. It measured nothing and
  // said so only in text nobody reads on a green run.
  //
  // Selectors by role and label now, and every step that must happen is
  // asserted. A layout change should break this loudly or not at all.
  if (!(await page.getByText('Workflow Panel').isVisible().catch(() => false))) {
    await page.getByRole('button', { name: 'Workflow panel' }).click();
  }
  await expect(page.getByText('Workflow Panel')).toBeVisible();
  await page.screenshot({ path: `${SHOTS}/panel-open.png` });

  // A source has to exist before the Transformations tab unlocks.
  await page.getByText('PostgreSQL', { exact: true }).first().click();
  await page.waitForTimeout(1500);

  const source = await drawerGeometry(page);
  console.log('\n=== SOURCE DRAWER ===\n' + JSON.stringify(source));
  expect(source.error, 'clicking a source should open its config drawer').toBeUndefined();
  await page.screenshot({ path: `${SHOTS}/drawer-source.png` });

  await page.keyboard.press('Escape');
  await page.waitForTimeout(800);

  await page.getByRole('tab', { name: 'Transformations' }).click();
  await page.waitForTimeout(1000);
  await page.screenshot({ path: `${SHOTS}/palette-transformations.png` });

  await page.getByText('Mapping', { exact: true }).first().click();
  await page.waitForTimeout(1500);
  await page.screenshot({ path: `${SHOTS}/editor-with-nodes.png` });

  // Clicking a palette item adds the node and opens its configuration in one
  // go, so the drawer is already up. The previous version closed the panel and
  // double-clicked the node on the canvas, which only worked because the node
  // happened not to be underneath the panel.
  const transform = await drawerGeometry(page);
  console.log('=== TRANSFORM DRAWER ===\n' + JSON.stringify(transform));
  expect(transform.error, 'choosing a transformation should open its config drawer').toBeUndefined();
  await page.screenshot({ path: `${SHOTS}/drawer-transform.png` });

  await page.keyboard.press('Escape');
  await page.waitForTimeout(800);

  const nodes = page.locator('.react-flow__node');
  await expect(nodes, 'a source and a transformation should both be on the canvas').toHaveCount(2);
  console.log(`nodes on canvas: ${await nodes.count()}`);
});
