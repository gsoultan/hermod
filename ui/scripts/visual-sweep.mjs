#!/usr/bin/env node
/**
 * Screenshot the main routes in both colour schemes at desktop and phone width.
 *
 * Seeds a source, a sink and a workflow first so lists and the editor have
 * content, then writes one PNG per route × scheme × viewport to /tmp/sweep/.
 * Also captures the two PWA manifest screenshots into public/screenshots/.
 *
 *   node scripts/visual-sweep.mjs http://localhost:5175
 */
import { chromium } from 'playwright';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const base = process.argv[2] || 'http://localhost:5175';
const OUT = '/tmp/sweep';
const here = path.dirname(fileURLToPath(import.meta.url));
const PUBLIC = path.resolve(here, '..', 'public', 'screenshots');
fs.mkdirSync(OUT, { recursive: true });
fs.mkdirSync(PUBLIC, { recursive: true });

const user = process.env.HERMOD_E2E_USER || 'admin';
const pass = process.env.HERMOD_E2E_PASS || 'admin';

const browser = await chromium.launch();

async function login(page) {
  await page.goto('/login');
  if (!/\/login/.test(page.url())) return;
  // Generous: the first load after a reset or a large edit compiles the whole
  // module graph on the dev server, and that took longer than 30s once.
  await page.getByPlaceholder('Your username').fill(user, { timeout: 120000 });
  await page.getByPlaceholder('Your password').fill(pass);
  await page.click('button:has-text("Sign In")');
  await page.waitForURL((u) => !u.pathname.includes('/login'), { timeout: 30000 });
}

const api = (page, p, init) =>
  page.evaluate(
    async ([p, method, body]) => {
      const headers = { 'Content-Type': 'application/json' };
      const m = document.cookie.match(/(?:^|;\s*)hermod_csrf=([^;]*)/);
      if (m) headers['X-CSRF-Token'] = decodeURIComponent(m[1]);
      const res = await fetch(p, { method, credentials: 'include', headers, body: body || undefined });
      return { status: res.status, body: await res.text() };
    },
    [p, init?.method || 'GET', init?.body ? JSON.stringify(init.body) : '']
  );

// --- seed once, in a throwaway context ---
const seedCtx = await browser.newContext({ baseURL: base });
const seed = await seedCtx.newPage();
await login(seed);
const src = await api(seed, '/api/sources', {
  method: 'POST',
  body: {
    name: `orders-db`, type: 'postgres', vhost: 'default',
    config: { host: 'db.internal', port: '5432', user: 'hermod', dbname: 'shop', tables: 'orders' },
    sample: JSON.stringify({ id: 1042, customer_email: 'ada@example.com', total: 129.5, status: 'paid' }),
  },
});
const snk = await api(seed, '/api/sinks', {
  method: 'POST',
  body: { name: `warehouse`, type: 'stdout', vhost: 'default', config: {} },
});
// Fail loudly. A crashed earlier run that never tore down leaves its names
// behind, the next create then conflicts, and every ID-based route silently
// becomes /undefined/edit — eight screenshots of a 404 toast, once.
for (const [what, r] of [['source', src], ['sink', snk]]) {
  if (r.status >= 300) throw new Error(`seed ${what} failed: ${r.status} ${r.body.slice(0, 200)}`);
}
const srcID = JSON.parse(src.body).id;
const snkID = JSON.parse(snk.body).id;
const wf = await api(seed, '/api/workflows', {
  method: 'POST',
  body: {
    name: 'Orders to warehouse', vhost: 'default', active: false,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: srcID, x: 100, y: 200 },
      { id: 'n-mask', type: 'transformation', config: { transType: 'mask', field: 'customer_email', maskType: 'email', label: 'Mask email' }, x: 360, y: 200 },
      { id: 'n-set', type: 'transformation', config: { transType: 'set', 'column.processed_at': 'now()', label: 'Stamp' }, x: 620, y: 200 },
      { id: 'n-snk', type: 'sink', ref_id: snkID, x: 880, y: 200 },
    ],
    edges: [
      { id: 'e1', source_id: 'n-src', target_id: 'n-mask' },
      { id: 'e2', source_id: 'n-mask', target_id: 'n-set' },
      { id: 'e3', source_id: 'n-set', target_id: 'n-snk' },
    ],
  },
});
if (wf.status >= 300) throw new Error(`seed workflow failed: ${wf.status} ${wf.body.slice(0, 200)}`);
const wfID = JSON.parse(wf.body).id;
await seedCtx.close();

const routes = [
  ['dashboard', '/'],
  ['sources', '/sources'],
  ['sinks', '/sinks'],
  ['workflows', '/workflows'],
  ['editor', `/workflows/${wfID}/edit`],
  ['source-new', '/sources/new'],
  ['sink-new', '/sinks/new'],
  ['source-edit', `/sources/${srcID}/edit`],
  ['logs', '/logs'],
  ['users', '/users'],
  ['settings', '/settings'],
];
const viewports = [['desktop', 1440, 900], ['phone', 390, 844]];

const problems = [];
for (const scheme of ['dark', 'light']) {
  for (const [vpName, w, h] of viewports) {
    const ctx = await browser.newContext({ baseURL: base, viewport: { width: w, height: h } });
    // Mantine reads this key before first paint (see index.html), so the
    // scheme is right from the first frame rather than toggled after load.
    await ctx.addInitScript((s) => localStorage.setItem('mantine-color-scheme-value', s), scheme);
    const page = await ctx.newPage();
    const errors = [];
    page.on('pageerror', (e) => errors.push(e.message));
    page.on('console', (m) => { if (m.type() === 'error' && !/401|WebSocket|Failed to fetch/.test(m.text())) errors.push(m.text().slice(0, 120)); });
    await login(page);

    for (const [name, route] of routes) {
      errors.length = 0;
      await page.goto(route, { waitUntil: 'networkidle' }).catch(() => {});
      await page.waitForTimeout(600);
      // Horizontal overflow at phone width is the classic mobile failure.
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      const file = `${OUT}/${scheme}-${vpName}-${name}.png`;
      await page.screenshot({ path: file, fullPage: false });
      if (overflow > 2) problems.push(`${scheme}/${vpName}/${name}: horizontal overflow ${overflow}px`);
      if (errors.length) problems.push(`${scheme}/${vpName}/${name}: ${errors[0]}`);
    }

    // Manifest screenshots: one wide, one narrow, dark scheme, real content.
    if (scheme === 'dark') {
      // Reuse the authenticated page. A second login() on this context found
      // /login rendering its signed-in state, with no form to fill.
      const mf = page;
      if (vpName === 'desktop') {
        await mf.setViewportSize({ width: 1280, height: 800 });
        await mf.goto(`/workflows/${wfID}/edit`, { waitUntil: 'networkidle' });
        await mf.waitForTimeout(1200);
        await mf.screenshot({ path: `${PUBLIC}/editor-wide.png` });
      } else {
        await mf.setViewportSize({ width: 390, height: 844 });
        await mf.goto('/workflows', { waitUntil: 'networkidle' });
        await mf.waitForTimeout(800);
        await mf.screenshot({ path: `${PUBLIC}/workflows-narrow.png` });
      }
    }
    await ctx.close();
  }
}

// Teardown seed data.
const t = await browser.newContext({ baseURL: base });
const tp = await t.newPage();
await login(tp);
await api(tp, `/api/workflows/${wfID}`, { method: 'DELETE' });
await api(tp, `/api/sources/${srcID}`, { method: 'DELETE' });
await api(tp, `/api/sinks/${snkID}`, { method: 'DELETE' });
await t.close();
await browser.close();

console.log(`wrote ${routes.length * 4} screenshots to ${OUT}, manifest shots to ${PUBLIC}`);
console.log(problems.length ? `\nPROBLEMS (${problems.length}):\n  ` + problems.join('\n  ') : '\nno overflow or console errors detected');
