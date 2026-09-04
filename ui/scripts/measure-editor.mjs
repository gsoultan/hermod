#!/usr/bin/env node
/**
 * Measure the workflow editor under load rather than reason about it.
 *
 * Creates a 14-node workflow, activates it, opens it in the editor, and pushes
 * messages through its webhook source for the whole sampling window so the
 * editor's telemetry socket is actually delivering frames — the store writes,
 * per-node subscriptions and edge re-renders are what the hot-path fixes
 * targeted, and an idle editor exercises none of them. Samples the JS heap
 * once a second and reports retained growth after a forced GC at each end.
 *
 * Run it against two UI servers proxying the same API to compare builds:
 *
 *   node scripts/measure-editor.mjs http://localhost:5175   # this checkout
 *   node scripts/measure-editor.mjs http://localhost:5176   # another worktree
 *
 * Needs a Chromium Playwright browser (the repo's E2E setup provides one).
 */
import { chromium } from 'playwright';

const base = process.argv[2] || 'http://localhost:5175';
const SECONDS = Number(process.argv[3] || 30);
const MSGS_PER_SEC = Number(process.argv[4] || 10);

const user = process.env.HERMOD_E2E_USER || 'admin';
const pass = process.env.HERMOD_E2E_PASS || 'admin';

const browser = await chromium.launch();
const context = await browser.newContext({ viewport: { width: 1600, height: 1000 }, baseURL: base });
const page = await context.newPage();

// --- login ---
await page.goto('/login');
await page.getByPlaceholder('Your username').fill(user);
await page.getByPlaceholder('Your password').fill(pass);
await page.click('button:has-text("Sign In")');
await page.waitForURL((u) => !u.pathname.includes('/login'), { timeout: 30000 });

const api = (path, init) =>
  page.evaluate(
    async ([p, method, body]) => {
      const headers = { 'Content-Type': 'application/json' };
      const m = document.cookie.match(/(?:^|;\s*)hermod_csrf=([^;]*)/);
      if (m) headers['X-CSRF-Token'] = decodeURIComponent(m[1]);
      const res = await fetch(p, { method, credentials: 'include', headers, body: body || undefined });
      return { status: res.status, body: await res.text() };
    },
    [path, init?.method || 'GET', init?.body ? JSON.stringify(init.body) : '']
  );

// --- fixture: webhook -> 12 transformations -> stdout ---
const name = `measure-${Date.now()}`;
const hook = `/api/webhooks/${name}`;
const src = await api('/api/sources', { method: 'POST', body: { name: `${name}-src`, type: 'webhook', config: { path: hook }, vhost: 'default' } });
const snk = await api('/api/sinks', { method: 'POST', body: { name: `${name}-snk`, type: 'stdout', config: {}, vhost: 'default' } });
if (src.status >= 400 || snk.status >= 400) throw new Error(`fixture failed: src ${src.status} ${src.body} snk ${snk.status} ${snk.body}`);
const srcID = JSON.parse(src.body).id;
const snkID = JSON.parse(snk.body).id;

const nodes = [{ id: 'n-src', type: 'source', ref_id: srcID, x: 60, y: 300 }];
const edges = [];
let prev = 'n-src';
for (let i = 0; i < 12; i++) {
  const id = `n-tx-${i}`;
  nodes.push({ id, type: 'transformation', config: { transType: 'set', [`column.f${i}`]: `'v${i}'` }, x: 260 + i * 200, y: 300 });
  edges.push({ id: `e-${i}`, source_id: prev, target_id: id });
  prev = id;
}
nodes.push({ id: 'n-snk', type: 'sink', ref_id: snkID, x: 260 + 12 * 200, y: 300 });
edges.push({ id: 'e-snk', source_id: prev, target_id: 'n-snk' });

const wf = await api('/api/workflows', { method: 'POST', body: { name, vhost: 'default', active: false, nodes, edges } });
if (wf.status >= 400) throw new Error(`workflow create failed: ${wf.status} ${wf.body}`);
const wfID = JSON.parse(wf.body).id;

// Activate through the same endpoint the UI's toggle uses. PATCH /status only
// writes the stored status field; /toggle is what starts the engine, and the
// webhook path is registered only once its source is actually running — until
// then every message posted to it is a 404 and the editor sees no telemetry.
const toggled = await api(`/api/workflows/${wfID}/toggle`, { method: 'POST', body: {} });
let activatedWith = null;
if (toggled.status < 300) {
  // Give the source time to register its path, and confirm it did.
  for (let i = 0; i < 20 && !activatedWith; i++) {
    await new Promise((r) => setTimeout(r, 500));
    const probe = await fetch(`${base}${hook}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{"probe":true}' });
    if (probe.ok) activatedWith = 'toggle';
  }
}
if (!activatedWith) console.warn(`could not bring the source up (toggle ${toggled.status}: ${toggled.body.slice(0, 120)}) — measuring an idle editor`);

// --- open the editor and count telemetry frames ---
let frames = 0;
page.on('websocket', (ws) => ws.on('framereceived', () => frames++));

const cdp = await context.newCDPSession(page);
await cdp.send('Performance.enable');
const heap = async () => {
  const { metrics } = await cdp.send('Performance.getMetrics');
  const get = (n) => metrics.find((x) => x.name === n)?.value ?? 0;
  return { used: get('JSHeapUsedSize'), nodes: get('Nodes'), listeners: get('JSEventListeners') };
};

await page.goto(`/workflows/${wfID}/edit`);
await page.locator('.react-flow__node').first().waitFor({ timeout: 30000 });
await page.waitForTimeout(2000);
await cdp.send('HeapProfiler.collectGarbage').catch(() => {});
const start = await heap();

// --- load + sampling ---
let sent = 0, failed = 0;
const pump = setInterval(() => {
  sent++;
  fetch(`${base}${hook}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ n: sent, ts: Date.now(), payload: 'x'.repeat(200) }),
  }).then((r) => { if (!r.ok) failed++; }).catch(() => failed++);
}, 1000 / MSGS_PER_SEC);

const samples = [];
for (let s = 0; s < SECONDS; s++) {
  await page.waitForTimeout(1000);
  samples.push(await heap());
}
clearInterval(pump);
await page.waitForTimeout(1500);
await cdp.send('HeapProfiler.collectGarbage').catch(() => {});
const end = await heap();

const mb = (b) => (b / 1048576).toFixed(1);
const peak = Math.max(...samples.map((x) => x.used));
console.log(`\n== editor under load, ${SECONDS}s, ${nodes.length} nodes / ${edges.length} edges — ${base}`);
console.log(`  workflow ${activatedWith ? `active (status="${activatedWith}")` : 'NOT ACTIVE'}; sent ${sent} messages (${failed} failed); ${frames} websocket frames received by the editor`);
console.log(`  start (after GC)   ${mb(start.used)} MB   ${start.nodes} DOM nodes   ${start.listeners} listeners`);
console.log(`  peak               ${mb(peak)} MB`);
console.log(`  end   (after GC)   ${mb(end.used)} MB   ${end.nodes} DOM nodes   ${end.listeners} listeners`);
console.log(`  retained growth    ${mb(end.used - start.used)} MB`);

// --- teardown ---
if (activatedWith) await api(`/api/workflows/${wfID}/toggle`, { method: 'POST', body: {} }).catch(() => {});
await api(`/api/workflows/${wfID}`, { method: 'DELETE' }).catch(() => {});
await api(`/api/sources/${srcID}`, { method: 'DELETE' }).catch(() => {});
await api(`/api/sinks/${snkID}`, { method: 'DELETE' }).catch(() => {});
await browser.close();
