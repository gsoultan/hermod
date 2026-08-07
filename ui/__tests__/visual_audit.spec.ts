import { test, expect, type Page } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'fs';

/**
 * Sidebar regression guard.
 *
 * Two defects this catches, both of which shipped:
 *   - the Go pseudo-version ("v1.2.5-0.20260730072614-816c03f6a612+dirty")
 *     wrapped to three lines and spilled out of the 80px collapsed rail onto
 *     the canvas;
 *   - collapsed nav icons sat 9px left of centre, because the hidden label body
 *     and an empty trailing section still claimed the row's flex space, so the
 *     active highlight read as lopsided.
 *
 * Both are invisible to a DOM-only check and to a passing render test, so they
 * are measured here in geometry.
 */

const SHOTS = 'audit-shots';

const login = async (page: Page) => {
  await page.goto('/login');
  await page.getByPlaceholder('Your username').fill('admin');
  await page.getByPlaceholder('Your password').fill('admin');
  await page.getByRole('button', { name: /sign in|login/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/login'), { timeout: 30000 });
};

/** Text wider than the box drawn around it, or spilling past the navbar edge. */
const OVERFLOW_PROBE = `(() => {
  const nav = document.querySelector('nav') || document.querySelector('.mantine-AppShell-navbar');
  if (!nav) return { error: 'no navbar' };
  const navR = nav.getBoundingClientRect();
  const clipped = [];
  for (const el of nav.querySelectorAll('*')) {
    const text = (el.textContent || '').trim();
    if (!text || el.children.length > 0) continue;
    const r = el.getBoundingClientRect();
    if (r.width === 0) continue;
    if (el.scrollWidth > el.clientWidth + 1) {
      clipped.push({ text: text.slice(0, 40), scrollWidth: el.scrollWidth, clientWidth: el.clientWidth });
    } else if (r.right > navR.right + 1) {
      clipped.push({ text: text.slice(0, 40), right: Math.round(r.right), navRight: Math.round(navR.right) });
    }
  }
  return { navWidth: Math.round(navR.width), clipped };
})()`;

/** How far each icon sits from the centre of the row that highlights it. */
const SYMMETRY_PROBE = `(() => {
  const nav = document.querySelector('nav') || document.querySelector('.mantine-AppShell-navbar');
  if (!nav) return { error: 'no navbar' };
  const out = [];
  for (const row of nav.querySelectorAll('.mantine-NavLink-root')) {
    const svg = row.querySelector('svg');
    if (!svg) continue;
    const r = row.getBoundingClientRect();
    const s = svg.getBoundingClientRect();
    if (r.width === 0) continue;
    out.push({
      label: (row.getAttribute('href') || '').trim().slice(0, 24),
      offset: Math.round((s.left + s.width / 2) - (r.left + r.width / 2)),
    });
  }
  const navR = nav.getBoundingClientRect();
  const first = nav.querySelector('.mantine-NavLink-root');
  const fr = first ? first.getBoundingClientRect() : null;
  return {
    navWidth: Math.round(navR.width),
    gutters: fr ? { left: Math.round(fr.left - navR.left), right: Math.round(navR.right - fr.right) } : null,
    rows: out,
  };
})()`;

test('sidebar fits its rail and centres its icons when collapsed', async ({ page }) => {
  test.setTimeout(180000);
  mkdirSync(SHOTS, { recursive: true });
  await page.setViewportSize({ width: 1440, height: 900 });
  await login(page);
  await page.waitForTimeout(1200);

  const expanded = {
    overflow: (await page.evaluate(OVERFLOW_PROBE)) as any,
    symmetry: (await page.evaluate(SYMMETRY_PROBE)) as any,
  };
  await page.screenshot({ path: `${SHOTS}/sidebar-expanded.png`, clip: { x: 0, y: 0, width: 320, height: 900 } });

  await page.getByRole('button', { name: /collapse sidebar/i }).click();
  await page.waitForTimeout(700);

  const collapsed = {
    overflow: (await page.evaluate(OVERFLOW_PROBE)) as any,
    symmetry: (await page.evaluate(SYMMETRY_PROBE)) as any,
  };
  await page.screenshot({ path: `${SHOTS}/sidebar-collapsed.png`, clip: { x: 0, y: 0, width: 200, height: 900 } });

  writeFileSync(`${SHOTS}/sidebar-report.json`, JSON.stringify({ expanded, collapsed }, null, 2));

  // Nothing in the navbar may be cut off, in either state.
  expect(expanded.overflow.clipped, `expanded: ${JSON.stringify(expanded.overflow.clipped)}`).toEqual([]);
  expect(collapsed.overflow.clipped, `collapsed: ${JSON.stringify(collapsed.overflow.clipped)}`).toEqual([]);

  // Collapsed, every icon must sit centred in its highlight. (Expanded is not
  // checked: there the icon is deliberately left-aligned beside its label.)
  expect(collapsed.symmetry.navWidth).toBeLessThan(120);
  const offCentre = collapsed.symmetry.rows.filter((r: any) => Math.abs(r.offset) > 1);
  expect(offCentre, `off-centre icons: ${JSON.stringify(offCentre)}`).toEqual([]);

  // ...and the rail's own gutters must match, or the whole column looks shifted.
  const g = collapsed.symmetry.gutters;
  expect(Math.abs(g.left - g.right), `gutters ${JSON.stringify(g)}`).toBeLessThanOrEqual(2);
});
