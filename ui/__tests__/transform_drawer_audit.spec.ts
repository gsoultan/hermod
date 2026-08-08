import { test, type Page } from '@playwright/test';
import { mkdirSync } from 'fs';
import { E2E_USER, E2E_PASS } from './support/auth';

const BASE = process.env.AUDIT_BASE_URL || 'http://localhost:5175';
const SHOTS = 'audit-shots';

const login = async (page: Page) => {
  await page.goto(`${BASE}/login`);
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

  await page.goto(`${BASE}/workflows/new`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);

  // Open the workflow panel (IconLayoutSidebarRight, far right of the toolbar).
  // Its open/closed state persists between visits, so toggle only when shut or
  // the click closes a panel that was already open.
  if (!(await page.getByText('Workflow Panel').isVisible().catch(() => false))) {
    await page.mouse.click(1412, 89);
    await page.waitForTimeout(1200);
  }
  await page.screenshot({ path: `${SHOTS}/panel-open.png` });

  // Add a PostgreSQL source: the Transformations tab stays disabled until a
  // source exists, so this has to come first.
  await page.mouse.click(1370, 356);
  await page.waitForTimeout(1500);
  const source = await drawerGeometry(page);
  console.log('\n=== SOURCE DRAWER ===\n' + JSON.stringify(source));
  await page.screenshot({ path: `${SHOTS}/drawer-source.png` });

  // Dismiss and switch to Transformations, now unlocked.
  await page.keyboard.press('Escape');
  await page.waitForTimeout(800);
  await page.evaluate(() => {
    for (const el of document.querySelectorAll('button, [role=tab]')) {
      if ((el.textContent || '').trim() === 'Transformations') { (el as HTMLElement).click(); return; }
    }
  });
  await page.waitForTimeout(1000);
  await page.screenshot({ path: `${SHOTS}/palette-transformations.png` });

  // Drop the first transformation, then open its configuration.
  await page.mouse.click(1370, 356);
  await page.waitForTimeout(1500);
  await page.screenshot({ path: `${SHOTS}/editor-with-nodes.png` });

  const nodes = page.locator('.react-flow__node');
  const count = await nodes.count();
  console.log(`nodes on canvas: ${count}`);
  if (count > 1) {
    await nodes.last().dblclick({ timeout: 10000 }).catch(() => {});
    await page.waitForTimeout(1800);
    const transform = await drawerGeometry(page);
    console.log('=== TRANSFORM DRAWER ===\n' + JSON.stringify(transform));
    await page.screenshot({ path: `${SHOTS}/drawer-transform.png` });
  }
});
