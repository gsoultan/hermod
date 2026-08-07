import { test, type Page } from '@playwright/test';
import { writeFileSync } from 'fs';

/**
 * Form structure audit.
 *
 * Checks the things that make a form feel considered rather than assembled:
 * do fields explain themselves, are widths consistent within a row, is the
 * label/description ratio sane, and how deep is the form before a user has to
 * scroll.
 */

const FORM_ROUTES = ['/sources/new', '/sinks/new', '/workflows/new', '/settings', '/users/new', '/workers/new'];

const login = async (page: Page) => {
  await page.goto('/login');
  await page.getByPlaceholder('Your username').fill('admin');
  await page.getByPlaceholder('Your password').fill('admin');
  await page.getByRole('button', { name: /sign in|login/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/login'), { timeout: 30000 });
};

const AUDIT = `(() => {
  const inputs = [...document.querySelectorAll('input:not([type=hidden]):not([type=checkbox]):not([type=radio]), select, textarea')];
  const visible = inputs.filter((el) => {
    const r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0;
  });

  const info = visible.map((el) => {
    const wrapper = el.closest('.mantine-InputWrapper-root') || el.parentElement?.parentElement;
    const label = wrapper?.querySelector('label')?.textContent?.trim() || '';
    const desc = wrapper?.querySelector('.mantine-InputWrapper-description')?.textContent?.trim() || '';
    const r = el.getBoundingClientRect();
    return {
      label, desc,
      hasDesc: desc.length > 0,
      placeholder: el.getAttribute('placeholder') || '',
      width: Math.round(r.width),
      top: Math.round(r.top),
      required: el.hasAttribute('required') || (label.includes('*')),
    };
  });

  // Fields sharing a row (within 8px vertically) should share a width, or the
  // row reads as ragged.
  const rows = {};
  for (const f of info) {
    const key = Math.round(f.top / 8);
    (rows[key] ||= []).push(f);
  }
  const raggedRows = [];
  for (const [, fields] of Object.entries(rows)) {
    if (fields.length < 2) continue;
    const widths = fields.map((f) => f.width);
    const min = Math.min(...widths), max = Math.max(...widths);
    // Ignore rows that are intentionally asymmetric (a wide field + a button).
    if (max - min > 40 && min / max < 0.75) {
      raggedRows.push({ widths, labels: fields.map((f) => f.label).slice(0, 4) });
    }
  }

  return {
    total: visible.length,
    withDesc: info.filter((f) => f.hasDesc).length,
    withLabel: info.filter((f) => f.label).length,
    withPlaceholder: info.filter((f) => f.placeholder).length,
    noLabelNoPlaceholder: info.filter((f) => !f.label && !f.placeholder).length,
    raggedRows,
    pageHeight: document.documentElement.scrollHeight,
    viewportHeight: window.innerHeight,
    fields: info.map((f) => ({ label: f.label, hasDesc: f.hasDesc, width: f.width })),
  };
})()`;

test('form structure audit', async ({ page }) => {
  test.setTimeout(300000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await login(page);

  const report: any = {};
  for (const route of FORM_ROUTES) {
    await page.goto(route, { waitUntil: 'networkidle' }).catch(() => {});
    await page.waitForTimeout(400);
    report[route] = await page.evaluate(AUDIT);
  }

  writeFileSync('form-audit.json', JSON.stringify(report, null, 2));

  console.log('\n=== FORM AUDIT ===');
  for (const [route, r] of Object.entries<any>(report)) {
    const pct = r.total ? Math.round((r.withDesc / r.total) * 100) : 0;
    const scrolls = r.pageHeight > r.viewportHeight * 1.5;
    console.log(
      `${route.padEnd(16)} fields=${String(r.total).padStart(3)}  ` +
      `described=${String(pct).padStart(3)}%  ` +
      `unlabelled=${r.noLabelNoPlaceholder}  ` +
      `ragged-rows=${r.raggedRows.length}  ` +
      `height=${r.pageHeight}px${scrolls ? ' (long)' : ''}`
    );
    for (const rr of r.raggedRows.slice(0, 2)) {
      console.log(`      ragged: widths=${rr.widths.join('/')} labels=${rr.labels.filter(Boolean).join(', ')}`);
    }
  }
});
